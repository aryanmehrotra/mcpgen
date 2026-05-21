package upstream

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gofr.dev/pkg/gofr/service"
)

// gofrTransport routes upstream HTTP through GoFr's instrumented service
// client. Consumes Request.Path and Request.Query directly — no URL string
// reparsing.
//
// Caveat: GoFr's service.HTTP owns the base URL via the registered HTTPService.
// We forward Request.Path (which must be relative).
type gofrTransport struct {
	svc service.HTTP
}

func NewGoFrTransport(svc service.HTTP) HTTPTransport {
	return &gofrTransport{svc: svc}
}

func (g *gofrTransport) Do(ctx context.Context, r Request) (Response, error) {
	path := strings.TrimPrefix(r.Path, "/")
	headers := flattenHeaders(r.Headers)
	queryParams := flattenQuery(r.Query)

	var (
		resp *http.Response
		err  error
	)
	switch strings.ToUpper(r.Method) {
	case http.MethodGet:
		resp, err = g.svc.GetWithHeaders(ctx, path, queryParams, headers)
	case http.MethodPost:
		resp, err = g.svc.PostWithHeaders(ctx, path, queryParams, r.Body, headers)
	case http.MethodPut:
		resp, err = g.svc.PutWithHeaders(ctx, path, queryParams, r.Body, headers)
	case http.MethodPatch:
		resp, err = g.svc.PatchWithHeaders(ctx, path, queryParams, r.Body, headers)
	case http.MethodDelete:
		resp, err = g.svc.DeleteWithHeaders(ctx, path, r.Body, headers)
	default:
		return Response{}, fmt.Errorf("gofr transport: unsupported method %q", r.Method)
	}
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Body: data}, readErr
}

func flattenHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// flattenQuery converts url.Values into the map[string]any shape GoFr's
// service.HTTP expects. Single-value keys are passed as string; multi-value
// keys are passed as []string.
func flattenQuery(q map[string][]string) map[string]any {
	if len(q) == 0 {
		return nil
	}
	out := make(map[string]any, len(q))
	for k, vs := range q {
		if len(vs) == 1 {
			out[k] = vs[0]
		} else {
			out[k] = vs
		}
	}
	return out
}
