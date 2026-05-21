// Package mcpsrv exposes translated OpenAPI tools over MCP (stdio / HTTP / SSE).
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/aryanmehrotra/mcpgen/pkg/tools"
	"github.com/aryanmehrotra/mcpgen/pkg/upstream"
)

const (
	defaultServerName  = "mcpgen"
	defaultVersion     = "0.1.0"
	defaultHTTPAddr    = ":8080"
	defaultSSEAddr     = ":8081"
)

type Options struct {
	Name    string
	Version string
}

// Server is a thin wrapper around mark3labs/mcp-go that knows how to invoke
// tools through an upstream.Client.
type Server struct {
	mcp    *server.MCPServer
	client *upstream.Client
	tools  []tools.ToolDef
}

// NewServer registers every tool and returns an error on the first schema-
// marshaling failure (rather than silently dropping tools).
func NewServer(opts Options, defs []tools.ToolDef, client *upstream.Client) (*Server, error) {
	if opts.Name == "" {
		opts.Name = defaultServerName
	}
	if opts.Version == "" {
		opts.Version = defaultVersion
	}
	s := &Server{
		mcp:    server.NewMCPServer(opts.Name, opts.Version, server.WithToolCapabilities(true)),
		client: client,
		tools:  defs,
	}
	for _, td := range defs {
		if err := s.registerTool(td); err != nil {
			return nil, fmt.Errorf("register %s: %w", td.Name, err)
		}
	}
	return s, nil
}

func (s *Server) registerTool(td tools.ToolDef) error {
	raw, err := json.Marshal(td.InputSchema)
	if err != nil {
		return fmt.Errorf("marshal input schema: %w", err)
	}
	t := mcp.NewToolWithRawSchema(td.Name, td.Description, raw)
	s.mcp.AddTool(t, s.handlerFor(td))
	return nil
}

func (s *Server) handlerFor(td tools.ToolDef) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := s.client.Call(ctx, td, req.GetArguments())
		if err != nil {
			return mcp.NewToolResultErrorFromErr("upstream call failed", err), nil
		}
		text := string(resp.Body)
		if text == "" {
			text = fmt.Sprintf("(empty response, status %d)", resp.Status)
		}
		if resp.Status >= 400 {
			return mcp.NewToolResultError(fmt.Sprintf("HTTP %d: %s", resp.Status, text)), nil
		}
		return mcp.NewToolResultText(text), nil
	}
}

func (s *Server) Tools() []tools.ToolDef { return s.tools }

func (s *Server) ServeStdio() error { return server.ServeStdio(s.mcp) }

func (s *Server) ServeHTTP(addr string) error {
	if addr == "" {
		addr = defaultHTTPAddr
	}
	return server.NewStreamableHTTPServer(s.mcp).Start(addr)
}

func (s *Server) ServeSSE(addr string) error {
	if addr == "" {
		addr = defaultSSEAddr
	}
	base := "http://localhost" + addr
	if !strings.HasPrefix(addr, ":") {
		base = "http://" + addr
	}
	return server.NewSSEServer(s.mcp, server.WithBaseURL(base)).Start(addr)
}
