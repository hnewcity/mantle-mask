package pricing

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/hnewcity/mantle-mask/internal/config"
)

func number(fields map[string]json.RawMessage, key string, multiplier float64) (*float64, bool) {
	data, ok := fields[key]
	if !ok || string(data) == "null" {
		return nil, false
	}
	var value float64
	if json.Unmarshal(data, &value) != nil {
		return nil, false
	}
	value *= multiplier
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1000000 {
		return nil, false
	}
	return &value, true
}
func closePrice(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

var liteTierPattern = regexp.MustCompile(`^input_cost_per_token_above_([0-9]+)k_tokens$`)
var modelTierPattern = regexp.MustCompile(`^context_over_([0-9]+)k$`)

func parsedPrice(data json.RawMessage, lite bool) (config.ModelPrice, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return config.ModelPrice{}, false
	}
	input, output, cached, multiplier := "input", "output", "cache_read", 1.0
	if lite {
		input = "input_cost_per_token"
		output = "output_cost_per_token"
		cached = "cache_read_input_token_cost"
		multiplier = 1000000
	}
	in, ok := number(fields, input, multiplier)
	if !ok {
		return config.ModelPrice{}, false
	}
	out, ok := number(fields, output, multiplier)
	if !ok {
		return config.ModelPrice{}, false
	}
	price := config.ModelPrice{InputUSDPerMillion: *in, OutputUSDPerMillion: *out}
	if value, exists := fields[cached]; exists && string(value) != "null" {
		if c, ok := number(fields, cached, multiplier); ok {
			price.CachedInputUSDPerMillion = c
		} else {
			return config.ModelPrice{}, false
		}
	}
	tierCount := 0
	for key, value := range fields {
		match := modelTierPattern.FindStringSubmatch(key)
		if lite {
			match = liteTierPattern.FindStringSubmatch(key)
		}
		if len(match) == 0 {
			continue
		}
		tierCount++
		if tierCount > 1 {
			return config.ModelPrice{}, false
		}
		n, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || n <= 0 || n > 10000 {
			return config.ModelPrice{}, false
		}
		price.LongContextThreshold = n * 1000
		if lite {
			suffix := "_above_" + match[1] + "k_tokens"
			price.LongInputUSDPerMillion, ok = number(fields, "input_cost_per_token"+suffix, multiplier)
			if !ok {
				return config.ModelPrice{}, false
			}
			price.LongOutputUSDPerMillion, ok = number(fields, "output_cost_per_token"+suffix, multiplier)
			if !ok {
				return config.ModelPrice{}, false
			}
			if _, exists := fields["cache_read_input_token_cost"+suffix]; exists {
				price.LongCachedInputUSDPerMillion, ok = number(fields, "cache_read_input_token_cost"+suffix, multiplier)
				if !ok {
					return config.ModelPrice{}, false
				}
			}
		} else {
			var tier map[string]json.RawMessage
			if json.Unmarshal(value, &tier) != nil {
				return config.ModelPrice{}, false
			}
			price.LongInputUSDPerMillion, ok = number(tier, input, multiplier)
			if !ok {
				return config.ModelPrice{}, false
			}
			price.LongOutputUSDPerMillion, ok = number(tier, output, multiplier)
			if !ok {
				return config.ModelPrice{}, false
			}
			if _, exists := tier[cached]; exists {
				price.LongCachedInputUSDPerMillion, ok = number(tier, cached, multiplier)
				if !ok {
					return config.ModelPrice{}, false
				}
			}
		}
	}
	// Do not flatten unknown or mismatched higher-context dimensions.
	for key := range fields {
		if lite && contextRateKey(key) {
			suffix := "_above_" + strconv.FormatInt(price.LongContextThreshold/1000, 10) + "k_tokens"
			if tierCount == 0 || !strings.HasSuffix(key, suffix) {
				return config.ModelPrice{}, false
			}
		}
		if !lite && strings.HasPrefix(key, "context_over_") && len(modelTierPattern.FindStringSubmatch(key)) == 0 {
			return config.ModelPrice{}, false
		}
	}
	if err := config.ValidateModelPrices(map[string]config.ModelPrice{"model": price}); err != nil {
		return config.ModelPrice{}, false
	}
	return price, true
}
func parseLiteCatalog(data []byte) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	for id, entry := range all {
		if strings.HasPrefix(id, "bedrock_mantle/") {
			out[id] = entry
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no Bedrock Mantle entries in LiteLLM catalog")
	}
	return out, nil
}
func litePrices(data map[string]json.RawMessage, region string) map[string]config.ModelPrice {
	out := map[string]config.ModelPrice{}
	for key, entry := range data {
		id := strings.TrimPrefix(key, "bedrock_mantle/")
		if strings.Contains(id, "/") {
			continue
		}
		if strings.HasPrefix(region, "us-gov-") {
			continue
		}
		if price, ok := parsedPrice(entry, true); ok {
			out[id] = price
		}
	}
	for key, entry := range data {
		prefix := "bedrock_mantle/" + region + "/"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		id := strings.TrimPrefix(key, prefix)
		if price, ok := parsedPrice(entry, true); ok {
			out[id] = price
		} else {
			delete(out, id)
		}
	}
	return out
}
func parseModelsCatalog(data []byte) (map[string]json.RawMessage, error) {
	var all map[string]struct {
		Models map[string]struct {
			Cost json.RawMessage `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	provider, ok := all["amazon-bedrock"]
	if !ok || len(provider.Models) == 0 {
		return nil, errors.New("no Amazon Bedrock models in models.dev catalog")
	}
	out := map[string]json.RawMessage{}
	for id, model := range provider.Models {
		out[id] = model.Cost
	}
	return out, nil
}
func modelsPrices(data map[string]json.RawMessage, region string) map[string]config.ModelPrice {
	out := map[string]config.ModelPrice{}
	for id, entry := range data {
		gov := strings.HasPrefix(id, "us-gov.")
		if gov != strings.HasPrefix(region, "us-gov-") {
			continue
		}
		if gov {
			id = strings.TrimPrefix(id, "us-gov.")
		}
		if strings.HasPrefix(id, "us.") || strings.HasPrefix(id, "eu.") || strings.HasPrefix(id, "apac.") || strings.HasPrefix(id, "global.") {
			continue
		}
		if price, ok := parsedPrice(entry, false); ok {
			out[id] = price
		}
	}
	return out
}

type awsDocument struct {
	Products map[string]struct {
		Attributes map[string]string `json:"attributes"`
	} `json:"products"`
	Terms struct {
		OnDemand map[string]map[string]struct {
			PriceDimensions map[string]struct {
				Unit         string            `json:"unit"`
				BeginRange   string            `json:"beginRange"`
				EndRange     string            `json:"endRange"`
				PricePerUnit map[string]string `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"OnDemand"`
	} `json:"terms"`
}

func parseAWS(data []byte, region string) (map[string]config.ModelPrice, error) {
	var doc awsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Products == nil || doc.Terms.OnDemand == nil {
		return nil, errors.New("incomplete AWS price list")
	}
	type rates struct {
		input, output, cache *float64
		unsafe               bool
	}
	models := map[string]*rates{}
	for sku, product := range doc.Products {
		a := product.Attributes
		if a["regionCode"] != region || (a["service_tier"] != "" && a["service_tier"] != "standard") {
			continue
		}
		parts := strings.SplitN(a["usagetype"], "-mantle-", 2)
		if len(parts) != 2 || !strings.HasSuffix(parts[1], "-standard") {
			continue
		}
		left := parts[0]
		i := strings.Index(left, "-")
		if i < 0 {
			continue
		}
		model := left[i+1:]
		if model == "" {
			continue
		}
		r := models[model]
		if r == nil {
			r = &rates{}
			models[model] = r
		}
		direction := parts[1]
		var target **float64
		switch direction {
		case "input-tokens-standard":
			target = &r.input
		case "output-tokens-standard":
			target = &r.output
		case "cache-read-input-tokens-standard", "cache-read-tokens-standard", "cached-input-tokens-standard":
			target = &r.cache
		default:
			if strings.Contains(direction, "input-tokens") || strings.Contains(direction, "output-tokens") {
				r.unsafe = true
			}
			continue
		}
		seen := false
		for _, term := range doc.Terms.OnDemand[sku] {
			for _, dimension := range term.PriceDimensions {
				if dimension.BeginRange != "0" || dimension.EndRange != "Inf" {
					r.unsafe = true
					continue
				}
				multiplier := 0.0
				switch strings.ToLower(strings.TrimSpace(dimension.Unit)) {
				case "1k tokens":
					multiplier = 1000
				case "1m tokens":
					multiplier = 1
				case "tokens", "token", "1 token":
					multiplier = 1000000
				}
				value, err := strconv.ParseFloat(dimension.PricePerUnit["USD"], 64)
				value *= multiplier
				if err != nil || multiplier == 0 || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > 1000000 {
					r.unsafe = true
					continue
				}
				if *target != nil && !closePrice(**target, value) {
					r.unsafe = true
				}
				copy := value
				*target = &copy
				seen = true
			}
		}
		if !seen {
			r.unsafe = true
		}
	}
	out := map[string]config.ModelPrice{}
	for model, r := range models {
		if !r.unsafe && r.input != nil && r.output != nil {
			out[model] = config.ModelPrice{InputUSDPerMillion: *r.input, OutputUSDPerMillion: *r.output, CachedInputUSDPerMillion: r.cache}
		}
	}
	return out, nil
}

func contextRateKey(key string) bool {
	return strings.HasPrefix(key, "input_cost_per_token_above_") || strings.HasPrefix(key, "output_cost_per_token_above_") || strings.HasPrefix(key, "cache_read_input_token_cost_above_")
}
func unsafeTierEntries(lite, models map[string]json.RawMessage, region string) map[string]bool {
	blocked := map[string]bool{}
	check := func(id string, entry json.RawMessage, isLite bool) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil {
			return
		}
		hasTier := false
		for key := range fields {
			if (isLite && contextRateKey(key)) || (!isLite && strings.HasPrefix(key, "context_over_")) {
				hasTier = true
			}
		}
		if hasTier {
			if _, ok := parsedPrice(entry, isLite); !ok {
				blocked[id] = true
			}
		}
	}
	selected := map[string]json.RawMessage{}
	if !strings.HasPrefix(region, "us-gov-") {
		for key, entry := range lite {
			id := strings.TrimPrefix(key, "bedrock_mantle/")
			if !strings.Contains(id, "/") {
				selected[id] = entry
			}
		}
	}
	prefix := "bedrock_mantle/" + region + "/"
	for key, entry := range lite {
		if strings.HasPrefix(key, prefix) {
			selected[strings.TrimPrefix(key, prefix)] = entry
		}
	}
	for id, entry := range selected {
		check(id, entry, true)
	}
	for id, entry := range models {
		gov := strings.HasPrefix(id, "us-gov.")
		if gov != strings.HasPrefix(region, "us-gov-") {
			continue
		}
		if gov {
			id = strings.TrimPrefix(id, "us-gov.")
		}
		check(id, entry, false)
	}
	return blocked
}
