package mcpsrv

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/aryanmehrotra/mcpgen/pkg/auth"
	"github.com/aryanmehrotra/mcpgen/pkg/spec"
	"github.com/aryanmehrotra/mcpgen/pkg/tools"
	"github.com/aryanmehrotra/mcpgen/pkg/upstream"
)

// Config is the on-disk YAML schema for a bridge instance.
type Config struct {
	Spec   SpecSource    `yaml:"spec"`
	Auth   auth.Config   `yaml:"auth"`
	Filter FilterConfig  `yaml:"filter"`
	Server ServerConfig  `yaml:"server"`
}

type SpecSource struct {
	URL  string `yaml:"url"`
	File string `yaml:"file"`
}

type FilterConfig struct {
	IncludeTags []string `yaml:"include_tags"`
	ExcludeTags []string `yaml:"exclude_tags"`
	IncludeOps  []string `yaml:"include_ops"`
	ExcludeOps  []string `yaml:"exclude_ops"`
}

type ServerConfig struct {
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
	Transport string `yaml:"transport"`
	Addr      string `yaml:"addr"`
	BaseURL   string `yaml:"base_url"`
}

// LoadConfig reads a YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &c, nil
}

// LoadTools parses the spec and applies filtering, without constructing an
// auth provider or HTTP transport. Used by `validate` and `tools` subcommands
// which inspect the spec but do not call the upstream API.
func LoadTools(ctx context.Context, c *Config) ([]tools.ToolDef, error) {
	doc, err := spec.Load(ctx, spec.Source{URL: c.Spec.URL, File: c.Spec.File})
	if err != nil {
		return nil, err
	}
	translator := tools.New(tools.Config{
		IncludeTags: c.Filter.IncludeTags,
		ExcludeTags: c.Filter.ExcludeTags,
		IncludeOps:  c.Filter.IncludeOps,
		ExcludeOps:  c.Filter.ExcludeOps,
	})
	return translator.Translate(doc)
}

// ResolvedBaseURL returns the configured upstream base URL (server.base_url
// override, else the first server URL in the spec).
func ResolvedBaseURL(ctx context.Context, c *Config) (string, error) {
	if c.Server.BaseURL != "" {
		return c.Server.BaseURL, nil
	}
	doc, err := spec.Load(ctx, spec.Source{URL: c.Spec.URL, File: c.Spec.File})
	if err != nil {
		return "", err
	}
	return spec.BaseURL(doc), nil
}

// App is the fully-wired runtime: one provider, one client, one set of tools.
// Built once in main(); shared between handlers and cron jobs so they observe
// the same in-memory token cache.
type App struct {
	Tools    []tools.ToolDef
	Provider auth.Provider
	Client   *upstream.Client
	BaseURL  string
	Server   *Server
}

// Load assembles an App from a Config. The caller supplies the HTTP transport,
// because main() needs to choose between the stdlib transport and a GoFr-
// instrumented one (the latter requires a registered HTTPService).
func Load(ctx context.Context, c *Config, transport upstream.HTTPTransport) (*App, error) {
	defs, err := LoadTools(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("load tools: %w", err)
	}
	base, err := ResolvedBaseURL(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("resolve base url: %w", err)
	}
	provider, err := auth.New(c.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if transport == nil {
		transport = upstream.NewStdlibTransport(base)
	}
	client := upstream.NewClient(base, provider, transport)
	srv, err := NewServer(Options{Name: c.Server.Name, Version: c.Server.Version}, defs, client)
	if err != nil {
		return nil, fmt.Errorf("build mcp server: %w", err)
	}
	return &App{
		Tools:    defs,
		Provider: provider,
		Client:   client,
		BaseURL:  base,
		Server:   srv,
	}, nil
}
