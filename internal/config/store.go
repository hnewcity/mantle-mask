package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Credentials struct {
	APIKey          string `json:"api_key,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

type Upstream struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	BaseURL        string            `json:"base_url"`
	Region         string            `json:"region"`
	AuthMode       string            `json:"auth_mode"`
	SigningService string            `json:"signing_service"`
	Profile        string            `json:"profile,omitempty"`
	Project        string            `json:"project,omitempty"`
	Weight         int               `json:"weight"`
	Enabled        bool              `json:"enabled"`
	Models         map[string]string `json:"models"`
	Secret         string            `json:"encrypted_credentials,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

type UpstreamInput struct {
	Upstream
	Credentials
	ClearCredentials bool `json:"clear_credentials,omitempty"`
}

type UpstreamView struct {
	Upstream
	HasCredentials bool `json:"has_credentials"`
}

type APIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Hash      string     `json:"hash,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UserID    string     `json:"user_id"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

const DefaultUserID = "usr_default"

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type UserInput struct {
	Name    string `json:"name"`
	Note    string `json:"note"`
	Enabled bool   `json:"enabled"`
}

type Settings struct {
	RequestTimeoutSeconds int  `json:"request_timeout_seconds"`
	MaxRetries            int  `json:"max_retries"`
	CooldownSeconds       int  `json:"cooldown_seconds"`
	LogCapacity           int  `json:"log_capacity"`
	AllowHTTPUpstreams    bool `json:"allow_http_upstreams"`
}

type State struct {
	Version   int        `json:"version"`
	Upstreams []Upstream `json:"upstreams"`
	APIKeys   []APIKey   `json:"api_keys"`
	Users     []User     `json:"users"`
	Settings  Settings   `json:"settings"`
}

type Store struct {
	mu    sync.RWMutex
	path  string
	aead  cipher.AEAD
	state State
}

func Open(dir, masterKey string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var key []byte
	var err error
	if masterKey != "" {
		key, err = base64.StdEncoding.DecodeString(masterKey)
	} else {
		key, err = os.ReadFile(filepath.Join(dir, "master.key"))
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(filepath.Join(dir, "config.json")); statErr == nil {
				return nil, errors.New("master.key is missing; restore it before opening existing data")
			}
			key = make([]byte, 32)
			if _, err = rand.Read(key); err == nil {
				err = os.WriteFile(filepath.Join(dir, "master.key"), key, 0600)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("master key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Store{
		path: filepath.Join(dir, "config.json"),
		aead: aead,
		state: State{
			Version: 2, Upstreams: []Upstream{}, APIKeys: []APIKey{},
			Users:    []User{{ID: DefaultUserID, Name: "默认用户", Enabled: true, CreatedAt: time.Now().UTC()}},
			Settings: Settings{RequestTimeoutSeconds: 300, MaxRetries: 2, CooldownSeconds: 30, LogCapacity: 1000},
		},
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		err = AtomicJSON(s.path, s.state)
	} else if err == nil {
		err = json.Unmarshal(b, &s.state)
		if err == nil && s.state.Version != 1 && s.state.Version != 2 {
			err = errors.New("unsupported configuration version")
		}
		if err == nil {
			err = ValidateSettings(s.state.Settings)
		}
		for _, u := range s.state.Upstreams {
			if err != nil {
				break
			}
			_, err = s.Credentials(u)
		}
		if err == nil && s.state.Version == 1 {
			s.state.Version = 2
			s.state.Users = []User{{ID: DefaultUserID, Name: "默认用户", Note: "升级前的 API Key 自动归属此用户", Enabled: true, CreatedAt: time.Now().UTC()}}
			for i := range s.state.APIKeys {
				s.state.APIKeys[i].UserID = DefaultUserID
			}
			err = AtomicJSON(s.path, s.state)
		}
	}
	return s, err
}

// AtomicJSON syncs a private temporary file and renames it over the destination.
func AtomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mantle-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func Token(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func AdminToken(dir, env string) (string, error) {
	if env != "" {
		if len(env) < 24 {
			return "", errors.New("MANTLE_ADMIN_TOKEN must be at least 24 characters")
		}
		return env, nil
	}
	path := filepath.Join(dir, "admin-token")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		token := Token("mm-admin-")
		return token, os.WriteFile(path, []byte(token+"\n"), 0600)
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 24 {
		return "", errors.New("admin-token is invalid")
	}
	return token, nil
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.state)
	var clone State
	_ = json.Unmarshal(b, &clone)
	return clone
}

func (s *Store) transaction(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.state)
	var next State
	_ = json.Unmarshal(b, &next)
	if err := fn(&next); err != nil {
		return err
	}
	if err := AtomicJSON(s.path, next); err != nil {
		return errors.New("could not persist configuration")
	}
	s.state = next
	return nil
}

func (s *Store) Views() []UpstreamView {
	out := []UpstreamView{}
	for _, u := range s.Snapshot().Upstreams {
		has := u.Secret != ""
		u.Secret = ""
		out = append(out, UpstreamView{Upstream: u, HasCredentials: has})
	}
	return out
}

func (s *Store) Credentials(u Upstream) (Credentials, error) {
	var c Credentials
	if u.Secret == "" {
		return c, nil
	}
	b, err := base64.StdEncoding.DecodeString(u.Secret)
	if err != nil || len(b) < s.aead.NonceSize() {
		return c, errors.New("invalid encrypted credentials")
	}
	plain, err := s.aead.Open(nil, b[:s.aead.NonceSize()], b[s.aead.NonceSize():], []byte(u.ID))
	if err != nil {
		return c, errors.New("cannot decrypt credentials; check master key")
	}
	return c, json.Unmarshal(plain, &c)
}

func (s *Store) seal(id string, c Credentials) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, b, []byte(id))), nil
}

