// Package pricing fetches public, credential-free Bedrock reference prices.
// No inference endpoint is contacted and no application credentials are sent.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hnewcity/mantle-mask/internal/config"
)

const maxBody = 32 << 20

var regionPattern = regexp.MustCompile(`^(us|eu|ap|sa|ca|me|af|il|mx)(-gov)?-[a-z]+-[0-9]+$`)

type Syncer struct {
	client    *http.Client
	awsURL    string
	liteURL   string
	modelsURL string
}

func New() *Syncer {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" {
			return errors.New("price source redirect rejected")
		}
		switch req.URL.Hostname() {
		case "pricing.us-east-1.amazonaws.com", "raw.githubusercontent.com", "models.dev":
			return nil
		}
		return errors.New("price source redirect rejected")
	}}
	return &Syncer{client: client, awsURL: "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonBedrock/current/%s/index.json", liteURL: "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json", modelsURL: "https://models.dev/api.json"}
}

func (s *Syncer) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "mantle-mask-price-sync/0.2")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, errors.New("price file exceeds 32 MiB")
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON price file")
	}
	return data, nil
}

type fetchResult struct {
	source, region string
	err            error
	count          int
	aws            map[string]config.ModelPrice
	catalog        map[string]json.RawMessage
}

// Sync preserves last-good entries only for regions whose every source failed.
// Successful catalogs replace their region atomically; administrator overrides
// are stored separately and are never replaced by this operation.
func (s *Syncer) Sync(ctx context.Context, old *config.PriceCatalog, inputRegions []string) (config.PriceCatalog, error) {
	regions := []string{}
	for _, region := range inputRegions {
		if !regionPattern.MatchString(region) {
			return config.PriceCatalog{}, fmt.Errorf("invalid AWS pricing region %q", region)
		}
		if !slices.Contains(regions, region) {
			regions = append(regions, region)
		}
	}
	slices.Sort(regions)
	if len(regions) == 0 {
		return config.PriceCatalog{}, errors.New("configure an upstream AWS region before synchronizing prices")
	}
	if len(regions) > 32 {
		return config.PriceCatalog{}, errors.New("at most 32 pricing regions are supported")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	jobs := []fetchResult{{source: "litellm"}, {source: "models.dev"}}
	for _, region := range regions {
		jobs = append(jobs, fetchResult{source: "aws", region: region})
	}
	results := make([]fetchResult, len(jobs))
	sem := make(chan struct{}, 4)
	var wait sync.WaitGroup
	var downloaded atomic.Int64
	for i, job := range jobs {
		wait.Add(1)
		go func(i int, job fetchResult) {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				job.err = ctx.Err()
				results[i] = job
				return
			}
			defer func() { <-sem }()
			url := s.liteURL
			if job.source == "models.dev" {
				url = s.modelsURL
			}
			if job.source == "aws" {
				url = fmt.Sprintf(s.awsURL, job.region)
			}
			data, err := s.get(ctx, url)
			job.err = err
			if err == nil && downloaded.Add(int64(len(data))) > 128<<20 {
				job.err = errors.New("total pricing download exceeds 128 MiB")
			}
			// Parse while the worker still holds the semaphore, so at most four
			// raw documents are alive instead of every downloaded region.
			if job.err == nil {
				switch job.source {
				case "aws":
					job.aws, job.err = parseAWS(data, job.region)
					job.count = len(job.aws)
				case "litellm":
					job.catalog, job.err = parseLiteCatalog(data)
					job.count = len(job.catalog)
				case "models.dev":
					job.catalog, job.err = parseModelsCatalog(data)
					job.count = len(job.catalog)
				}
			}
			results[i] = job
		}(i, job)
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return config.PriceCatalog{}, err
	}
	now := time.Now().UTC()
	out := config.PriceCatalog{UpdatedAt: now, Entries: []config.ReferencePrice{}, Sources: []config.PriceSourceStatus{}, Warnings: []string{}}
	var lite, models map[string]json.RawMessage
	aws := map[string]map[string]config.ModelPrice{}
	awsOK := map[string]bool{}
	liteOK, modelsOK := false, false
	for _, result := range results {
		if result.err == nil {
			switch result.source {
			case "aws":
				aws[result.region], awsOK[result.region] = result.aws, true
			case "litellm":
				lite, liteOK = result.catalog, true
			case "models.dev":
				models, modelsOK = result.catalog, true
			}
		}
		status := config.PriceSourceStatus{Source: result.source, Region: result.region, Success: result.err == nil, Models: result.count}
		if result.err != nil {
			status.Error = shortError(result.err)
			out.Warnings = append(out.Warnings, result.source+" "+result.region+": "+status.Error)
		}
		out.Sources = append(out.Sources, status)
	}
	awsSucceeded := false
	for _, ok := range awsOK {
		awsSucceeded = awsSucceeded || ok
	}
	if !liteOK && !modelsOK && !awsSucceeded {
		return out, errors.New("all price sources failed; previous reference prices were retained")
	}
	usedOld, conflicts := false, false
	fresh := 0
	for _, region := range regions {
		if !awsOK[region] && !liteOK && !modelsOK {
			if old != nil {
				for _, entry := range old.Entries {
					if entry.Region == region {
						out.Entries = append(out.Entries, entry)
						usedOld = true
					}
				}
			}
			continue
		}
		choices := map[string]config.ReferencePrice{}
		blocked := unsafeTierEntries(lite, models, region)
		if len(blocked) > 0 {
			conflicts = true
		}
		add := func(model, source string, price config.ModelPrice) {
			if _, exists := choices[model]; !exists && !blocked[model] {
				choices[model] = config.ReferencePrice{Model: model, Region: region, Source: source, UpdatedAt: now, Price: price}
			}
		}
		for model, price := range aws[region] {
			add(model, "aws", price)
		}
		for model, price := range litePrices(lite, region) {
			if official, exists := choices[model]; exists {
				same := closePrice(official.Price.InputUSDPerMillion, price.InputUSDPerMillion) && closePrice(official.Price.OutputUSDPerMillion, price.OutputUSDPerMillion)
				if official.Price.CachedInputUSDPerMillion != nil && price.CachedInputUSDPerMillion != nil {
					same = same && closePrice(*official.Price.CachedInputUSDPerMillion, *price.CachedInputUSDPerMillion)
				}
				if price.LongContextThreshold > 0 && !same {
					delete(choices, model)
					blocked[model] = true
					conflicts = true
					continue
				}
				if same && (price.LongContextThreshold > 0 || (official.Price.CachedInputUSDPerMillion == nil && price.CachedInputUSDPerMillion != nil)) {
					p := official.Price
					if p.CachedInputUSDPerMillion == nil {
						p.CachedInputUSDPerMillion = price.CachedInputUSDPerMillion
					}
					p.LongContextThreshold = price.LongContextThreshold
					p.LongInputUSDPerMillion = price.LongInputUSDPerMillion
					p.LongOutputUSDPerMillion = price.LongOutputUSDPerMillion
					p.LongCachedInputUSDPerMillion = price.LongCachedInputUSDPerMillion
					official.Price = p
					official.Source = "aws+litellm"
					choices[model] = official
				}
			} else {
				add(model, "litellm", price)
			}
		}
		for model, price := range modelsPrices(models, region) {
			add(model, "models.dev", price)
		}
		if len(choices) == 0 && old != nil {
			for _, entry := range old.Entries {
				if entry.Region == region && !blocked[entry.Model] {
					choices[entry.Model] = entry
					usedOld = true
				}
			}
		}
		for _, entry := range choices {
			out.Entries = append(out.Entries, entry)
			if entry.UpdatedAt.Equal(now) {
				fresh++
			}
		}
	}
	if usedOld {
		out.Warnings = append(out.Warnings, "部分区域来源不可用，仍使用该区域上次成功的缓存价格。")
	}
	if conflicts {
		out.Warnings = append(out.Warnings, "部分长上下文价格不完整或来源不一致，未采用不安全的参考价；请手动核对。")
	}
	slices.SortFunc(out.Entries, func(a, b config.ReferencePrice) int {
		if a.Region != b.Region {
			return strings.Compare(a.Region, b.Region)
		}
		return strings.Compare(a.Model, b.Model)
	})
	if len(out.Entries) == 0 || fresh == 0 {
		return out, errors.New("no complete model prices found; previous reference prices were retained")
	}
	if err := config.ValidatePriceCatalog(&out); err != nil {
		return out, err
	}
	return out, nil
}
func shortError(err error) string {
	text := err.Error()
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}
