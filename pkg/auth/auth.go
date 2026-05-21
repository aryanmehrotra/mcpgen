// Package auth provides pluggable authentication for upstream API calls.
//
// Providers add headers or query parameters to outgoing requests. They are
// constructed from a typed Config (no map[string]any bag) so configuration
// errors surface at YAML parse time, not at first call.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultRefreshSkew         = 60 * time.Second
	defaultAccessTokenLifetime = 15 * time.Minute
)

// Provider adds authentication to an outgoing request by mutating headers
// and/or query parameters.
type Provider interface {
	Apply(ctx context.Context, h http.Header, q url.Values) error
}

// Refresher is an optional capability for providers that hold renewable
// credentials (e.g. refresh-token flows). Cron jobs call Refresh() to keep
// tokens warm outside the request path.
type Refresher interface {
	Refresh(ctx context.Context) error
}

// Config is a tagged union: Provider names which sub-config is used.
type Config struct {
	Provider     string              `yaml:"provider"`
	BearerStatic *BearerStaticConfig `yaml:"bearer_static,omitempty"`
	APIKey       *APIKeyConfig       `yaml:"api_key,omitempty"`
	RefreshToken *RefreshTokenConfig `yaml:"refresh_token,omitempty"`
}

type BearerStaticConfig struct {
	Token    string `yaml:"token"`
	TokenEnv string `yaml:"token_env"`
}

type APIKeyConfig struct {
	In       string `yaml:"in"` // header | query | cookie
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	ValueEnv string `yaml:"value_env"`
}

type RefreshTokenConfig struct {
	RefreshEndpoint   string `yaml:"refresh_endpoint"`
	RefreshIn         string `yaml:"refresh_in"` // header (default) | body
	RefreshTokenField string `yaml:"refresh_token_field"`
	AccessTokenField  string `yaml:"access_token_field"`
	ExpiresInField    string `yaml:"expires_in_field"`
	CachePath         string `yaml:"cache_path"`
	BootstrapEnv      string `yaml:"bootstrap_env"`
	SkewSeconds       int    `yaml:"skew_seconds"`
}

// New constructs a Provider from Config. Errors immediately if required
// fields for the named provider are missing.
func New(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "", "none":
		return noopProvider{}, nil
	case "bearer_static":
		if cfg.BearerStatic == nil {
			return nil, fmt.Errorf("auth: provider=bearer_static requires bearer_static block")
		}
		return newBearerStatic(*cfg.BearerStatic)
	case "api_key":
		if cfg.APIKey == nil {
			return nil, fmt.Errorf("auth: provider=api_key requires api_key block")
		}
		return newAPIKey(*cfg.APIKey)
	case "refresh_token":
		if cfg.RefreshToken == nil {
			return nil, fmt.Errorf("auth: provider=refresh_token requires refresh_token block")
		}
		return newRefreshTokenProvider(*cfg.RefreshToken)
	default:
		return nil, fmt.Errorf("auth: unknown provider %q", cfg.Provider)
	}
}

// ---------- noop ----------

type noopProvider struct{}

func (noopProvider) Apply(context.Context, http.Header, url.Values) error { return nil }

// ---------- bearer_static ----------

type bearerStatic struct{ token string }

func newBearerStatic(c BearerStaticConfig) (Provider, error) {
	token := c.Token
	if c.TokenEnv != "" {
		token = os.Getenv(c.TokenEnv)
	}
	if token == "" {
		return nil, fmt.Errorf("bearer_static: empty token (set token or token_env)")
	}
	return bearerStatic{token: token}, nil
}

func (b bearerStatic) Apply(_ context.Context, h http.Header, _ url.Values) error {
	h.Set("Authorization", "Bearer "+b.token)
	return nil
}

// ---------- api_key ----------

type apiKeyAuth struct {
	in, name, value string
}

func newAPIKey(c APIKeyConfig) (Provider, error) {
	in := c.In
	if in == "" {
		in = "header"
	}
	if c.Name == "" {
		return nil, fmt.Errorf("api_key: name required")
	}
	val := c.Value
	if c.ValueEnv != "" {
		val = os.Getenv(c.ValueEnv)
	}
	if val == "" {
		return nil, fmt.Errorf("api_key: empty value (set value or value_env)")
	}
	return apiKeyAuth{in: in, name: c.Name, value: val}, nil
}

func (a apiKeyAuth) Apply(_ context.Context, h http.Header, q url.Values) error {
	switch a.in {
	case "header":
		h.Set(a.name, a.value)
	case "query":
		q.Set(a.name, a.value)
	case "cookie":
		h.Add("Cookie", a.name+"="+a.value)
	default:
		return fmt.Errorf("api_key: unsupported location %q", a.in)
	}
	return nil
}

// ---------- refresh_token ----------

