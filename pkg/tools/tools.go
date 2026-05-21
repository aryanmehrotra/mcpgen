// Package tools translates OpenAPI operations into MCP tool definitions.
package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

type Config struct {
	IncludeTags []string
	ExcludeTags []string
	IncludeOps  []string
	ExcludeOps  []string
}

type ParamLoc string

const (
	ParamPath   ParamLoc = "path"
	ParamQuery  ParamLoc = "query"
	ParamHeader ParamLoc = "header"
	ParamBody   ParamLoc = "body"
)

type ParamRef struct {
	Name     string
	Location ParamLoc
	Required bool
}

type ToolDef struct {
	Name            string
	Description     string
	Method          string
	Path            string
	Params          []ParamRef
	BodyContentType string
	ArgsSchema      map[string]any
	// InputSchema is an alias of ArgsSchema kept for downstream callers that
	// expect the MCP JSON-Schema field. They reference the same map.
	InputSchema map[string]any
}

type Translator struct {
	cfg Config
}

func New(cfg Config) *Translator { return &Translator{cfg: cfg} }

func (t *Translator) Translate(doc *openapi3.T) ([]ToolDef, error) {
	if doc == nil || doc.Paths == nil {
		return nil, fmt.Errorf("nil spec")
	}
	include := toSet(t.cfg.IncludeTags)
	exclude := toSet(t.cfg.ExcludeTags)
	includeOp := toSet(t.cfg.IncludeOps)
	excludeOp := toSet(t.cfg.ExcludeOps)

	components := map[string]*openapi3.SchemaRef{}
	if doc.Components != nil {
		components = doc.Components.Schemas
	}

	out := make([]ToolDef, 0, 64)
	usedNames := map[string]int{}

	for _, p := range sortedPathKeys(doc) {
		item := doc.Paths.Map()[p]
		for method, op := range item.Operations() {
			if op == nil {
				continue
			}
			if !tagsAllowed(op.Tags, include, exclude) {
				continue
			}
			opID := opIdentity(op, method, p)
			if len(includeOp) > 0 && !includeOp[opID] {
				continue
			}
			if excludeOp[opID] {
				continue
			}
			td, err := buildTool(method, p, op, item.Parameters, components)
			if err != nil {
				return nil, fmt.Errorf("build %s %s: %w", method, p, err)
			}
			td.Name = uniq(td.Name, usedNames)
			out = append(out, td)
		}
	}
	return out, nil
}

