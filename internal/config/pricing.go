package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
)

// ModelPrice is a configurable flat-rate estimate in USD per million tokens.
// Keys are the exact model IDs (including aliases) recorded by the gateway.
// A nil cached rate bills reported cache reads at the regular input rate.
type ModelPrice struct {
	LongContextThreshold         int64    `json:"long_context_threshold,omitempty"`
	LongInputUSDPerMillion       *float64 `json:"long_input_usd_per_million,omitempty"`
	LongOutputUSDPerMillion      *float64 `json:"long_output_usd_per_million,omitempty"`
	LongCachedInputUSDPerMillion *float64 `json:"long_cached_input_usd_per_million,omitempty"`
	InputUSDPerMillion           float64  `json:"input_usd_per_million"`
	OutputUSDPerMillion          float64  `json:"output_usd_per_million"`
	CachedInputUSDPerMillion     *float64 `json:"cached_input_usd_per_million,omitempty"`
}

// Missing or null regular rates must not silently turn a model into a free
// model. Explicit zeros are valid; the cached rate alone remains optional.
func (p *ModelPrice) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"input_usd_per_million", "output_usd_per_million"} {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("input and output model prices are required; use explicit 0 for free tokens")
		}
	}
	type plain ModelPrice
	var out plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*p = ModelPrice(out)
	return nil
}

func ValidateModelPrices(prices map[string]ModelPrice) error {
	if len(prices) > 200 {
		return errors.New("at most 200 model prices are supported")
	}
	valid := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1000000
	}
	for model, price := range prices {
		if model == "" || strings.TrimSpace(model) != model || len(model) > 256 {
			return errors.New("model price IDs must be nonempty, trimmed and at most 256 bytes")
		}
		if price.LongContextThreshold < 0 || price.LongContextThreshold > 10000000 {
			return errors.New("invalid long-context threshold")
		}
		if price.LongContextThreshold > 0 && (price.LongInputUSDPerMillion == nil || price.LongOutputUSDPerMillion == nil) {
			return errors.New("long-context input and output prices are required")
		}
		if price.LongContextThreshold == 0 && (price.LongInputUSDPerMillion != nil || price.LongOutputUSDPerMillion != nil || price.LongCachedInputUSDPerMillion != nil) {
			return errors.New("long-context prices require a threshold")
		}
		badOptional := false
		for _, value := range []*float64{price.CachedInputUSDPerMillion, price.LongInputUSDPerMillion, price.LongOutputUSDPerMillion, price.LongCachedInputUSDPerMillion} {
			if value != nil && !valid(*value) {
				badOptional = true
			}
		}
		if !valid(price.InputUSDPerMillion) || !valid(price.OutputUSDPerMillion) || badOptional {
			return errors.New("model prices must be finite USD values between 0 and 1000000 per million tokens")
		}
	}
	return nil
}

func (s *Store) UpdateModelPrices(prices map[string]ModelPrice) error {
	if err := ValidateModelPrices(prices); err != nil {
		return err
	}
	copied := clonePrices(prices)
	return s.transaction(func(st *State) error { st.ModelPrices = copied; return nil })
}

// Reference prices are separate from administrator overrides. They are public
// current-rate estimates, not historical invoices or credentials.
type ReferencePrice struct {
	Model     string     `json:"model"`
	Region    string     `json:"region"`
	Source    string     `json:"source"`
	UpdatedAt time.Time  `json:"updated_at"`
	Price     ModelPrice `json:"price"`
}
type PriceSourceStatus struct {
	Source  string `json:"source"`
	Region  string `json:"region,omitempty"`
	Success bool   `json:"success"`
	Models  int    `json:"models"`
	Error   string `json:"error,omitempty"`
}
type PriceCatalog struct {
	UpdatedAt time.Time           `json:"updated_at"`
	Entries   []ReferencePrice    `json:"entries"`
	Sources   []PriceSourceStatus `json:"sources"`
	Warnings  []string            `json:"warnings"`
}

func ValidatePriceCatalog(c *PriceCatalog) error {
	if c == nil {
		return nil
	}
	if c.UpdatedAt.IsZero() || len(c.Entries) > 10000 || len(c.Sources) > 40 || len(c.Warnings) > 40 {
		return errors.New("invalid price catalog size or timestamp")
	}
	seen := map[string]bool{}
	for _, entry := range c.Entries {
		key := entry.Region + "\x00" + entry.Model
		if entry.Region == "" || len(entry.Region) > 32 || entry.UpdatedAt.IsZero() || seen[key] {
			return errors.New("invalid or duplicate reference price")
		}
		switch entry.Source {
		case "aws", "litellm", "models.dev", "aws+litellm":
		default:
			return errors.New("invalid reference price source")
		}
		if err := ValidateModelPrices(map[string]ModelPrice{entry.Model: entry.Price}); err != nil {
			return err
		}
		seen[key] = true
	}
	return nil
}
func (s *Store) UpdatePriceCatalog(c PriceCatalog) error {
	if err := ValidatePriceCatalog(&c); err != nil {
		return err
	}
	copied := cloneCatalog(&c)
	return s.transaction(func(st *State) error { st.PriceCatalog = copied; return nil })
}

// NewPriceResolver resolves the exact configured alias on the recorded upstream.
// Missing/deleted upstreams and mismatched regions never receive guessed prices.
// Manual overrides intentionally retain their existing global alias scope.
func NewPriceResolver(st State) func(string, string) (ModelPrice, bool) {
	upstreams := map[string]Upstream{}
	for _, u := range st.Upstreams {
		upstreams[u.ID] = u
	}
	references := map[string]ModelPrice{}
	if st.PriceCatalog != nil {
		for _, entry := range st.PriceCatalog.Entries {
			references[entry.Region+"\x00"+entry.Model] = entry.Price
		}
	}
	return func(model, upstreamID string) (ModelPrice, bool) {
		if p, ok := st.ModelPrices[model]; ok {
			return p, true
		}
		u, ok := upstreams[upstreamID]
		region := PricingRegion(u)
		if !ok || region == "" {
			return ModelPrice{}, false
		}
		actual := model
		if mapped, exists := u.Models[model]; exists {
			actual = mapped
		}
		p, ok := references[region+"\x00"+actual]
		return p, ok
	}
}

// The hostname is authoritative for direct Mantle endpoints; proxied endpoints
// use the explicitly configured region. No credential or endpoint request occurs.
func PricingRegion(u Upstream) string {
	parsed, err := url.Parse(u.BaseURL)
	if err == nil {
		host := parsed.Hostname()
		if strings.HasPrefix(host, "bedrock-mantle.") && strings.HasSuffix(host, ".api.aws") {
			return strings.TrimSuffix(strings.TrimPrefix(host, "bedrock-mantle."), ".api.aws")
		}
	}
	return u.Region
}
