package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

type Principal struct {
	ID       string
	Name     string
	UserID   string
	UserName string
}

type Gateway struct {
	restorePending atomic.Bool
	store          *config.Store
	client         *http.Client
	runtime        *runtimeState
	affinity       *affinityStore
	usage          *usage.Store
	credentialMu   sync.Mutex
	providers      map[string]aws.CredentialsProvider
}

func New(store *config.Store, dir string) (*Gateway, error) {
	affinity, err := openAffinity(dir)
	if err != nil {
		return nil, err
	}
	accounting, err := usage.Open(dir)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 200, MaxIdleConnsPerHost: 20,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, DisableCompression: true,
	}
	g := &Gateway{
		store: store, affinity: affinity, usage: accounting, runtime: newRuntime(), providers: map[string]aws.CredentialsProvider{},
		client: &http.Client{
			Transport: transport,
			// A redirect must not receive credentials for a configured upstream.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if err := g.recoverRestore(); err != nil {
		_ = g.Close()
		return nil, fmt.Errorf("recover pending backup restore: %w", err)
	}
	return g, nil
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	kind := "invalid_request_error"
	if status == 401 {
		kind = "authentication_error"
	} else if status >= 500 {
		kind = "server_error"
	} else if status == 429 {
		kind = "rate_limit_error"
	}
	JSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": kind, "param": nil, "code": code}})
}

var resourceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func validRoute(method, path string) bool {
	if path == "/chat/completions" || path == "/responses" {
		return method == http.MethodPost
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "responses" && resourceID.MatchString(parts[1]) {
		if len(parts) == 2 {
			return method == http.MethodGet || method == http.MethodDelete
		}
		if len(parts) == 3 {
			return (parts[2] == "cancel" && method == http.MethodPost) || (parts[2] == "input_items" && method == http.MethodGet)
		}
	}
	return false
}

func (g *Gateway) Close() error { g.client.CloseIdleConnections(); return g.usage.Close() }
func (g *Gateway) Usage(ctx context.Context, f usage.Filter) (usage.Report, error) {
	return g.usage.Report(ctx, f)
}
func (g *Gateway) Metrics() Metrics {
	m := g.runtime.metrics()
	m.UsageStorageHealthy = g.usage.Healthy()
	return m
}
func (g *Gateway) Logs() []RequestLog { return g.runtime.requestLogs() }

func (g *Gateway) TestUpstream(ctx context.Context, id string) ([]Model, error) {
	for _, u := range g.store.Snapshot().Upstreams {
		if u.ID == id {
			return g.discover(ctx, u, u.Project, false)
		}
	}
	return nil, errors.New("upstream not found")
}

func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	// Keep discovery workers inside the request lifetime, including cancellation,
	// so a subsequent migration cannot race late cache/credential writes.
	var workers sync.WaitGroup
	defer workers.Wait()
	type result struct {
		models []Model
		err    error
	}
	results := make(chan result, 100)
	limit := make(chan struct{}, 4)
	count := 0
	project := r.Header.Get("OpenAI-Project")
	for _, u := range g.store.Snapshot().Upstreams {
		if !u.Enabled {
			continue
		}
		count++
		if len(u.Models) > 0 {
			mapped := []Model{}
			for alias := range u.Models {
				mapped = append(mapped, Model{ID: alias, Object: "model", Created: u.CreatedAt.Unix(), OwnedBy: "mantle-mask"})
			}
			results <- result{models: mapped}
			continue
		}
		workers.Add(1)
		go func(u config.Upstream) {
			defer workers.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-r.Context().Done():
				results <- result{err: r.Context().Err()}
				return
			}
			effectiveProject := project
			if u.Project != "" {
				effectiveProject = u.Project
			}
			models, err := g.discover(r.Context(), u, effectiveProject, true)
			results <- result{models: models, err: err}
		}(u)
	}
	if count == 0 {
		Error(w, 503, "no_upstreams", "No enabled upstreams. Add an upstream in the dashboard.")
		return
	}
	all, failures := map[string]Model{}, 0
	for i := 0; i < count; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				failures++
			}
			for _, model := range result.models {
				all[model.ID] = model
			}
		case <-r.Context().Done():
			Error(w, 504, "upstream_timeout", "Model discovery timed out.")
			return
		}
	}
	if failures == count {
		Error(w, 502, "model_discovery_failed", "All upstream model discovery requests failed. Check upstream credentials and base URLs.")
		return
	}
	if failures > 0 {
		w.Header().Set("X-Mantle-Partial", "true")
	}
	JSON(w, 200, map[string]any{"object": "list", "data": sortedModels(all)})
}

