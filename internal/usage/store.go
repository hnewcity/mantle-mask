// Package usage persists observed upstream token counts. It never estimates
// tokens from text and never stores prompts, completions, or credentials.
package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
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
	healthy     atomic.Bool
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
	s := &Store{db: db, coordinator: &sync.RWMutex{}}
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

func (s *Store) Close() error { return errors.Join(s.reader.Close(), s.db.Close()) }

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
	return s.exec("INSERT INTO response_links(scope,request_id) VALUES(?,?) ON CONFLICT(scope) DO NOTHING", scope, requestID)
}

const tokenUpdate = `UPDATE requests SET
	input_tokens=MAX(input_tokens,?), output_tokens=MAX(output_tokens,?),
	cached_tokens=MAX(cached_tokens,?), reasoning_tokens=MAX(reasoning_tokens,?), usage_known=1 `

func (s *Store) Tokens(id string, c Counters) error {
	return s.exec(tokenUpdate+"WHERE id=?", c.InputTokens, c.OutputTokens, c.CachedTokens, c.ReasoningTokens, id)
}

func (s *Store) Reconcile(scope string, c Counters) error {
	return s.exec(tokenUpdate+"WHERE id=(SELECT request_id FROM response_links WHERE scope=?)",
		c.InputTokens, c.OutputTokens, c.CachedTokens, c.ReasoningTokens, scope)
}

const aggregate = `COUNT(*), COALESCE(SUM(status>=400),0),
	COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
	COALESCE(SUM(input_tokens+output_tokens),0), COALESCE(SUM(cached_tokens),0),
	COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(usage_known),0),
	COALESCE(SUM(completed=1 AND attempts>0 AND usage_known=0),0),
	COALESCE(SUM(completed=0 AND pending_sync=0),0), COALESCE(SUM(pending_sync=1),0)`

func destinations(t *Totals) []any {
	return []any{&t.Requests, &t.Errors, &t.InputTokens, &t.OutputTokens, &t.TotalTokens,
		&t.CachedTokens, &t.ReasoningTokens, &t.KnownUsageRequests, &t.MissingUsageRequests, &t.InFlightRequests, &t.PendingSyncRequests}
}

func (s *Store) Report(ctx context.Context, f Filter) (Report, error) {
	return s.ReportWithPrices(ctx, f, nil)
}

func costDestinations(t *Totals, cost *Cost) []any {
	return append(destinations(t), &cost.EstimatedUSD, &cost.PricedRequests, &cost.UnpricedRequests)
}

const costAggregate = `, COALESCE(SUM(CASE WHEN usage_known=1 AND price_model IS NOT NULL THEN
 ((input_tokens-MIN(cached_tokens,input_tokens))*
 (CASE WHEN threshold>0 AND input_tokens>threshold THEN long_input ELSE input_usd END) +
 MIN(cached_tokens,input_tokens)*
 (CASE WHEN threshold>0 AND input_tokens>threshold THEN COALESCE(long_cached,long_input) ELSE COALESCE(cached_usd,input_usd) END) +
 output_tokens*(CASE WHEN threshold>0 AND input_tokens>threshold THEN long_output ELSE output_usd END))/1000000.0 ELSE 0 END),0),
 COALESCE(SUM(usage_known=1 AND price_model IS NOT NULL),0),
 COALESCE(SUM(usage_known=1 AND price_model IS NULL),0)`

const priceRelation = `WITH prices AS (
 SELECT json_extract(value,'$.model') AS price_model, json_extract(value,'$.upstream') AS price_upstream,
 json_extract(value,'$.price.input_usd_per_million') AS input_usd,
 json_extract(value,'$.price.output_usd_per_million') AS output_usd,
 json_extract(value,'$.price.cached_input_usd_per_million') AS cached_usd,
 json_extract(value,'$.price.long_context_threshold') AS threshold,
 json_extract(value,'$.price.long_input_usd_per_million') AS long_input,
 json_extract(value,'$.price.long_output_usd_per_million') AS long_output,
 json_extract(value,'$.price.long_cached_input_usd_per_million') AS long_cached
 FROM json_each(?)) `

func (s *Store) ReportWithPrices(ctx context.Context, f Filter, prices map[string]config.ModelPrice) (Report, error) {
	return s.ReportWithPricing(ctx, f, config.State{ModelPrices: prices})
}

