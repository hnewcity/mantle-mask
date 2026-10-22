package config

import (
	"errors"
	"slices"
	"strings"
)

var (
	ErrUpstreamNotFound = errors.New("upstream not found")
	ErrUpstreamAuthMode = errors.New("API key rotation requires an api_key upstream")
	ErrInvalidAPIKey    = errors.New("api_key must be a non-empty ASCII token without whitespace or control characters")
)

// UpdateUpstreamAPIKey rotates only the secret within the configuration transaction.
// Readers already holding a snapshot retain their original credentials.
func (s *Store) UpdateUpstreamAPIKey(id, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ErrInvalidAPIKey
	}
	for _, ch := range apiKey {
		if ch < '!' || ch > '~' {
			return ErrInvalidAPIKey
		}
	}
	return s.transaction(func(st *State) error {
		idx := slices.IndexFunc(st.Upstreams, func(u Upstream) bool { return u.ID == id })
		if idx < 0 {
			return ErrUpstreamNotFound
		}
		u := &st.Upstreams[idx]
		if u.AuthMode != "api_key" {
			return ErrUpstreamAuthMode
		}
		secret, err := s.seal(u.ID, Credentials{APIKey: apiKey})
		if err != nil {
			return err
		}
		u.Secret = secret
		return nil
	})
}
