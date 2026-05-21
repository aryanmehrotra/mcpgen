// Package spec loads OpenAPI v3 documents from a URL or local file.
package spec

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

type Source struct {
	URL  string
	File string
}

func Load(ctx context.Context, src Source) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true

	switch {
	case src.URL != "":
		u, err := url.Parse(src.URL)
		if err != nil {
			return nil, fmt.Errorf("parse spec url: %w", err)
		}
		doc, err := loader.LoadFromURI(u)
		if err != nil {
			return nil, fmt.Errorf("load spec from %s: %w", src.URL, err)
		}
		// Skip strict validation — real-world specs frequently have minor
		// inconsistencies (e.g. unreferenced path params) that don't block tool
		// generation. Callers can re-add Validate() if they need it.
		return doc, nil
	case src.File != "":
		data, err := os.ReadFile(src.File)
		if err != nil {
			return nil, fmt.Errorf("read spec file: %w", err)
		}
		doc, err := loader.LoadFromData(data)
		if err != nil {
			return nil, fmt.Errorf("parse spec file: %w", err)
		}
		return doc, nil
	default:
		return nil, fmt.Errorf("spec source: must set URL or File")
	}
}

// LoadFromBytes parses an OpenAPI v3 document from in-memory bytes.
// Used primarily by tests; production callers use Load with a URL or file.
func LoadFromBytes(ctx context.Context, data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("parse spec bytes: %w", err)
	}
	return doc, nil
}

func BaseURL(doc *openapi3.T) string {
	for _, s := range doc.Servers {
		if s != nil && s.URL != "" {
			return strings.TrimRight(s.URL, "/")
		}
	}
	return ""
}