func (g *Gateway) Proxy(w http.ResponseWriter, r *http.Request, principal Principal) {
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	st := g.store.Snapshot()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(st.Settings.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if path == "/models" && r.Method == http.MethodGet {
		g.models(w, r)
		return
	}
	if !validRoute(r.Method, path) {
		Error(w, 404, "unsupported_endpoint", "Supported endpoints: /v1/models, /v1/chat/completions, and /v1/responses.")
		return
	}
	start := time.Now()
	log := RequestLog{ID: w.Header().Get("X-Request-Id"), Time: start.UTC(), Method: r.Method, Path: r.URL.Path,
		KeyID: principal.ID, KeyName: principal.Name, UserID: principal.UserID, UserName: principal.UserName,
		Inference: r.Method == http.MethodPost && (path == "/chat/completions" || path == "/responses")}
	if log.ID == "" {
		log.ID = config.Token("req_")[:28]
	}
	accounted := false
	defer func() {
		log.DurationMS = time.Since(start).Milliseconds()
		if accounted {
			if err := g.usage.Finish(usage.Request{ID: log.ID, Model: log.Model, Status: log.Status, Attempts: log.Attempts, DurationMS: log.DurationMS}); err != nil {
				log.Error = "usage_persistence_failed"
				slog.Error("could not finish usage record", "request_id", log.ID)
			}
		}
		g.runtime.record(log, st.Settings.LogCapacity)
	}()
	fail := func(status int, code, message string) {
		log.Status, log.Error = status, code
		Error(w, status, code, message)
	}
	if log.Inference {
		if err := g.usage.Begin(usage.Request{ID: log.ID, StartedAt: start, UserID: principal.UserID, UserName: principal.UserName,
			KeyID: principal.ID, KeyName: principal.Name, Path: r.URL.Path}); err != nil {
			fail(503, "usage_storage_unavailable", "Usage storage is unavailable. No upstream request was sent.")
			return
		}
		accounted = true
	}
	body := []byte(nil)
	fields := map[string]json.RawMessage{}
	if r.Method == http.MethodPost {
		var err error
		body, err = boundedRead(r.Body, 16<<20)
		if err != nil {
			fail(413, "request_too_large", "Request body could not be read or exceeds 16 MiB.")
			return
		}
		if len(body) == 0 && strings.HasSuffix(path, "/cancel") {
			body = []byte("{}")
		}
		if !json.Valid(body) || json.Unmarshal(body, &fields) != nil || fields == nil {
			fail(400, "invalid_json", "Request body must be one JSON object.")
			return
		}
	}
	var model, previous string
	if raw, ok := fields["model"]; ok && (json.Unmarshal(raw, &model) != nil || model == "") {
		fail(400, "invalid_model", "model must be a non-empty string.")
		return
	}
	if raw, ok := fields["previous_response_id"]; ok && string(raw) != "null" {
		if json.Unmarshal(raw, &previous) != nil || !resourceID.MatchString(previous) {
			fail(400, "invalid_previous_response_id", "previous_response_id must be a response ID.")
			return
		}
	}
	if raw, ok := fields["stream"]; ok && json.Unmarshal(raw, &log.Stream) != nil {
		fail(400, "invalid_stream", "stream must be a boolean.")
		return
	}
	if path == "/chat/completions" && log.Stream {
		// Ask for reported streaming usage unless the caller explicitly opts out.
		options := map[string]json.RawMessage{}
		if raw, ok := fields["stream_options"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &options) != nil || options == nil {
				fail(400, "invalid_stream_options", "stream_options must be an object.")
				return
			}
		}
		if _, exists := options["include_usage"]; !exists {
			options["include_usage"] = json.RawMessage("true")
			fields["stream_options"], _ = json.Marshal(options)
			body, _ = json.Marshal(fields)
		}
	}
	log.Model = model
	if (path == "/chat/completions" || path == "/responses") && model == "" && previous == "" {
		fail(400, "model_required", "model is required for a new request.")
		return
	}
	// Conversations API IDs need their own routing registry. Reject instead of
	// silently moving a server-side conversation between accounts or regions.
	if raw, ok := fields["conversation"]; ok && string(raw) != "null" {
		fail(400, "unsupported_conversation", "Use previous_response_id or explicit input history; Conversations API routing is not supported.")
		return
	}
	requestedProject := r.Header.Get("OpenAI-Project")
	pinned := ""
	binding := Binding{}
	if strings.HasPrefix(path, "/responses/") {
		previous = strings.Split(path, "/")[2]
	}
	if previous != "" {
		var ok bool
		binding, ok = g.affinity.get(scopeKey(principal.ID, requestedProject, previous))
		if !ok {
			fail(404, "response_not_found", "Response routing not found for this API key and project. Use a response created through this gateway.")
			return
		}
		pinned = binding.UpstreamID
		if model == "" {
			log.Model = binding.Model
		}
	}
	tried := map[string]bool{}
	var lastBody []byte
	lastStatus := 503
	lastHeaders := http.Header{}
	lastCode := "no_available_upstream"
	for attempt := 0; attempt <= st.Settings.MaxRetries; attempt++ {
		u, ok := g.runtime.choose(st.Upstreams, model, tried, pinned)
		if !ok {
			break
		}
		if pinned != "" && fingerprint(u) != binding.Fingerprint {
			fail(409, "upstream_changed", "The original upstream configuration changed; its stored response cannot be routed safely.")
			return
		}
		tried[u.ID] = true
		log.Upstream, log.UpstreamName, log.Attempts = u.ID, u.Name, attempt+1
		if accounted {
			if err := g.usage.Attempt(usage.Request{ID: log.ID, Model: log.Model, Attempts: log.Attempts, UpstreamID: u.ID, UpstreamName: u.Name}); err != nil {
				fail(503, "usage_storage_unavailable", "Could not persist the upstream attempt.")
				return
			}
		}
		project := requestedProject
		if u.Project != "" {
			project = u.Project
		}
		if pinned != "" {
			project = binding.Project
		}
		payload := body
		if mapped, exists := u.Models[model]; exists && mapped != model {
			fields["model"], _ = json.Marshal(mapped)
			payload, _ = json.Marshal(fields)
		} else if model != "" {
			// Undo any mapping from a previous failed attempt.
			fields["model"], _ = json.Marshal(model)
		}
		attemptStart := time.Now()
		resp, err := g.upstreamRequest(ctx, u, r.Method, path, r.URL.RawQuery, payload, r.Header, project)
		if err != nil {
			lastStatus, lastCode, lastBody = 502, "upstream_connection_failed", nil
			g.runtime.mark(u.ID, 502, time.Since(attemptStart), time.Duration(st.Settings.CooldownSeconds)*time.Second, "连接或鉴权失败")
			if ctx.Err() != nil || pinned != "" {
				break
			}
			continue
		}
		cooldown := time.Duration(st.Settings.CooldownSeconds) * time.Second
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			cooldown = max(cooldown, min(time.Duration(seconds)*time.Second, 10*time.Minute))
		} else if until, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			cooldown = max(cooldown, min(time.Until(until), 10*time.Minute))
		}
		g.runtime.mark(u.ID, resp.StatusCode, time.Since(attemptStart), cooldown, "")
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastStatus, lastCode, lastHeaders = resp.StatusCode, "upstream_error", resp.Header
			raw, _ := boundedRead(resp.Body, 1<<20)
			_ = resp.Body.Close()
			lastBody = g.redactError(raw, u)
			if retryable(resp.StatusCode) && pinned == "" {
				continue
			}
			break
		}
		expectedChoices := 1
		if raw, ok := fields["n"]; ok {
			var n int
			if json.Unmarshal(raw, &n) == nil && n > 0 {
				expectedChoices = n
			}
		}
		obs := &observer{expectedChoices: expectedChoices}
		responseScope := ""
		obs.onUsage = func(c usage.Counters) error {
			if accounted {
				return g.usage.Tokens(log.ID, c)
			}
			if responseScope != "" && r.Method != http.MethodDelete {
				return g.usage.Reconcile(responseScope, c)
			}
			return nil
		}
		if strings.HasPrefix(path, "/responses") {
			obs.onResponse = func(id string) error {
				responseScope = scopeKey(principal.ID, requestedProject, id)
				if accounted {
					if err := g.usage.Bind(responseScope, log.ID); err != nil {
						return err
					}
				}
				return g.affinity.put(responseScope, Binding{
					UpstreamID: u.ID, Fingerprint: fingerprint(u), Model: log.Model,
					Project: project, Expires: time.Now().UTC().Add(30 * 24 * time.Hour),
				})
			}
		}
		log.Status = resp.StatusCode
		isStream := strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
		if isStream {
			copyHeaders(w.Header(), resp.Header)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(resp.StatusCode)
			controller := http.NewResponseController(w)
			_ = controller.Flush()
			buf := make([]byte, 32<<10)
			cleanEOF := false
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					if err := obs.write(buf[:n]); err != nil {
						log.Status, log.Error = 502, "response_observation_failed"
						break
					}
					_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
					if _, err := w.Write(buf[:n]); err != nil {
						log.Status, log.Error = 499, "client_disconnected"
						break
					}
					if err := controller.Flush(); err != nil {
						log.Status, log.Error = 499, "client_disconnected"
						break
					}
				}
				if readErr != nil {
					cleanEOF = readErr == io.EOF
					if readErr != io.EOF || !obs.complete {
						log.Status, log.Error = 502, "stream_interrupted"
					}
					break
				}
			}
			_ = controller.SetWriteDeadline(time.Time{})
			_ = resp.Body.Close()
			if ctx.Err() != nil {
				log.Status, log.Error = 504, "upstream_timeout"
				if r.Context().Err() == context.Canceled {
					log.Status, log.Error = 499, "client_disconnected"
				}
			}
			if log.Status < 400 && cleanEOF && obs.sawChat && obs.complete && !obs.sawDone && !obs.failed {
				// Normalize Mantle's valid finish_reason + EOF ending to the
				// conventional OpenAI sentinel; existing event bytes stay intact.
				_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
					log.Status, log.Error = 499, "client_disconnected"
				} else if err := controller.Flush(); err != nil {
					log.Status, log.Error = 499, "client_disconnected"
				}
				_ = controller.SetWriteDeadline(time.Time{})
			}
			if log.Status >= 500 && !obs.failed {
				// A truncated SSE response must be observable to SDK callers,
				// even though the HTTP status has already been committed.
				data, _ := json.Marshal(map[string]any{
					"type": "error", "code": log.Error, "message": "Upstream stream ended before completion.",
					"error": map[string]string{"type": "server_error", "code": log.Error, "message": "Upstream stream ended before completion."},
				})
				_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
				_ = controller.Flush()
				_ = controller.SetWriteDeadline(time.Time{})
			}
		} else {
			data, err := boundedRead(resp.Body, 32<<20)
			_ = resp.Body.Close()
			if err != nil {
				fail(502, "upstream_read_failed", "Could not read upstream response.")
				return
			}
			if err := obs.json(data); err != nil {
				fail(500, "response_persistence_failed", "Could not save response routing or usage.")
				return
			}
			copyHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
			if _, err := w.Write(data); err != nil {
				log.Status, log.Error = 499, "client_disconnected"
			}
		}
		if obs.failed {
			log.Status, log.Error = 502, "upstream_response_failed"
		}
		if log.Inference {
			log.InputTokens, log.OutputTokens = obs.input, obs.output
			log.CachedTokens, log.ReasoningTokens, log.UsageKnown = obs.cached, obs.reasoning, obs.known
		}
		return
	}
	if ctx.Err() != nil {
		fail(504, "upstream_timeout", "The upstream request timed out or was canceled.")
		return
	}
	log.Status, log.Error = lastStatus, lastCode
	copyHeaders(w.Header(), lastHeaders)
	if len(lastBody) > 0 {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(lastBody, &envelope) == nil && len(envelope["error"]) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(lastStatus)
			_, _ = w.Write(lastBody)
			return
		}
	}
	if lastStatus < 400 {
		lastStatus, log.Status = 502, 502
	}
	message := fmt.Sprintf("Upstream request failed (HTTP %d). Check upstream status and credentials.", lastStatus)
	if len(tried) == 0 {
		message = "No enabled upstream is available for this model; check model mappings and cooldown status."
	}
	Error(w, lastStatus, lastCode, message)
}

func copyHeaders(dst, src http.Header) {
	for name, values := range src {
		key := strings.ToLower(name)
		if key == "content-type" || key == "retry-after" || key == "openai-processing-ms" || strings.HasPrefix(key, "x-ratelimit-") {
			dst[name] = append([]string(nil), values...)
		}
		if key == "x-request-id" || key == "x-amzn-requestid" {
			dst.Set("X-Upstream-Request-Id", src.Get(name))
		}
	}
}
