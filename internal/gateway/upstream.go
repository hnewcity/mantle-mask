package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/hnewcity/mantle-mask/internal/config"
)

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func retryable(status int) bool {
	return status == 429 || status == 502 || status == 503 || status == 504
}

func (g *Gateway) provider(ctx context.Context, u config.Upstream) (aws.CredentialsProvider, error) {
	key := config.Hash(u.ID + u.Region + u.Profile + u.Secret)
	g.credentialMu.Lock()
	defer g.credentialMu.Unlock()
	if p, ok := g.providers[key]; ok {
		return p, nil
	}
	c, err := g.store.Credentials(u)
	if err != nil {
		return nil, err
	}
	var provider aws.CredentialsProvider
	if c.AccessKeyID != "" {
		provider = credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)
	} else {
		options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(u.Region)}
		if u.Profile != "" {
			options = append(options, awsconfig.WithSharedConfigProfile(u.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
		if err != nil {
			return nil, errors.New("could not load AWS credential chain")
		}
		provider = cfg.Credentials
	}
	if len(g.providers) > 200 {
		g.providers = map[string]aws.CredentialsProvider{}
	}
	g.providers[key] = aws.NewCredentialsCache(provider)
	return g.providers[key], nil
}

func (g *Gateway) upstreamRequest(ctx context.Context, u config.Upstream, method, path, query string, body []byte, headers http.Header, project string) (*http.Response, error) {
	target := u.BaseURL + path
	if query != "" {
		target += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid upstream request")
	}
	// Mantle's model catalog uses /v1 even when inference uses /openai/v1.
	// Normalize before signing so SigV4 covers the actual request path.
	if method == http.MethodGet && path == "/models" && req.URL.Path == "/openai/v1/models" {
		req.URL.Path = "/v1/models"
		req.URL.RawPath = ""
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Content-Type", "application/json")
	// Deliberate allowlist: client authorization, cookies, and routing headers
	// must never reach the AWS endpoint.
	for _, name := range []string{
		"Accept", "OpenAI-Beta", "X-Amzn-Bedrock-GuardrailIdentifier",
		"X-Amzn-Bedrock-GuardrailVersion", "X-Amzn-Bedrock-Trace", "Idempotency-Key",
	} {
		if value := headers.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	if project != "" {
		req.Header.Set("OpenAI-Project", project)
	}
	if u.AuthMode == "api_key" {
		c, err := g.store.Credentials(u)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	} else {
		provider, err := g.provider(ctx, u)
		if err != nil {
			return nil, err
		}
		creds, err := provider.Retrieve(ctx)
		if err != nil {
			return nil, errors.New("AWS credentials unavailable")
		}
		sum := sha256.Sum256(body)
		err = v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), u.SigningService, u.Region, time.Now())
		if err != nil {
			return nil, errors.New("AWS request signing failed")
		}
	}
	return g.client.Do(req)
}

// boundedRead is io.ReadAll with a limit. A declared length sizes the first
// buffer to avoid repeated doubling. It is capped at 1 MiB so a client cannot
// make the gateway reserve the full limit by only sending a large header.
func boundedRead(reader io.Reader, limit, sizeHint int64) ([]byte, error) {
	capacity := int64(512)
	if sizeHint > 0 {
		capacity = min(sizeHint, limit, 1<<20) + 1
	}
	data := make([]byte, 0, capacity)
	reader = io.LimitReader(reader, limit+1)
	for {
		n, err := reader.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(data) == cap(data) {
			data = append(data, 0)[:len(data)]
		}
	}
	if int64(len(data)) > limit {
		return nil, errors.New("upstream response exceeds size limit")
	}
	return data, nil
}

func (g *Gateway) discover(ctx context.Context, u config.Upstream, project string, cached bool) ([]Model, error) {
	cacheKey := config.Hash(u.ID + u.BaseURL + u.Secret + project)
	if cached {
		g.runtime.mu.Lock()
		cache, ok := g.runtime.modelCache[cacheKey]
		g.runtime.mu.Unlock()
		if ok && time.Now().Before(cache.Expires) {
			return cache.Models, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := g.upstreamRequest(ctx, u, http.MethodGet, "/models", "", nil, http.Header{}, project)
	if err != nil {
		g.runtime.mark(u, 502, time.Since(start), 0, "连接或鉴权失败")
		return nil, errors.New("upstream connection or authentication failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		g.runtime.mark(u, resp.StatusCode, time.Since(start), 0, fmt.Sprintf("模型列表返回 HTTP %d", resp.StatusCode))
		return nil, fmt.Errorf("model discovery returned HTTP %d", resp.StatusCode)
	}
	data, err := boundedRead(resp.Body, 4<<20, resp.ContentLength)
	if err != nil {
		return nil, errors.New("could not read upstream models")
	}
	var list struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Data == nil {
		return nil, errors.New("upstream did not return an OpenAI model list")
	}
	for i := range list.Data {
		list.Data[i].Object = "model"
		if list.Data[i].OwnedBy == "" {
			list.Data[i].OwnedBy = "bedrock"
		}
	}
	g.runtime.mark(u, 200, time.Since(start), 0, "")
	g.runtime.mu.Lock()
	if len(g.runtime.modelCache) > 300 {
		g.runtime.modelCache = map[string]cachedModels{}
	}
	g.runtime.modelCache[cacheKey] = cachedModels{Models: list.Data, Expires: time.Now().Add(time.Minute)}
	g.runtime.mu.Unlock()
	return list.Data, nil
}

func (g *Gateway) redactError(body []byte, u config.Upstream) []byte {
	c, err := g.store.Credentials(u)
	if err != nil {
		return nil
	}
	text := string(body)
	for _, secret := range []string{c.APIKey, c.AccessKeyID, c.SecretAccessKey, c.SessionToken} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return []byte(text)
}
