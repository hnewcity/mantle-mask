package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

type RequestLog struct {
	ID              string    `json:"id"`
	Time            time.Time `json:"time"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	Model           string    `json:"model"`
	Upstream        string    `json:"upstream"`
	UpstreamName    string    `json:"upstream_name"`
	KeyName         string    `json:"key_name"`
	KeyID           string    `json:"key_id"`
	UserID          string    `json:"user_id"`
	UserName        string    `json:"user_name"`
	Status          int       `json:"status"`
	DurationMS      int64     `json:"duration_ms"`
	Stream          bool      `json:"stream"`
	Attempts        int       `json:"attempts"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	CachedTokens    int64     `json:"cached_tokens"`
	ReasoningTokens int64     `json:"reasoning_tokens"`
	UsageKnown      bool      `json:"usage_known"`
	Inference       bool      `json:"inference"`
	Error           string    `json:"error,omitempty"`
}

type Health struct {
	Status        string    `json:"status"`
	CheckedAt     time.Time `json:"checked_at"`
	CooldownUntil time.Time `json:"cooldown_until"`
	LatencyMS     int64     `json:"latency_ms"`
	Message       string    `json:"message,omitempty"`
}

type bucket struct {
	Time     time.Time `json:"time"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
}

type Metrics struct {
	StartedAt           time.Time         `json:"started_at"`
	Requests            int64             `json:"requests"`
	Errors              int64             `json:"errors"`
	Tokens              int64             `json:"tokens"`
	AvgLatencyMS        int64             `json:"avg_latency_ms"`
	Recent              []RequestLog      `json:"recent"`
	Timeline            []bucket          `json:"timeline"`
	Health              map[string]Health `json:"health"`
	UsageStorageHealthy bool              `json:"usage_storage_healthy"`
}

type runtimeState struct {
	mu         sync.Mutex
	started    time.Time
	logs       []RequestLog
	logStart   int // index of the oldest retained entry
	total      int64
	errors     int64
	tokens     int64
	latency    int64
	health     map[string]Health
	current    map[string]int
	modelCache map[string]cachedModels
}

type cachedModels struct {
	Models  []Model
	Expires time.Time
}

func newRuntime() *runtimeState {
	return &runtimeState{started: time.Now().UTC(), logs: []RequestLog{}, health: map[string]Health{}, current: map[string]int{}, modelCache: map[string]cachedModels{}}
}

func (s *runtimeState) record(log RequestLog, capacity int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	if log.Status >= 400 {
		s.errors++
	}
	s.latency += log.DurationMS
	s.tokens += log.InputTokens + log.OutputTokens
	capacity = max(0, capacity)
	if cap(s.logs) != capacity {
		// Settings can change while requests are in flight. Resize once per
		// capacity change, retaining the newest entries in completion order.
		keep := min(len(s.logs), capacity)
		next := make([]RequestLog, keep, capacity)
		for i := range keep {
			next[i] = s.logs[(s.logStart+len(s.logs)-keep+i)%len(s.logs)]
		}
		s.logs, s.logStart = next, 0
	}
	if capacity == 0 {
		return
	}
	if len(s.logs) < capacity {
		s.logs = append(s.logs, log)
	} else {
		s.logs[s.logStart] = log
		s.logStart = (s.logStart + 1) % capacity
	}
}

func (s *runtimeState) metrics() Metrics {
	return s.metricsAt(time.Now().UTC())
}

func (s *runtimeState) metricsAt(now time.Time) Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Metrics{StartedAt: s.started, Requests: s.total, Errors: s.errors, Tokens: s.tokens, Recent: make([]RequestLog, 0, min(8, len(s.logs))), Health: map[string]Health{}}
	if s.total > 0 {
		out.AvgLatencyMS = s.latency / s.total
	}
	for k, v := range s.health {
		out.Health[k] = v
	}
	for i := len(s.logs) - 1; i >= 0 && len(out.Recent) < 8; i-- {
		out.Recent = append(out.Recent, s.logs[(s.logStart+i)%len(s.logs)])
	}
	start := now.UTC().Truncate(time.Minute).Add(-29 * time.Minute)
	end := start.Add(30 * time.Minute)
	out.Timeline = make([]bucket, 30)
	for i := range out.Timeline {
		out.Timeline[i].Time = start.Add(time.Duration(i) * time.Minute)
	}
	// Logs arrive in completion order, not start-time order: scan all retained
	// entries once rather than scanning the entire buffer for each minute.
	for _, log := range s.logs {
		if log.Time.Before(start) || !log.Time.Before(end) {
			continue
		}
		b := &out.Timeline[int(log.Time.Sub(start)/time.Minute)]
		b.Requests++
		if log.Status >= 400 {
			b.Errors++
		}
	}
	return out
}

func (s *runtimeState) requestLogs() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RequestLog, len(s.logs))
	for i := range s.logs {
		out[len(s.logs)-i-1] = s.logs[(s.logStart+i)%len(s.logs)]
	}
	return out
}

func healthKey(u config.Upstream) string {
	return config.Hash(u.ID + "\x00" + fingerprint(u) + "\x00" + u.Secret + "\x00" + u.SigningService)
}

func (s *runtimeState) mark(u config.Upstream, status int, elapsed time.Duration, cooldown time.Duration, message string) {
	id := healthKey(u)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.health[id]
	h.CheckedAt, h.LatencyMS = time.Now().UTC(), elapsed.Milliseconds()
	h.Message = message
	switch {
	case status >= 200 && status < 300:
		h.Status, h.CooldownUntil = "healthy", time.Time{}
	case retryable(status):
		h.Status, h.CooldownUntil = "cooldown", time.Now().Add(cooldown)
	case status == 401 || status == 403:
		h.Status = "error"
	default:
		h.Status = "reachable"
	}
	s.health[id] = h
}

// Smooth weighted round robin; each failed upstream is tried at most once.
func (s *runtimeState) choose(upstreams []config.Upstream, model string, tried map[string]bool, pinned string) (config.Upstream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var candidates []config.Upstream
	for _, u := range upstreams {
		if !u.Enabled || tried[u.ID] || (pinned != "" && u.ID != pinned) {
			continue
		}
		if model != "" && len(u.Models) > 0 {
			if _, ok := u.Models[model]; !ok {
				continue
			}
		}
		if pinned == "" && time.Now().Before(s.health[healthKey(u)].CooldownUntil) {
			continue
		}
		candidates = append(candidates, u)
	}
	if len(candidates) == 0 {
		return config.Upstream{}, false
	}
	best, total := 0, 0
	for i, u := range candidates {
		s.current[u.ID] += u.Weight
		total += u.Weight
		if s.current[u.ID] > s.current[candidates[best].ID] {
			best = i
		}
	}
	s.current[candidates[best].ID] -= total
	return candidates[best], true
}

type Binding struct {
	UpstreamID  string    `json:"upstream_id"`
	Fingerprint string    `json:"fingerprint"`
	Model       string    `json:"model"`
	Project     string    `json:"project"`
	Expires     time.Time `json:"expires"`
}

type affinityStore struct {
	mu       sync.Mutex
	path     string
	bindings map[string]Binding
	journal  *os.File // append-only puts since the last snapshot of path
	size     int64    // journal length after the last complete append
	logged   int      // entries in the journal
}

type affinityEntry struct {
	Scope   string  `json:"scope"`
	Binding Binding `json:"binding"`
}

const affinityCapacity = 100000

// openAffinity loads the last snapshot, replays the journal on top of it, then
// compacts so new appends never follow a record torn by a crash.
func openAffinity(dir string) (*affinityStore, error) {
	s := &affinityStore{path: filepath.Join(dir, "affinity.json"), bindings: map[string]Binding{}}
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &s.bindings); err != nil {
			return nil, err
		}
		if s.bindings == nil {
			s.bindings = map[string]Binding{}
		}
	}
	data = nil
	s.journal, err = os.OpenFile(s.journalPath(), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	replayed, err := s.replay()
	if err == nil && replayed {
		err = s.replaceLocked(s.bindings)
	}
	if err != nil {
		_ = s.journal.Close()
		return nil, err
	}
	return s, nil
}

func (s *affinityStore) journalPath() string { return s.path + ".log" }

func (s *affinityStore) replay() (bool, error) {
	info, err := s.journal.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	scanner := bufio.NewScanner(io.NewSectionReader(s.journal, 0, info.Size()))
	scanner.Buffer(make([]byte, 0, 4<<10), 1<<20)
	torn := false
	for scanner.Scan() {
		if torn {
			return true, errors.New("corrupt response routing journal")
		}
		var entry affinityEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.Scope == "" {
			// Only the final record can be incomplete after a crash.
			torn = true
			continue
		}
		s.bindings[entry.Scope] = entry.Binding
	}
	return true, scanner.Err()
}

// close folds the journal into affinity.json on a clean shutdown, so the data
// directory stays readable by versions without the journal. Failure is not
// fatal: the journal still holds every record and is replayed on next open.
func (s *affinityStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logged > 0 {
		_ = s.replaceLocked(s.bindings)
	}
	return s.journal.Close()
}

func scopeKey(principal, project, id string) string {
	return config.Hash(principal + "\x00" + project + "\x00" + id)
}

func fingerprint(u config.Upstream) string {
	return config.Hash(u.BaseURL + "\x00" + u.Region + "\x00" + u.Project + "\x00" + u.AuthMode + "\x00" + u.Profile)
}

func (s *affinityStore) get(scope string) (Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bindings[scope]
	return b, ok && time.Now().Before(b.Expires)
}

// put durably appends one record instead of rewriting every binding, so the
// cost of creating a response no longer grows with the retained history.
func (s *affinityStore) put(scope string, binding Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.bindings[scope]; ok && old.UpstreamID == binding.UpstreamID && old.Fingerprint == binding.Fingerprint {
		return nil
	}
	if _, exists := s.bindings[scope]; !exists && len(s.bindings) >= affinityCapacity {
		s.pruneExpiredLocked()
		if len(s.bindings) >= affinityCapacity {
			return errors.New("response routing capacity reached")
		}
	}
	line, err := json.Marshal(affinityEntry{Scope: scope, Binding: binding})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err = s.journal.Write(line); err == nil {
		err = s.journal.Sync()
	}
	if err != nil {
		// Drop a partial record so later appends stay line-aligned.
		_ = s.journal.Truncate(s.size)
		return err
	}
	s.size += int64(len(line))
	s.logged++
	s.bindings[scope] = binding
	if s.logged >= max(1024, len(s.bindings)) {
		// Amortized compaction; the journal already holds the record durably.
		_ = s.replaceLocked(s.bindings)
	}
	return nil
}

func (s *affinityStore) pruneExpiredLocked() {
	now := time.Now()
	for key, b := range s.bindings {
		if !now.Before(b.Expires) {
			delete(s.bindings, key)
		}
	}
}

// replaceLocked publishes bindings as the new snapshot and empties the
// journal. Expired bindings are dropped. The caller holds s.mu.
func (s *affinityStore) replaceLocked(bindings map[string]Binding) error {
	now := time.Now()
	err := config.AtomicWrite(s.path, func(w io.Writer) error {
		if _, err := io.WriteString(w, "{"); err != nil {
			return err
		}
		first := true
		for key, b := range bindings {
			if !now.Before(b.Expires) {
				continue
			}
			k, err := json.Marshal(key)
			if err != nil {
				return err
			}
			v, err := json.Marshal(b)
			if err != nil {
				return err
			}
			if !first {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			first = false
			if _, err := w.Write(k); err != nil {
				return err
			}
			if _, err := io.WriteString(w, ":"); err != nil {
				return err
			}
			if _, err := w.Write(v); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "}")
		return err
	})
	if err != nil {
		return err
	}
	s.bindings = bindings
	s.pruneExpiredLocked()
	// The snapshot already contains every journaled record, so replaying a
	// journal that failed to truncate is harmless.
	if err := s.journal.Truncate(0); err != nil {
		return err
	}
	s.size, s.logged = 0, 0
	return nil
}

func sortedModels(models map[string]Model) []Model {
	out := make([]Model, 0, len(models))
	for _, m := range models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
