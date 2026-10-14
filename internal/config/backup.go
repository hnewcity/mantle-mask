package config

import (
	"encoding/hex"
	"errors"
	"strings"
)

// PortableState is only serialized inside a password-encrypted backup. API keys
// retain their original hashes; upstream credentials are re-sealed on import.
type PortableState struct {
	State       State                  `json:"state"`
	Credentials map[string]Credentials `json:"credentials"`
}

func (s *Store) Export() (PortableState, error) {
	out := PortableState{State: s.Snapshot(), Credentials: map[string]Credentials{}}
	for i, u := range out.State.Upstreams {
		c, err := s.Credentials(u)
		if err != nil {
			return PortableState{}, err
		}
		out.Credentials[u.ID] = c
		out.State.Upstreams[i].Secret = ""
	}
	return out, nil
}

func (s *Store) PrepareImport(in PortableState) (State, error) {
	st := in.State
	if st.Version != 2 || st.Upstreams == nil || st.Users == nil || st.APIKeys == nil || in.Credentials == nil {
		return State{}, errors.New("unsupported or incomplete backup configuration")
	}
	if err := ValidateSettings(st.Settings); err != nil {
		return State{}, err
	}
	if len(st.Upstreams) > 100 || len(st.Users) > 1000 || len(st.APIKeys) > 10000 || len(in.Credentials) != len(st.Upstreams) {
		return State{}, errors.New("invalid backup configuration size")
	}
	users, keys, hashes, upstreams := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range st.Users {
		if !backupID(u.ID) || users[u.ID] || strings.TrimSpace(u.Name) == "" || len([]rune(u.Name)) > 100 || len([]rune(u.Note)) > 500 || u.CreatedAt.IsZero() {
			return State{}, errors.New("invalid or duplicate backup user")
		}
		users[u.ID] = true
	}
	if !users[DefaultUserID] {
		return State{}, errors.New("backup default user is missing")
	}
	for _, k := range st.APIKeys {
		hash, err := hex.DecodeString(k.Hash)
		validHash := err == nil && len(hash) == 32 && k.Hash == strings.ToLower(k.Hash)
		if !backupID(k.ID) || strings.TrimSpace(k.Name) == "" || len(k.Name) > 100 || k.Prefix == "" || keys[k.ID] || !users[k.UserID] || k.CreatedAt.IsZero() || (k.RevokedAt == nil && (!validHash || hashes[k.Hash])) || (k.RevokedAt != nil && k.Hash != "" && !validHash) {
			return State{}, errors.New("invalid or duplicate backup API key")
		}
		keys[k.ID], hashes[k.Hash] = true, true
	}
	// Copy the slice before replacing secrets so the caller's portable backup is unchanged.
	st.Upstreams = append([]Upstream{}, st.Upstreams...)
	for i, u := range st.Upstreams {
		c, ok := in.Credentials[u.ID]
		if !backupID(u.ID) || upstreams[u.ID] || !ok || u.Secret != "" || u.CreatedAt.IsZero() {
			return State{}, errors.New("invalid or duplicate backup upstream")
		}
		if err := validateUpstream(&u, st.Settings.AllowHTTPUpstreams); err != nil {
			return State{}, err
		}
		if strings.ContainsAny(c.APIKey+c.AccessKeyID+c.SecretAccessKey+c.SessionToken, "\r\n") ||
			(u.AuthMode == "api_key" && c.APIKey == "") || ((c.AccessKeyID == "") != (c.SecretAccessKey == "")) {
			return State{}, errors.New("invalid backup upstream credentials")
		}
		if c != (Credentials{}) {
			secret, err := s.seal(u.ID, c)
			if err != nil {
				return State{}, err
			}
			u.Secret = secret
		}
		st.Upstreams[i], upstreams[u.ID] = u, true
	}
	return st, nil
}

func backupID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Restore replaces a previously validated state encrypted with this store's key.
// The gateway journals this operation together with usage and affinity changes.
func (s *Store) Restore(st State) error {
	return s.transaction(func(next *State) error { *next = st; return nil })
}