type refreshTokenProvider struct {
	cfg    RefreshTokenConfig
	skew   time.Duration
	client *http.Client

	mu    sync.Mutex
	cache tokenCache
}

type tokenCache struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func newRefreshTokenProvider(c RefreshTokenConfig) (Provider, error) {
	if c.RefreshEndpoint == "" {
		return nil, fmt.Errorf("refresh_token: refresh_endpoint required")
	}
	if c.CachePath == "" {
		return nil, fmt.Errorf("refresh_token: cache_path required")
	}
	if c.RefreshIn == "" {
		c.RefreshIn = "header"
	}
	if c.RefreshIn != "header" && c.RefreshIn != "body" {
		return nil, fmt.Errorf("refresh_token: refresh_in must be 'header' or 'body', got %q", c.RefreshIn)
	}
	if c.RefreshTokenField == "" {
		c.RefreshTokenField = "refreshToken"
	}
	if c.AccessTokenField == "" {
		c.AccessTokenField = "accessToken"
	}
	if c.BootstrapEnv == "" {
		c.BootstrapEnv = "REFRESH_TOKEN"
	}
	c.CachePath = resolveHomeRelative(c.CachePath)
	skew := defaultRefreshSkew
	if c.SkewSeconds > 0 {
		skew = time.Duration(c.SkewSeconds) * time.Second
	}
	p := &refreshTokenProvider{
		cfg:    c,
		skew:   skew,
		client: &http.Client{Timeout: 30 * time.Second},
	}
	if err := p.loadFromDiskOrEnv(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *refreshTokenProvider) loadFromDiskOrEnv() error {
	if data, err := os.ReadFile(p.cfg.CachePath); err == nil {
		_ = json.Unmarshal(data, &p.cache)
		if p.cache.RefreshToken != "" {
			return nil
		}
	}
	rt := os.Getenv(p.cfg.BootstrapEnv)
	if rt == "" {
		return fmt.Errorf("refresh_token: no cached refresh token and %s is empty", p.cfg.BootstrapEnv)
	}
	p.cache = tokenCache{RefreshToken: rt}
	return p.persist()
}

func (p *refreshTokenProvider) persist() error {
	if err := os.MkdirAll(filepath.Dir(p.cfg.CachePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p.cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.cfg.CachePath, data, 0o600)
}

func (p *refreshTokenProvider) Apply(ctx context.Context, h http.Header, _ url.Values) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shouldRefreshLocked() {
		if err := p.refreshLocked(ctx); err != nil {
			return err
		}
	}
	h.Set("Authorization", "Bearer "+p.cache.AccessToken)
	return nil
}

func (p *refreshTokenProvider) Refresh(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refreshLocked(ctx)
}

func (p *refreshTokenProvider) shouldRefreshLocked() bool {
	return p.cache.AccessToken == "" || time.Until(p.cache.ExpiresAt) < p.skew
}

func (p *refreshTokenProvider) refreshLocked(ctx context.Context) error {
	req, err := p.buildRefreshRequest(ctx)
	if err != nil {
		return fmt.Errorf("refresh: build request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("refresh: status %d", resp.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("refresh: decode: %w", err)
	}
	nested := raw
	if d, ok := raw["data"].(map[string]any); ok {
		nested = d
	}
	access, _ := nested[p.cfg.AccessTokenField].(string)
	if access == "" {
		return fmt.Errorf("refresh: response missing %q", p.cfg.AccessTokenField)
	}
	p.cache.AccessToken = access
	if rt, ok := nested[p.cfg.RefreshTokenField].(string); ok && rt != "" {
		p.cache.RefreshToken = rt
	}
	p.cache.ExpiresAt = p.computeExpiry(access, nested)
	return p.persist()
}

// buildRefreshRequest produces the POST that exchanges a refresh token for a
// new access token. The token goes in either a header or the JSON body
// depending on RefreshIn.
func (p *refreshTokenProvider) buildRefreshRequest(ctx context.Context) (*http.Request, error) {
	if p.cfg.RefreshIn == "body" {
		body, err := json.Marshal(map[string]string{p.cfg.RefreshTokenField: p.cache.RefreshToken})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.RefreshEndpoint, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.RefreshEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(p.cfg.RefreshTokenField, p.cache.RefreshToken)
	return req, nil
}

func (p *refreshTokenProvider) computeExpiry(accessToken string, body map[string]any) time.Time {
	if p.cfg.ExpiresInField != "" {
		if secs, ok := body[p.cfg.ExpiresInField].(float64); ok {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
	}
	exp, err := tokenExpiry(accessToken)
	if err == nil && !exp.IsZero() {
		return exp
	}
	return time.Now().Add(defaultAccessTokenLifetime)
}

// resolveHomeRelative expands a leading "~" to the user's home directory.
// Returns the input unchanged if home is unavailable.
func resolveHomeRelative(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