func sortedPathKeys(doc *openapi3.T) []string {
	paths := doc.Paths.Map()
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func tagsAllowed(tags []string, include, exclude map[string]bool) bool {
	for _, t := range tags {
		if exclude[t] {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, t := range tags {
		if include[t] {
			return true
		}
	}
	return false
}

var nameSan = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func opIdentity(op *openapi3.Operation, method, path string) string {
	if op.OperationID != "" {
		return op.OperationID
	}
	raw := strings.ToLower(method) + "_" + path
	return nameSan.ReplaceAllString(raw, "_")
}

func uniq(name string, used map[string]int) string {
	if used[name] == 0 {
		used[name] = 1
		return name
	}
	used[name]++
	return fmt.Sprintf("%s_%d", name, used[name])
}

// buildTool assembles one ToolDef from an operation. Sub-helpers each handle
// one slice of the schema (path/query/header params, then request body).
func buildTool(method, path string, op *openapi3.Operation, pathItemParams openapi3.Parameters, components map[string]*openapi3.SchemaRef) (ToolDef, error) {
	td := ToolDef{
		Name:        opIdentity(op, method, path),
		Description: descriptionFor(op),
		Method:      strings.ToUpper(method),
		Path:        path,
	}

	properties := map[string]any{}
	required := []string{}

	addParams(&td, properties, &required, pathItemParams, components)
	addParams(&td, properties, &required, op.Parameters, components)
	addRequestBody(&td, properties, &required, op.RequestBody, components)

	td.ArgsSchema = map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		td.ArgsSchema["required"] = required
	}
	td.InputSchema = td.ArgsSchema
	return td, nil
}

func addParams(td *ToolDef, props map[string]any, required *[]string, params openapi3.Parameters, components map[string]*openapi3.SchemaRef) {
	for _, pref := range params {
		if pref == nil || pref.Value == nil {
			continue
		}
		p := pref.Value
		schema := inlineRefsCopy(schemaFor(p.Schema), components)
		if desc, ok := schema["description"].(string); !ok || desc == "" {
			if p.Description != "" {
				schema["description"] = p.Description
			}
		}
		props[p.Name] = schema
		if p.Required {
			*required = append(*required, p.Name)
		}
		td.Params = append(td.Params, ParamRef{
			Name:     p.Name,
			Location: ParamLoc(p.In),
			Required: p.Required,
		})
	}
}

func addRequestBody(td *ToolDef, props map[string]any, required *[]string, ref *openapi3.RequestBodyRef, components map[string]*openapi3.SchemaRef) {
	if ref == nil || ref.Value == nil {
		return
	}
	mime, mt := pickJSONLike(ref.Value.Content)
	if mt == nil || mt.Schema == nil {
		return
	}
	td.BodyContentType = mime
	bodySchema := inlineRefsCopy(schemaFor(mt.Schema), components)

	flat, ok := flattenObjectBody(bodySchema, props)
	if !ok {
		// Array or scalar body, or name collision — keep nested under "body".
		props["body"] = bodySchema
		if ref.Value.Required {
			*required = append(*required, "body")
		}
		td.Params = append(td.Params, ParamRef{
			Name: "body", Location: ParamBody, Required: ref.Value.Required,
		})
		return
	}
	for k, v := range flat.Props {
		props[k] = v
		td.Params = append(td.Params, ParamRef{
			Name: k, Location: ParamBody, Required: ref.Value.Required,
		})
	}
	*required = append(*required, flat.Required...)
}

type flattenedBody struct {
	Props    map[string]any
	Required []string
}

// flattenObjectBody is a pure helper: given an object-typed body schema and
// the already-claimed top-level prop names, return the flattened fields and
// whether flattening is safe. Returns ok=false for non-object bodies or on
// name collision.
func flattenObjectBody(bodySchema map[string]any, existing map[string]any) (flattenedBody, bool) {
	if t, _ := bodySchema["type"].(string); t != "object" {
		return flattenedBody{}, false
	}
	bodyProps, ok := bodySchema["properties"].(map[string]any)
	if !ok || len(bodyProps) == 0 {
		return flattenedBody{}, false
	}
	for k := range bodyProps {
		if _, collides := existing[k]; collides {
			return flattenedBody{}, false
		}
	}
	out := flattenedBody{Props: bodyProps}
	if reqList, ok := bodySchema["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				out.Required = append(out.Required, s)
			}
		}
	}
	return out, true
}

func descriptionFor(op *openapi3.Operation) string {
	parts := []string{}
	if op.Summary != "" {
		parts = append(parts, op.Summary)
	}
	if op.Description != "" && op.Description != op.Summary {
		parts = append(parts, op.Description)
	}
	return strings.Join(parts, "\n")
}

// pickJSONLike returns the JSON-ish media type from a content map, preferring
// application/json, then application/*+json, then */*, then anything with
// "json" in the name, then the first entry encountered.
func pickJSONLike(content openapi3.Content) (string, *openapi3.MediaType) {
	priority := []string{"application/json", "application/*+json"}
	for _, p := range priority {
		if mt, ok := content[p]; ok && mt != nil {
			return p, mt
		}
	}
	if mt, ok := content["*/*"]; ok && mt != nil {
		return "application/json", mt
	}
	for mime, mt := range content {
		if strings.Contains(mime, "json") {
			return mime, mt
		}
	}
	for mime, mt := range content {
		return mime, mt
	}
	return "", nil
}

func schemaFor(ref *openapi3.SchemaRef) map[string]any {
	if ref == nil || ref.Value == nil {
		return map[string]any{}
	}
	data, err := ref.Value.MarshalJSON()
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}
