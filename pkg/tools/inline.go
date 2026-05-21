package tools

import (
	"encoding/json"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// inlineRefsCopy returns a deep copy of schema with every "$ref" to
// components/schemas/* replaced by the referenced schema (also inlined).
// Cycles are broken with a stub {type: object, description: "(cycle: X)"}.
//
// Pure function — input is not mutated.
func inlineRefsCopy(schema map[string]any, components map[string]*openapi3.SchemaRef) map[string]any {
	return (&inliner{components: components}).inline(schema, map[string]bool{})
}

type inliner struct {
	components map[string]*openapi3.SchemaRef
}

func (in *inliner) inline(schema map[string]any, seen map[string]bool) map[string]any {
	if schema == nil {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return in.resolveRef(ref, schema, seen)
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = in.inlineValue(v, seen)
	}
	return out
}

func (in *inliner) inlineValue(v any, seen map[string]bool) any {
	switch tv := v.(type) {
	case map[string]any:
		return in.inline(tv, seen)
	case []any:
		out := make([]any, len(tv))
		for i, item := range tv {
			out[i] = in.inlineValue(item, seen)
		}
		return out
	default:
		return v
	}
}

func (in *inliner) resolveRef(ref string, original map[string]any, seen map[string]bool) map[string]any {
	name := refName(ref)
	if name == "" || in.components == nil || in.components[name] == nil {
		// Unresolvable ref — drop the $ref key, keep siblings.
		out := make(map[string]any, len(original))
		for k, v := range original {
			if k == "$ref" {
				continue
			}
			out[k] = in.inlineValue(v, seen)
		}
		return out
	}
	if seen[name] {
		return map[string]any{
			"type":        "object",
			"description": "(cycle: " + name + ")",
		}
	}
	seen[name] = true
	defer func() { seen[name] = false }()

	data, _ := in.components[name].Value.MarshalJSON()
	var resolved map[string]any
	_ = json.Unmarshal(data, &resolved)
	resolved = in.inline(resolved, seen)

	for k, v := range original {
		if k == "$ref" {
			continue
		}
		if _, exists := resolved[k]; !exists {
			resolved[k] = in.inlineValue(v, seen)
		}
	}
	return resolved
}

func refName(ref string) string {
	const prefix = "#/components/schemas/"
	if strings.HasPrefix(ref, prefix) {
		return strings.TrimPrefix(ref, prefix)
	}
	return ""
}
