package gateway

import (
	"encoding/json"
	"errors"
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
	s.logs = append(s.logs, log)
	if len(s.logs) > capacity {
		s.logs = append([]RequestLog(nil), s.logs[len(s.logs)-capacity:]...)
	}
}

func (s *runtimeState) metrics() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Metrics{StartedAt: s.started, Requests: s.total, Errors: s.errors, Tokens: s.tokens, Recent: []RequestLog{}, Timeline: []bucket{}, Health: map[string]Health{}}
	if s.total > 0 {
		out.AvgLatencyMS = s.latency / s.total
	}
	for k, v := range s.health {
		out.Health[k] = v
	}
	for i := len(s.logs) - 1; i >= 0 && len(out.Recent) < 8; i-- {
		out.Recent = append(out.Recent, s.logs[i])
	}
	start := time.Now().UTC().Truncate(time.Minute).Add(-29 * time.Minute)
	for i := 0; i < 30; i++ {
		b := bucket{Time: start.Add(time.Duration(i) * time.Minute)}
		for _, log := range s.logs {
			if !log.Time.Before(b.Time) && log.Time.Before(b.Time.Add(time.Minute)) {
				b.Requests++
				if log.Status >= 400 {
					b.Errors++
				}
			}
		}
		out.Timeline = append(out.Timeline, b)
	}
	return out
}

func (s *runtimeState) requestLogs() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RequestLog, len(s.logs))
	for i, l := range s.logs {
		out[len(s.logs)-i-1] = l
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
}

func openAffinity(dir string) (*affinityStore, error) {
	s := &affinityStore{path: filepath.Join(dir, "affinity.json"), bindings: map[string]Binding{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &s.bindings)
	return s, err
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

func (s *affinityStore) put(scope string, binding Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.bindings[scope]; ok && old.UpstreamID == binding.UpstreamID && old.Fingerprint == binding.Fingerprint {
		return nil
	}
	next := make(map[string]Binding, len(s.bindings)+1)
	for key, b := range s.bindings {
		if time.Now().Before(b.Expires) {
			next[key] = b
		}
	}
	if len(next) >= 100000 {
		return errors.New("response routing capacity reached")
	}
	next[scope] = binding
	if err := config.AtomicJSON(s.path, next); err != nil {
		return err
	}
	s.bindings = next
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
