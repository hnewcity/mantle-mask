package config

import (
	"maps"
	"slices"
)

// Snapshots own every mutable slice, map and optional pointer. Typed copies
// avoid JSON encoding/validation on reads while retaining request isolation.
func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneUpstreams(upstreams []Upstream) []Upstream {
	out := slices.Clone(upstreams)
	for i := range out {
		out[i].Models = maps.Clone(out[i].Models)
	}
	return out
}

func clonePrice(p ModelPrice) ModelPrice {
	p.CachedInputUSDPerMillion = clonePointer(p.CachedInputUSDPerMillion)
	p.LongInputUSDPerMillion = clonePointer(p.LongInputUSDPerMillion)
	p.LongOutputUSDPerMillion = clonePointer(p.LongOutputUSDPerMillion)
	p.LongCachedInputUSDPerMillion = clonePointer(p.LongCachedInputUSDPerMillion)
	return p
}

func clonePrices(prices map[string]ModelPrice) map[string]ModelPrice {
	out := maps.Clone(prices)
	for model, price := range out {
		out[model] = clonePrice(price)
	}
	return out
}

func cloneCatalog(c *PriceCatalog) *PriceCatalog {
	out := clonePointer(c)
	if out == nil {
		return nil
	}
	out.Entries = slices.Clone(c.Entries)
	for i := range out.Entries {
		out.Entries[i].Price = clonePrice(out.Entries[i].Price)
	}
	out.Sources = slices.Clone(c.Sources)
	out.Warnings = slices.Clone(c.Warnings)
	return out
}

func cloneState(state State) State {
	state.Upstreams = cloneUpstreams(state.Upstreams)
	state.APIKeys = slices.Clone(state.APIKeys)
	for i := range state.APIKeys {
		state.APIKeys[i].RevokedAt = clonePointer(state.APIKeys[i].RevokedAt)
	}
	state.Users = slices.Clone(state.Users)
	state.ModelPrices = clonePrices(state.ModelPrices)
	state.PriceCatalog = cloneCatalog(state.PriceCatalog)
	return state
}

func routingState(state State) State {
	return State{Version: state.Version, Settings: state.Settings, Upstreams: cloneUpstreams(state.Upstreams)}
}

// RoutingSnapshot excludes the price catalog and unrelated users/keys from
// inference requests. It is still an owned snapshot, not a mutable store view.
func (s *Store) RoutingSnapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return routingState(s.state)
}

// AuthenticateRoutingSnapshot authorizes and captures routing in the same
// critical section, including the principal's user metadata from that version.
func (s *Store) AuthenticateRoutingSnapshot(token string) (APIKey, State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := authenticateState(s.state, token)
	if !ok {
		return APIKey{}, State{}, false
	}
	state := routingState(s.state)
	for _, user := range s.state.Users {
		if user.ID == key.UserID {
			state.Users = []User{user}
			break
		}
	}
	return key, state, true
}

// PricingSnapshot retains aliases/regions for historical price resolution but
// excludes credentials and identity lists that reporting does not need.
func (s *Store) PricingSnapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := routingState(s.state)
	for i := range state.Upstreams {
		state.Upstreams[i].Secret = ""
	}
	state.ModelPrices = clonePrices(s.state.ModelPrices)
	state.PriceCatalog = cloneCatalog(s.state.PriceCatalog)
	return state
}

func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings
}

func (s *Store) Users() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.state.Users)
}
