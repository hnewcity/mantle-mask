package usage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func (s *Store) Report(ctx context.Context, f Filter) (Report, error) {
	return s.ReportWithPrices(ctx, f, nil)
}

func (s *Store) ReportWithPrices(ctx context.Context, f Filter, prices map[string]config.ModelPrice) (Report, error) {
	return s.ReportWithPricing(ctx, f, config.State{ModelPrices: prices})
}

// ReportWithPricing streams one authorized SQLite snapshot. Each request is
// priced once, then folded into every report dimension; neither the request
// history nor repeated JSON price joins need to be materialized in memory.
// Prices are resolved anew for each report so edits and alias changes take
// effect immediately, including when re-estimating historical usage.
func (s *Store) ReportWithPricing(ctx context.Context, f Filter, state config.State) (Report, error) {
	if err := config.ValidateModelPrices(state.ModelPrices); err != nil {
		return Report{}, err
	}
	if err := config.ValidatePriceCatalog(state.PriceCatalog); err != nil {
		return Report{}, err
	}
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
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT started_at,user_id,user_name,key_id,key_name,
  model,upstream_id,upstream_name,status,attempts,completed,pending_sync,
  input_tokens,output_tokens,cached_tokens,reasoning_tokens,usage_known
  FROM requests WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	users, keys, models, upstreams, daily := groupTotals{}, groupTotals{}, groupTotals{}, groupTotals{}, groupTotals{}
	type dailyModelKey struct{ date, model string }
	dailyModels := map[dailyModelKey]*DailyModel{}
	type priceKey struct{ model, upstream string }
	type resolvedPrice struct {
		price config.ModelPrice
		known bool
	}
	prices := map[priceKey]resolvedPrice{}
	resolve := config.NewPriceResolver(state)
	dates := map[int64]string{}
	var startedAt, status, attempts, completed, pending, known int64
	var userID, userName, keyID, keyName, model, upstreamID, upstreamName string
	var counts Counters
	for rows.Next() {
		if err = rows.Scan(&startedAt, &userID, &userName, &keyID, &keyName,
			&model, &upstreamID, &upstreamName, &status, &attempts, &completed, &pending,
			&counts.InputTokens, &counts.OutputTokens, &counts.CachedTokens, &counts.ReasoningTokens, &known); err != nil {
			return out, err
		}
		// SQLite SUM previously failed on overflowing integer counters. Do not
		// turn a corrupt or oversized history into wrapped/negative totals.
		if counts.InputTokens > math.MaxInt64-counts.OutputTokens ||
			counts.InputTokens+counts.OutputTokens > math.MaxInt64-out.Totals.TotalTokens ||
			counts.CachedTokens > math.MaxInt64-out.Totals.CachedTokens ||
			counts.ReasoningTokens > math.MaxInt64-out.Totals.ReasoningTokens {
			return out, errors.New("usage token totals overflow int64")
		}
		totals := Totals{Requests: 1, InputTokens: counts.InputTokens, OutputTokens: counts.OutputTokens,
			TotalTokens: counts.InputTokens + counts.OutputTokens, CachedTokens: counts.CachedTokens,
			ReasoningTokens: counts.ReasoningTokens, KnownUsageRequests: known}
		if status >= 400 {
			totals.Errors = 1
		}
		if completed == 1 && attempts > 0 && known == 0 {
			totals.MissingUsageRequests = 1
		}
		if completed == 0 && pending == 0 {
			totals.InFlightRequests = 1
		}
		if pending == 1 {
			totals.PendingSyncRequests = 1
		}

		cost := Cost{}
		if known == 1 {
			key := priceKey{model, upstreamID}
			price, exists := prices[key]
			if !exists {
				price.price, price.known = resolve(model, upstreamID)
				prices[key] = price
			}
			if price.known {
				cost.PricedRequests = 1
				cost.EstimatedUSD = requestCost(counts, price.price)
			} else {
				cost.UnpricedRequests = 1
			}
		}
		// Match SQLite's started_at/1000 integer division before applying the
		// fixed timezone. Cache formatting per date, not per historical request.
		seconds := startedAt/1000 + int64(f.TimezoneOffset)*60
		day := seconds / 86400
		if seconds < 0 && seconds%86400 != 0 {
			day--
		}
		date, exists := dates[day]
		if !exists {
			date = time.Unix(day*86400, 0).UTC().Format(time.DateOnly)
			dates[day] = date
		}

		addUsage(&out.Totals, &out.Cost, totals, cost)
		users.add(userID, userName, "", totals, cost)
		keys.add(keyID, keyName, userID, totals, cost)
		models.add(model, model, "", totals, cost)
		upstreams.add(upstreamID, upstreamName, "", totals, cost)
		daily.add(date, date, "", totals, cost)
		key := dailyModelKey{date, model}
		dm := dailyModels[key]
		if dm == nil {
			dm = &DailyModel{Date: date, Model: model}
			dailyModels[key] = dm
		}
		addUsage(&dm.Totals, &dm.Cost, totals, cost)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}

	out.ByUser, out.ByKey, out.ByModel, out.ByUpstream, out.Daily = users.sorted(), keys.sorted(), models.sorted(), upstreams.sorted(), daily.sorted()
	for _, dm := range dailyModels {
		out.DailyByModel = append(out.DailyByModel, *dm)
	}
	slices.SortFunc(out.DailyByModel, func(a, b DailyModel) int {
		if n := strings.Compare(a.Date, b.Date); n != 0 {
			return n
		}
		return strings.Compare(a.Model, b.Model)
	})
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func requestCost(c Counters, price config.ModelPrice) float64 {
	input, output, cached := price.InputUSDPerMillion, price.OutputUSDPerMillion, price.CachedInputUSDPerMillion
	// The threshold is evaluated per request, including cache reads; never
	// apply a long-context tier to the aggregate input of several requests.
	if price.LongContextThreshold > 0 && c.InputTokens > price.LongContextThreshold {
		input, output, cached = *price.LongInputUSDPerMillion, *price.LongOutputUSDPerMillion, price.LongCachedInputUSDPerMillion
	}
	cachePrice := input
	if cached != nil {
		cachePrice = *cached
	}
	cacheTokens := min(c.CachedTokens, c.InputTokens)
	return (float64(c.InputTokens-cacheTokens)*input + float64(cacheTokens)*cachePrice + float64(c.OutputTokens)*output) / 1000000
}

func addUsage(dst *Totals, dstCost *Cost, t Totals, c Cost) {
	dst.Requests += t.Requests
	dst.Errors += t.Errors
	dst.InputTokens += t.InputTokens
	dst.OutputTokens += t.OutputTokens
	dst.TotalTokens += t.TotalTokens
	dst.CachedTokens += t.CachedTokens
	dst.ReasoningTokens += t.ReasoningTokens
	dst.KnownUsageRequests += t.KnownUsageRequests
	dst.MissingUsageRequests += t.MissingUsageRequests
	dst.InFlightRequests += t.InFlightRequests
	dst.PendingSyncRequests += t.PendingSyncRequests
	dstCost.EstimatedUSD += c.EstimatedUSD
	dstCost.PricedRequests += c.PricedRequests
	dstCost.UnpricedRequests += c.UnpricedRequests
}

type groupTotals map[string]*Group

func (groups groupTotals) add(id, name, owner string, totals Totals, cost Cost) {
	group := groups[id]
	if group == nil {
		group = &Group{ID: id, Name: name, UserID: owner}
		groups[id] = group
	} else {
		// Preserve the previous SQL MAX(name)/MAX(user_id) rules for renamed
		// and deleted entities; live display names are overlaid by the server.
		if name > group.Name {
			group.Name = name
		}
		if owner > group.UserID {
			group.UserID = owner
		}
	}
	addUsage(&group.Totals, &group.Cost, totals, cost)
}

func (groups groupTotals) sorted() []Group {
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	slices.SortFunc(out, func(a, b Group) int { return strings.Compare(a.ID, b.ID) })
	return out
}
