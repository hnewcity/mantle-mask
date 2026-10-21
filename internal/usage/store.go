// Package usage persists observed upstream token counts. It never estimates
// tokens from text and never stores prompts, completions, or credentials.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

type Counters struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type Request struct {
	ID, UserID, UserName, KeyID, KeyName, Model, Path string
	UpstreamID, UpstreamName                          string
	StartedAt                                         time.Time
	Status, Attempts                                  int
	DurationMS                                        int64
}

type Totals struct {
	Requests             int64 `json:"requests"`
	Errors               int64 `json:"errors"`
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	ReasoningTokens      int64 `json:"reasoning_tokens"`
	KnownUsageRequests   int64 `json:"known_usage_requests"`
	MissingUsageRequests int64 `json:"missing_usage_requests"`
	InFlightRequests     int64 `json:"in_flight_requests"`
	PendingSyncRequests  int64 `json:"pending_sync_requests"`
}

// Cost distinguishes a configured zero price from requests without a price.
// It is recalculated with the current price configuration, never a billing ledger.
type Cost struct {
	EstimatedUSD     float64 `json:"estimated_usd"`
	PricedRequests   int64   `json:"priced_requests"`
	UnpricedRequests int64   `json:"unpriced_requests"`
}

type Group struct {
	Cost   Cost   `json:"cost"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	UserID string `json:"user_id,omitempty"`
	Totals
}

// DailyModel preserves the date/model cross-dimension for the usage chart.
type DailyModel struct {
	Cost  Cost   `json:"cost"`
	Date  string `json:"date"`
	Model string `json:"model"`
	Totals
}

type Report struct {
	Cost           Cost         `json:"cost"`
	From           string       `json:"from"`
	To             string       `json:"to"`
	TimezoneOffset int          `json:"timezone_offset"`
	All            bool         `json:"all"`
	Totals         Totals       `json:"totals"`
	ByUser         []Group      `json:"by_user"`
	ByKey          []Group      `json:"by_key"`
	ByModel        []Group      `json:"by_model"`
	ByUpstream     []Group      `json:"by_upstream,omitempty"`
	Daily          []Group      `json:"daily"`
	DailyByModel   []DailyModel `json:"daily_by_model"`
}

type Filter struct {
	From, To                         string
	UserID, KeyID, Model, UpstreamID string
	TimezoneOffset                   int
	All                              bool
	start, end                       int64
}

func ParseFilter(values url.Values, now time.Time) (Filter, error) {
	f := Filter{UserID: values.Get("user_id"), KeyID: values.Get("key_id"), Model: values.Get("model"), UpstreamID: values.Get("upstream_id"), All: values.Get("all") == "true"}
	if raw := values.Get("timezone_offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < -840 || n > 840 {
			return f, errors.New("timezone_offset must be between -840 and 840 minutes")
		}
		f.TimezoneOffset = n
	}
	zone := time.FixedZone("report", f.TimezoneOffset*60)
	f.From, f.To = values.Get("from"), values.Get("to")
	if f.All {
		if f.From != "" || f.To != "" {
			return f, errors.New("all cannot be combined with from or to")
		}
		return f, nil
	}
	if f.To == "" {
		f.To = now.In(zone).Format(time.DateOnly)
	}
	to, err := time.ParseInLocation(time.DateOnly, f.To, zone)
	if err != nil {
		return f, errors.New("to must be YYYY-MM-DD")
	}
	if f.From == "" {
		f.From = to.AddDate(0, 0, -29).Format(time.DateOnly)
	}
	from, err := time.ParseInLocation(time.DateOnly, f.From, zone)
	if err != nil || from.After(to) || to.Sub(from) > 365*24*time.Hour {
		return f, errors.New("from must be YYYY-MM-DD, no later than to; maximum range is 366 days")
	}
	f.start, f.end = from.UnixMilli(), to.AddDate(0, 0, 1).UnixMilli()
	return f, nil
}

type Store struct {
	reader      *sql.DB
	coordinator *sync.RWMutex
	db          *sql.DB
	sql         dialect
	lock        *mysqlLock
	healthy     atomic.Bool
}

// dialect holds the statements whose upsert and scalar MAX syntax differ.
type dialect struct {
	mysql                        bool
	bindLink, tokenUpdate, merge string
}

var sqliteDialect = dialect{
	bindLink:    "INSERT INTO response_links(scope,request_id) VALUES(?,?) ON CONFLICT(scope) DO NOTHING",
	tokenUpdate: tokenUpdate,
	merge:       mergeRequest,
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(dir, "usage.db"))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, coordinator: &sync.RWMutex{}, sql: sqliteDialect}
	for _, stmt := range []string{
		"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON",
	} {
		if _, err = db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize usage database: %w", err)
		}
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version > 3 {
		db.Close()
		return nil, errors.New("unsupported usage database version")
	}
	if err = s.initialize(version); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize usage schema: %w", err)
	}
	// Independent WAL readers do not monopolize the single writer connection.
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}
	uri.RawQuery = q.Encode()
	s.reader, err = sql.Open("sqlite", uri.String())
	if err != nil {
		db.Close()
		return nil, err
	}
	s.reader.SetMaxOpenConns(4)
	s.reader.SetMaxIdleConns(4)
	if err = s.reader.Ping(); err != nil {
		s.reader.Close()
		db.Close()
		return nil, err
	}
	s.healthy.Store(true)
	return s, nil
}

func (s *Store) initialize(version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS requests (
			id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
			user_id TEXT NOT NULL, user_name TEXT NOT NULL,
			key_id TEXT NOT NULL, key_name TEXT NOT NULL,
			model TEXT NOT NULL, path TEXT NOT NULL,
			status INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0, completed INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0, reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			usage_known INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS response_links (
			scope TEXT PRIMARY KEY, request_id TEXT NOT NULL REFERENCES requests(id)
		);
		CREATE INDEX IF NOT EXISTS requests_date ON requests(started_at);
		CREATE INDEX IF NOT EXISTS requests_user_date ON requests(user_id, started_at);
		CREATE INDEX IF NOT EXISTS requests_key_date ON requests(key_id, started_at);
	`)
	if err != nil {
		return err
	}
	if version < 2 {
		// Older records have no reliable attribution; keep them in an unnamed group.
		if _, err = tx.Exec(`
			ALTER TABLE requests ADD COLUMN upstream_id TEXT NOT NULL DEFAULT '';
			ALTER TABLE requests ADD COLUMN upstream_name TEXT NOT NULL DEFAULT '';
		`); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.Exec("ALTER TABLE requests ADD COLUMN pending_sync INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`
  CREATE INDEX IF NOT EXISTS requests_upstream_date ON requests(upstream_id, started_at);
		PRAGMA user_version=3;
		UPDATE requests SET status=499, completed=1 WHERE completed=0 AND pending_sync=0;
	`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	var err error
	if s.lock != nil {
		err = s.lock.release()
	}
	return errors.Join(err, s.reader.Close(), s.db.Close())
}

// SetCoordinator is only called before the gateway starts accepting requests.
func (s *Store) SetCoordinator(gate *sync.RWMutex) { s.coordinator = gate }
func (s *Store) Healthy() bool                     { return s.healthy.Load() }

func (s *Store) exec(query string, args ...any) error {
	s.coordinator.RLock()
	defer s.coordinator.RUnlock()
	return s.execLocked(query, args...)
}

func (s *Store) execLocked(query string, args ...any) error {
	// This context outlives a disconnected client so its final usage is saved,
	// but disk/lock failures cannot stall proxy handlers indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, query, args...)
	s.healthy.Store(err == nil)
	return err
}

func (s *Store) Begin(r Request) error {
	return s.exec(`INSERT INTO requests (id,started_at,user_id,user_name,key_id,key_name,model,path)
		VALUES (?,?,?,?,?,?,?,?)`, r.ID, r.StartedAt.UnixMilli(), r.UserID, r.UserName, r.KeyID, r.KeyName, r.Model, r.Path)
}

// Attempt is committed before contacting an upstream, including when a process
// subsequently crashes before Finish. Retries replace the attribution so a
// logical request (and its reported tokens) belongs only to its last upstream.
func (s *Store) Attempt(r Request) error {
	return s.exec("UPDATE requests SET model=?, attempts=?, upstream_id=?, upstream_name=? WHERE id=?",
		r.Model, r.Attempts, r.UpstreamID, r.UpstreamName, r.ID)
}

func (s *Store) Finish(r Request) error {
	return s.exec(`UPDATE requests SET model=?,status=?,attempts=?,duration_ms=?,completed=1,pending_sync=0 WHERE id=?`,
		r.Model, r.Status, r.Attempts, r.DurationMS, r.ID)
}

func (s *Store) Bind(scope, requestID string) error {
	return s.exec(s.sql.bindLink, scope, requestID)
}

const tokenUpdate = `UPDATE requests SET
	input_tokens=MAX(input_tokens,?), output_tokens=MAX(output_tokens,?),
	cached_tokens=MAX(cached_tokens,?), reasoning_tokens=MAX(reasoning_tokens,?), usage_known=1 `

func (s *Store) Tokens(id string, c Counters) error {
	return s.exec(s.sql.tokenUpdate+"WHERE id=?", c.InputTokens, c.OutputTokens, c.CachedTokens, c.ReasoningTokens, id)
}

func (s *Store) Reconcile(scope string, c Counters) error {
	return s.exec(s.sql.tokenUpdate+"WHERE id=(SELECT request_id FROM response_links WHERE scope=?)",
		c.InputTokens, c.OutputTokens, c.CachedTokens, c.ReasoningTokens, scope)
}

// BindWith makes the usage link and Responses route one snapshot boundary.
func (s *Store) BindWith(scope, requestID string, bindRoute func() error) error {
	s.coordinator.RLock()
	defer s.coordinator.RUnlock()
	if requestID != "" {
		if err := s.execLocked(s.sql.bindLink, scope, requestID); err != nil {
			return err
		}
	}
	return bindRoute()
}