func ValidateSettings(v Settings) error {
	if v.RequestTimeoutSeconds < 5 || v.RequestTimeoutSeconds > 3600 {
		return errors.New("request timeout must be between 5 and 3600 seconds")
	}
	if v.MaxRetries < 0 || v.MaxRetries > 5 {
		return errors.New("max retries must be between 0 and 5")
	}
	if v.CooldownSeconds < 1 || v.CooldownSeconds > 600 {
		return errors.New("cooldown must be between 1 and 600 seconds")
	}
	if v.LogCapacity < 100 || v.LogCapacity > 10000 {
		return errors.New("log capacity must be between 100 and 10000")
	}
	return nil
}

func validateUpstream(u *Upstream, allowHTTP bool) error {
	u.Name = strings.TrimSpace(u.Name)
	if u.Name == "" || len(u.Name) > 100 {
		return errors.New("name is required and must be at most 100 characters")
	}
	raw, err := url.Parse(strings.TrimRight(strings.TrimSpace(u.BaseURL), "/"))
	if err != nil || raw.Hostname() == "" || raw.User != nil || raw.RawQuery != "" || raw.Fragment != "" {
		return errors.New("base URL must be an absolute URL without credentials, query, or fragment")
	}
	if raw.Scheme != "https" {
		ip := net.ParseIP(raw.Hostname())
		loopback := raw.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
		if raw.Scheme != "http" || (!loopback && !allowHTTP) {
			return errors.New("use HTTPS; HTTP is allowed only for loopback or when explicitly enabled")
		}
	}
	if raw.Path == "" {
		raw.Path = "/v1"
	}
	if raw.Path != "/v1" && raw.Path != "/openai/v1" {
		return errors.New("base URL path must be /v1 or /openai/v1")
	}
	u.BaseURL = raw.String()
	if u.Weight < 1 || u.Weight > 100 {
		return errors.New("weight must be between 1 and 100")
	}
	if u.AuthMode != "api_key" && u.AuthMode != "sigv4" {
		return errors.New("auth mode must be api_key or sigv4")
	}
	if u.SigningService == "" {
		u.SigningService = "bedrock-mantle"
	}
	if u.SigningService != "bedrock-mantle" && u.SigningService != "bedrock" {
		return errors.New("signing service must be bedrock-mantle or bedrock")
	}
	if u.AuthMode == "sigv4" && strings.TrimSpace(u.Region) == "" {
		return errors.New("region is required for SigV4")
	}
	if strings.ContainsAny(u.Project+u.Region+u.Profile, "\r\n") {
		return errors.New("invalid newline in configuration")
	}
	if u.Models == nil {
		u.Models = map[string]string{}
	}
	for alias, model := range u.Models {
		if strings.TrimSpace(alias) == "" || strings.TrimSpace(model) == "" {
			return errors.New("model aliases and IDs cannot be empty")
		}
	}
	return nil
}

func (s *Store) SaveUpstream(id string, input UpstreamInput) (string, error) {
	err := s.transaction(func(st *State) error {
		u := input.Upstream
		if err := validateUpstream(&u, st.Settings.AllowHTTPUpstreams); err != nil {
			return err
		}
		idx := -1
		u.Secret = ""
		c := Credentials{}
		if id != "" {
			idx = slices.IndexFunc(st.Upstreams, func(v Upstream) bool { return v.ID == id })
			if idx < 0 {
				return errors.New("upstream not found")
			}
			u.CreatedAt = st.Upstreams[idx].CreatedAt
			old, err := s.Credentials(st.Upstreams[idx])
			if err != nil {
				return err
			}
			c = old
			if st.Upstreams[idx].AuthMode != u.AuthMode {
				c = Credentials{}
			}
		} else {
			if len(st.Upstreams) >= 100 {
				return errors.New("at most 100 upstreams are supported")
			}
			id = Token("up_")[:19]
			u.CreatedAt = time.Now().UTC()
		}
		u.ID = id
		if input.ClearCredentials {
			c = Credentials{}
		}
		if input.APIKey != "" {
			c.APIKey = strings.TrimSpace(input.APIKey)
		}
		if input.AccessKeyID != "" || input.SecretAccessKey != "" {
			if input.AccessKeyID == "" || input.SecretAccessKey == "" {
				return errors.New("access key ID and secret access key must be provided together")
			}
			c.AccessKeyID, c.SecretAccessKey, c.SessionToken = input.AccessKeyID, input.SecretAccessKey, input.SessionToken
		}
		if u.AuthMode == "api_key" {
			if c.APIKey == "" || strings.ContainsAny(c.APIKey, "\r\n") {
				return errors.New("a valid Bedrock API key is required")
			}
			c = Credentials{APIKey: c.APIKey}
		} else {
			c.APIKey = ""
		}
		if c != (Credentials{}) {
			secret, err := s.seal(id, c)
			if err != nil {
				return err
			}
			u.Secret = secret
		}
		if idx >= 0 {
			st.Upstreams[idx] = u
		} else {
			st.Upstreams = append(st.Upstreams, u)
		}
		return nil
	})
	return id, err
}

