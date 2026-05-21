package upstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

const defaultHTTPTimeout = 30 * time.Second

// stdlibTransport implements HTTPTransport against net/http with no
// observability. Used when no GoFr HTTPService is registered.
type stdlibTransport struct {
	baseURL string
	client  *http.Client
}

func NewStdlibTransport(baseURL string) HTTPTransport {
	return &stdlibTransport{
		baseURL: baseURL,
		client:  &http.Client{Timeout: defaultHTTPTimeout},
	}
}

func (s *stdlibTransport) Do(ctx context.Context, r Request) (Response, error) {
	fullURL := s.baseURL + r.Path
	if enc := r.Query.Encode(); enc != "" {
		fullURL += "?" + enc
	}
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, fullURL, body)
	if err != nil {
		return Response{}, err
	}
	for k, vs := range r.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Body: data}, err
}