func (s *Store) ReportWithPricing(ctx context.Context, f Filter, state config.State) (Report, error) {
	if err := config.ValidateModelPrices(state.ModelPrices); err != nil {
		return Report{}, err
	}
	if err := config.ValidatePriceCatalog(state.PriceCatalog); err != nil {
		return Report{}, err
	}
	prefix := priceRelation
	sums := aggregate + costAggregate
	out := Report{From: f.From, To: f.To, All: f.All, TimezoneOffset: f.TimezoneOffset,
		ByUser: []Group{}, ByKey: []Group{}, ByModel: []Group{}, ByUpstream: []Group{}, Daily: []Group{}, DailyByModel: []DailyModel{}}
	where, args := []string{"1=1"}, []any{}
	if !f.All {
		where, args = append(where, "started_at>=? AND started_at<?"), append(args, f.start, f.end)
	}
	for _, field := range []struct{ name, value string }{{"user_id", f.UserID}, {"key_id", f.KeyID}, {"model", f.Model}, {"upstream_id", f.UpstreamID}} {
		if field.value != "" {
			where, args = append(where, field.name+"=?"), append(args, field.value)
		}
	}
	baseClause := " FROM requests WHERE " + strings.Join(where, " AND ")
	clause := " FROM requests LEFT JOIN prices ON requests.model=prices.price_model AND requests.upstream_id=prices.price_upstream WHERE " + strings.Join(where, " AND ")
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Resolve only model/upstream pairs present in this authorized snapshot.
	// One JSON parameter avoids SQLite bind limits and SQL interpolation.
	pairs, err := tx.QueryContext(ctx, "SELECT DISTINCT model,upstream_id"+baseClause+" ORDER BY model,upstream_id", args...)
	if err != nil {
		return out, err
	}
	type pricedPair struct {
		Model    string            `json:"model"`
		Upstream string            `json:"upstream"`
		Price    config.ModelPrice `json:"price"`
	}
	prices := []pricedPair{}
	resolve := config.NewPriceResolver(state)
	for pairs.Next() {
		var model, upstream string
		if err = pairs.Scan(&model, &upstream); err != nil {
			pairs.Close()
			return out, err
		}
		if price, known := resolve(model, upstream); known {
			prices = append(prices, pricedPair{model, upstream, price})
		}
	}
	err = pairs.Err()
	pairs.Close()
	if err != nil {
		return out, err
	}
	data, err := json.Marshal(prices)
	if err != nil {
		return out, err
	}
	args = append([]any{string(data)}, args...)
	if err = tx.QueryRowContext(ctx, prefix+"SELECT "+sums+clause, args...).Scan(costDestinations(&out.Totals, &out.Cost)...); err != nil {
		return out, err
	}
	day := fmt.Sprintf("strftime('%%Y-%%m-%%d',started_at/1000,'unixepoch','%+d minutes')", f.TimezoneOffset)
	dimensions := []struct {
		id, name, owner string
		dest            *[]Group
	}{
		{"user_id", "MAX(user_name)", "''", &out.ByUser},
		{"key_id", "MAX(key_name)", "MAX(user_id)", &out.ByKey},
		{"model", "model", "''", &out.ByModel},
		{"upstream_id", "MAX(upstream_name)", "''", &out.ByUpstream},
		{day, day, "''", &out.Daily},
	}
	for _, d := range dimensions {
		rows, err := tx.QueryContext(ctx, prefix+"SELECT "+d.id+","+d.name+","+d.owner+","+sums+clause+" GROUP BY "+d.id+" ORDER BY "+d.id, args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var g Group
			if err = rows.Scan(append([]any{&g.ID, &g.Name, &g.UserID}, costDestinations(&g.Totals, &g.Cost)...)...); err != nil {
				rows.Close()
				return out, err
			}
			*d.dest = append(*d.dest, g)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	// Use the same transaction, timezone and authorization filters as every
	// other dimension. Missing usage remains distinct from a reported zero.
	rows, err := tx.QueryContext(ctx, prefix+"SELECT "+day+",model,"+sums+clause+" GROUP BY "+day+",model ORDER BY "+day+",model", args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var g DailyModel
		if err = rows.Scan(append([]any{&g.Date, &g.Model}, costDestinations(&g.Totals, &g.Cost)...)...); err != nil {
			rows.Close()
			return out, err
		}
		out.DailyByModel = append(out.DailyByModel, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// BindWith makes the usage link and Responses route one snapshot boundary.
func (s *Store) BindWith(scope, requestID string, bindRoute func() error) error {
	s.coordinator.RLock()
	defer s.coordinator.RUnlock()
	if requestID != "" {
		if err := s.execLocked("INSERT INTO response_links(scope,request_id) VALUES(?,?) ON CONFLICT(scope) DO NOTHING", scope, requestID); err != nil {
			return err
		}
	}
	return bindRoute()
}