func (s *Store) DeleteUpstream(id string) error {
	return s.transaction(func(st *State) error {
		idx := slices.IndexFunc(st.Upstreams, func(v Upstream) bool { return v.ID == id })
		if idx < 0 {
			return errors.New("upstream not found")
		}
		st.Upstreams = slices.Delete(st.Upstreams, idx, idx+1)
		return nil
	})
}

func (s *Store) UpdateSettings(v Settings) error {
	if err := ValidateSettings(v); err != nil {
		return err
	}
	return s.transaction(func(st *State) error {
		if !v.AllowHTTPUpstreams {
			for _, u := range st.Upstreams {
				if err := validateUpstream(&u, false); err != nil {
					return errors.New("remove non-loopback HTTP upstreams before disabling HTTP")
				}
			}
		}
		st.Settings = v
		return nil
	})
}

func (s *Store) SaveUser(id string, input UserInput) (User, error) {
	user := User{ID: id, Name: strings.TrimSpace(input.Name), Note: strings.TrimSpace(input.Note), Enabled: input.Enabled}
	if user.Name == "" || len([]rune(user.Name)) > 100 || len([]rune(user.Note)) > 500 {
		return User{}, errors.New("user name is required (up to 100 characters); note must be at most 500 characters")
	}
	err := s.transaction(func(st *State) error {
		if id == "" {
			if len(st.Users) >= 1000 {
				return errors.New("at most 1000 users are supported")
			}
			user.ID, user.CreatedAt = Token("usr_")[:20], time.Now().UTC()
			st.Users = append(st.Users, user)
			return nil
		}
		i := slices.IndexFunc(st.Users, func(u User) bool { return u.ID == id })
		if i < 0 {
			return errors.New("user not found")
		}
		user.CreatedAt = st.Users[i].CreatedAt
		st.Users[i] = user
		return nil
	})
	return user, err
}

func (s *Store) User(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.state.Users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

func (s *Store) Keys(userID string, includeRevoked bool) []APIKey {
	keys := []APIKey{}
	for _, k := range s.Snapshot().APIKeys {
		if (userID == "" || k.UserID == userID) && (includeRevoked || k.RevokedAt == nil) {
			k.Hash = ""
			keys = append(keys, k)
		}
	}
	return keys
}

func (s *Store) CreateKey(name string, owners ...string) (APIKey, string, error) {
	owner := DefaultUserID
	if len(owners) > 0 && owners[0] != "" {
		owner = owners[0]
	}
	token := Token("sk-mm-")
	key := APIKey{ID: Token("key_")[:20], Name: strings.TrimSpace(name), UserID: owner, Prefix: token[:14], Hash: Hash(token), CreatedAt: time.Now().UTC()}
	if key.Name == "" || len(key.Name) > 100 {
		return APIKey{}, "", errors.New("key name is required and must be at most 100 characters")
	}
	err := s.transaction(func(st *State) error {
		if !slices.ContainsFunc(st.Users, func(u User) bool { return u.ID == owner && u.Enabled }) {
			return errors.New("an enabled user is required")
		}
		if len(st.APIKeys) >= 10000 {
			return errors.New("at most 10000 API keys including revoked keys are supported")
		}
		st.APIKeys = append(st.APIKeys, key)
		return nil
	})
	key.Hash = ""
	return key, token, err
}

func (s *Store) DeleteKey(id string) error {
	return s.transaction(func(st *State) error {
		idx := slices.IndexFunc(st.APIKeys, func(v APIKey) bool { return v.ID == id })
		if idx < 0 {
			return errors.New("key not found")
		}
		if st.APIKeys[idx].RevokedAt == nil {
			now := time.Now().UTC()
			st.APIKeys[idx].RevokedAt, st.APIKeys[idx].Hash = &now, ""
		}
		return nil
	})
}

func (s *Store) Authenticate(token string) (APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hash := Hash(token)
	for _, key := range s.state.APIKeys {
		if key.RevokedAt == nil && subtle.ConstantTimeCompare([]byte(hash), []byte(key.Hash)) == 1 &&
			slices.ContainsFunc(s.state.Users, func(u User) bool { return u.ID == key.UserID && u.Enabled }) {
			key.Hash = ""
			return key, true
		}
	}
	return APIKey{}, false
}
