// Command mcpgen is the CLI entrypoint for mcpgen — OpenAPI → MCP bridge.
//
// Subcommands:
//
//	serve     run the MCP server over stdio / HTTP / SSE
//	validate  parse spec + config, report tool count
//	tools     dump generated tools as JSON
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/aryanmehrotra/mcpgen/pkg/auth"
	"github.com/aryanmehrotra/mcpgen/pkg/mcpsrv"
	"github.com/aryanmehrotra/mcpgen/pkg/upstream"
)

const (
	upstreamServiceName  = "upstream"
	envConfigPath        = "GOFR_MCP_CONFIG"
	tokenRefreshSchedule = "*/5 * * * *"
	cronRefreshTimeout   = 15 * time.Second
)

// boot is the shared runtime state. It is populated in main() before
// app.Run(), so handlers and cron jobs operate on the SAME app — in
// particular, the SAME auth.Provider, so token-refresh state stays consistent
// across the request path and proactive refresh.
type boot struct {
	cfg *mcpsrv.Config
	app *mcpsrv.App // nil until first handler runs (needs *gofr.Context to pick GoFr transport)
}

func main() {
	b := &boot{}
	gApp := gofr.NewCMD()

	cfgPath := resolveConfigPath()
	if cfgPath != "" {
		if cfg, err := mcpsrv.LoadConfig(cfgPath); err == nil {
			b.cfg = cfg
			if base, err := mcpsrv.ResolvedBaseURL(context.Background(), cfg); err == nil && base != "" {
				gApp.AddHTTPService(upstreamServiceName, base)
				maybeRegisterRefreshCron(gApp, b)
			}
		}
	}

	gApp.SubCommand("serve", b.serve,
		gofr.AddDescription("Run the MCP server (stdio | http | sse)."),
		gofr.AddHelp(`serve --config=<path> [--transport=stdio|http|sse] [--addr=:8080]`))

	gApp.SubCommand("validate", b.validate,
		gofr.AddDescription("Parse spec + config, report generated tool count."),
		gofr.AddHelp(`validate --config=<path>`))

	gApp.SubCommand("tools", b.listTools,
		gofr.AddDescription("Dump generated tools as JSON."),
		gofr.AddHelp(`tools --config=<path>`))

	gApp.Run()
}

// resolveConfigPath checks --config=… in os.Args before app.Run() consumes
// them, falling back to $GOFR_MCP_CONFIG.
func resolveConfigPath() string {
	for _, arg := range os.Args[1:] {
		if v, ok := strings.CutPrefix(arg, "--config="); ok {
			return v
		}
	}
	return os.Getenv(envConfigPath)
}

func maybeRegisterRefreshCron(gApp *gofr.App, b *boot) {
	if b.cfg.Server.Transport != "http" && b.cfg.Server.Transport != "sse" {
		return
	}
	gApp.AddCronJob(tokenRefreshSchedule, "token_refresh", func(ctx *gofr.Context) {
		if b.app == nil {
			return // serve handler hasn't initialized the app yet
		}
		r, ok := b.app.Provider.(auth.Refresher)
		if !ok {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, cronRefreshTimeout)
		defer cancel()
		if err := r.Refresh(cctx); err != nil {
			ctx.Logger.Errorf("proactive refresh failed: %v", err)
			return
		}
		ctx.Logger.Debugf("proactive refresh OK")
	})
}

func (b *boot) loadCfg(ctx *gofr.Context) (*mcpsrv.Config, error) {
	if b.cfg != nil {
		return b.cfg, nil
	}
	path := ctx.Param("config")
	if path == "" {
		path = os.Getenv(envConfigPath)
	}
	if path == "" {
		return nil, fmt.Errorf("--config is required (or set %s)", envConfigPath)
	}
	cfg, err := mcpsrv.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	b.cfg = cfg
	return cfg, nil
}

func (b *boot) buildTransport(ctx *gofr.Context, baseURL string) upstream.HTTPTransport {
	if svc := ctx.GetHTTPService(upstreamServiceName); svc != nil {
		return upstream.NewGoFrTransport(svc)
	}
	return upstream.NewStdlibTransport(baseURL)
}

func (b *boot) serve(ctx *gofr.Context) (any, error) {
	cfg, err := b.loadCfg(ctx)
	if err != nil {
		return nil, err
	}
	transportName := ctx.Param("transport")
	if transportName == "" {
		transportName = cfg.Server.Transport
	}
	if transportName == "" {
		transportName = "stdio"
	}
	addr := ctx.Param("addr")
	if addr == "" {
		addr = cfg.Server.Addr
	}

	base, err := mcpsrv.ResolvedBaseURL(ctx, cfg)
	if err != nil {
		return nil, err
	}
	app, err := mcpsrv.Load(ctx, cfg, b.buildTransport(ctx, base))
	if err != nil {
		return nil, err
	}
	b.app = app // make available to cron

	ctx.Logger.Infof("mcpgen loaded %d tools, transport=%s base=%s", len(app.Tools), transportName, base)

	switch transportName {
	case "stdio":
		return nil, app.Server.ServeStdio()
	case "http":
		ctx.Logger.Infof("MCP Streamable HTTP listening on %s", addr)
		return nil, app.Server.ServeHTTP(addr)
	case "sse":
		ctx.Logger.Infof("MCP SSE listening on %s", addr)
		return nil, app.Server.ServeSSE(addr)
	default:
		return nil, fmt.Errorf("unknown transport %q", transportName)
	}
}

func (b *boot) validate(ctx *gofr.Context) (any, error) {
	cfg, err := b.loadCfg(ctx)
	if err != nil {
		return nil, err
	}
	base, _ := mcpsrv.ResolvedBaseURL(ctx, cfg)
	defs, err := mcpsrv.LoadTools(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("OK: %d tools generated (base=%s)", len(defs), base), nil
}

func (b *boot) listTools(ctx *gofr.Context) (any, error) {
	cfg, err := b.loadCfg(ctx)
	if err != nil {
		return nil, err
	}
	defs, err := mcpsrv.LoadTools(ctx, cfg)
	if err != nil {
		return nil, err
	}
	type summary struct {
		Name        string `json:"name"`
		Method      string `json:"method"`
		Path        string `json:"path"`
		Description string `json:"description,omitempty"`
	}
	out := make([]summary, 0, len(defs))
	for _, d := range defs {
		out = append(out, summary{Name: d.Name, Method: d.Method, Path: d.Path, Description: d.Description})
	}
	enc, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return string(enc), nil
}
