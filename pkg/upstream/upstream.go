// Package upstream calls an upstream HTTP API based on a tool definition and
// caller-supplied arguments.
//
// The Client splits work into pure helpers (bucketArgs, encodeBody, expandPath,
// applyAuth) and a pluggable HTTPTransport, so unit tests can cover request
// assembly without an HTTP server.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aryanmehrotra/mcpgen/pkg/auth"
	"github.com/aryanmehrotra/mcpgen/pkg/tools"
)

// Request is the structured form of an outgoing HTTP call. Transports consume
// this directly — no URL string round-tripping.
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Body    []byte
}

// Response is the structured form of an HTTP response.
type Response struct {
	Status int
	Body   []byte
}

// HTTPTransport executes a Request. Implementations: stdlib net/http, GoFr's
// instrumented service.HTTP.
type HTTPTransport interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// Client wires tool dispatch to an HTTP transport, with pluggable auth.
type Client struct {
	baseURL   string
	auth      auth.Provider
	transport HTTPTransport
}

// NewClient panics if any required dependency is nil. Construction is a
// boundary concern; callers must wire it explicitly.
func NewClient(baseURL string, p auth.Provider, t HTTPTransport) *Client {
	if p == nil {
		panic("upstream: auth provider must not be nil")
	}
	if t == nil {
		panic("upstream: transport must not be nil")
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		auth:      p,
		transport: t,
	}
}

// BaseURL returns the configured upstream base URL (with trailing slash trimmed).
func (c *Client) BaseURL() string { return c.baseURL }

// Call dispatches one tool invocation. Pure helpers do the heavy lifting; this
// function is just composition.
func (c *Client) Call(ctx context.Context, td tools.ToolDef, args map[string]any) (Response, error) {
	req, err := buildRequest(td, args)
	if err != nil {
		return Response{}, fmt.Errorf("build request for %s: %w", td.Name, err)
	}
	if err := c.auth.Apply(ctx, req.Headers, req.Query); err != nil {
		return Response{}, fmt.Errorf("apply auth: %w", err)
	}
	return c.transport.Do(ctx, req)
}

// ---------- pure assembly helpers ----------

type argBuckets struct {
	path    map[string]string
	query   url.Values
	headers http.Header
	body    any
	hasBody bool
}

// bucketArgs sorts caller args into path / query / header / body buckets,
// keyed by parameter metadata on the ToolDef.
func bucketArgs(params []tools.ParamRef, args map[string]any) argBuckets {
	byName := make(map[string]tools.ParamRef, len(params))
	for _, p := range params {
		byName[p.Name] = p
	}
	bodyObj := map[string]any{}
	bucketed := argBuckets{
		path:    map[string]string{},
		query:   url.Values{},
		headers: http.Header{},
	}
	for name, raw := range args {
		p, ok := byName[name]
		if !ok {
			continue
		}
		switch p.Location {
		case tools.ParamPath:
			bucketed.path[name] = stringify(raw)
		case tools.ParamQuery:
			appendQuery(bucketed.query, name, raw)
		case tools.ParamHeader:
			bucketed.headers.Set(name, stringify(raw))
		case tools.ParamBody:
			// Either a single flattened body field (object body was hoisted
			// up by the translator) or the literal "body" arg (array/scalar).
			if name == "body" {
				bucketed.body = raw
				bucketed.hasBody = true
			} else {
				bodyObj[name] = raw
				bucketed.hasBody = true
			}
		}
	}
	if len(bodyObj) > 0 && bucketed.body == nil {
		bucketed.body = bodyObj
	}
	return bucketed
}

// encodeBody renders the body bucket to bytes + content type. Returns (nil,
// "", nil) when there is no body.
func encodeBody(td tools.ToolDef, body any, present bool) ([]byte, string, error) {
	if !present {
		return nil, "", nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode body: %w", err)
	}
	contentType := td.BodyContentType
	if contentType == "" {
		contentType = "application/json"
	}
	return data, contentType, nil
}

// expandPath substitutes {placeholders} with their path-param values.
func expandPath(template string, pathParams map[string]string) string {
	out := template
	for k, v := range pathParams {
		out = strings.ReplaceAll(out, "{"+k+"}", url.PathEscape(v))
	}
	return out
}

// buildRequest assembles a Request without performing IO or invoking auth.
func buildRequest(td tools.ToolDef, args map[string]any) (Request, error) {
	b := bucketArgs(td.Params, args)
	body, contentType, err := encodeBody(td, b.body, b.hasBody)
	if err != nil {
		return Request{}, err
	}
	headers := b.headers
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	headers.Set("Accept", "application/json")
	return Request{
		Method:  td.Method,
		Path:    expandPath(td.Path, b.path),
		Query:   b.query,
		Headers: headers,
		Body:    body,
	}, nil
}

// ---------- coercion ----------

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func appendQuery(q url.Values, name string, v any) {
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			q.Add(name, stringify(item))
		}
	default:
		q.Set(name, stringify(v))
	}
}
