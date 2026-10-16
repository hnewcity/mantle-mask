package usage

// Frozen v0.3.0 query implementation: a test-only correctness oracle and
// reproducible baseline. Never call this repeated-scan path in production.
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hnewcity/mantle-mask/internal/config"
)

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

func (s *Store) legacyReportWithPricing(ctx context.Context, f Filter, state config.State) (Report, error) {
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
