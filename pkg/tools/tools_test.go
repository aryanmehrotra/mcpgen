package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aryanmehrotra/mcpgen/pkg/spec"
	"github.com/aryanmehrotra/mcpgen/pkg/tools"
)

// fixtureSpec is a minimal OpenAPI 3 doc covering the translator features we
// care about: tags, body bodies (object + array), $refs, path params.
const fixtureSpec = `
openapi: 3.0.0
info: { title: t, version: "1" }
paths:
  /updates:
    post:
      tags: [Updates]
      operationId: postUpdate
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/UpdateDto"
      responses: { default: { description: ok } }
  /updates/bulk:
    post:
      tags: [Updates]
      operationId: postBulkUpdates
      requestBody:
        content:
          application/json:
            schema:
              type: array
              items: { $ref: "#/components/schemas/UpdateDto" }
      responses: { default: { description: ok } }
  /updates/{id}:
    delete:
      tags: [Updates]
      operationId: deleteUpdate
      parameters:
        - { name: id, in: path, required: true, schema: { type: integer } }
      responses: { default: { description: ok } }
  /employees/{id}/timeline:
    get:
      tags: [Employees]
      operationId: getEmployeeTimeline
      parameters:
        - { name: id, in: path, required: true, schema: { type: integer } }
        - { name: startDate, in: query, required: true, schema: { type: string } }
      responses: { default: { description: ok } }
  /admin/secret:
    get:
      tags: [Admin]
      operationId: adminSecret
      responses: { default: { description: ok } }
components:
  schemas:
    UpdateDto:
      type: object
      required: [remarks, date]
      properties:
        remarks:   { type: string }
        date:      { type: string, format: date }
        projectId: { type: integer }
`

func loadFixture(t *testing.T) []tools.ToolDef {
	t.Helper()
	doc, err := spec.LoadFromBytes(context.Background(), []byte(fixtureSpec))
	if err != nil {
		t.Fatalf("load fixture spec: %v", err)
	}
	defs, err := tools.New(tools.Config{}).Translate(doc)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	return defs
}

func findTool(defs []tools.ToolDef, name string) (tools.ToolDef, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return tools.ToolDef{}, false
}

func TestTranslate_GeneratesAllOperationsByDefault(t *testing.T) {
	defs := loadFixture(t)
	if len(defs) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(defs))
	}
}

func TestTranslate_TagFiltering(t *testing.T) {
	cases := []struct {
		name        string
		cfg         tools.Config
		wantInclude []string
		wantExclude []string
	}{
		{
			name:        "include tag",
			cfg:         tools.Config{IncludeTags: []string{"Updates"}},
			wantInclude: []string{"postUpdate", "deleteUpdate", "postBulkUpdates"},
			wantExclude: []string{"getEmployeeTimeline", "adminSecret"},
		},
		{
			name:        "exclude tag wins over include",
			cfg:         tools.Config{IncludeTags: []string{"Updates", "Admin"}, ExcludeTags: []string{"Admin"}},
			wantInclude: []string{"postUpdate"},
			wantExclude: []string{"adminSecret"},
		},
		{
			name:        "include ops",
			cfg:         tools.Config{IncludeOps: []string{"postUpdate"}},
			wantInclude: []string{"postUpdate"},
			wantExclude: []string{"deleteUpdate", "getEmployeeTimeline"},
		},
		{
			name:        "exclude ops",
			cfg:         tools.Config{ExcludeOps: []string{"adminSecret", "deleteUpdate"}},
			wantInclude: []string{"postUpdate", "getEmployeeTimeline"},
			wantExclude: []string{"adminSecret", "deleteUpdate"},
		},
	}
	doc, err := spec.LoadFromBytes(context.Background(), []byte(fixtureSpec))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := tools.New(tc.cfg).Translate(doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wantInclude {
				if _, ok := findTool(defs, want); !ok {
					t.Errorf("expected %q to be included", want)
				}
			}
			for _, want := range tc.wantExclude {
				if _, ok := findTool(defs, want); ok {
					t.Errorf("expected %q to be excluded", want)
				}
			}
		})
	}
}

func TestTranslate_FlattensObjectBody(t *testing.T) {
	defs := loadFixture(t)
	td, ok := findTool(defs, "postUpdate")
	if !ok {
		t.Fatal("postUpdate not found")
	}
	props := td.ArgsSchema["properties"].(map[string]any)
	for _, key := range []string{"remarks", "date", "projectId"} {
		if _, present := props[key]; !present {
			t.Errorf("expected flattened key %q in args", key)
		}
	}
	if _, present := props["body"]; present {
		t.Errorf("object body should be flattened, not wrapped under 'body'")
	}
	// Required from body schema must propagate.
	required := requiredFields(td.ArgsSchema)
	gotRequired := map[string]bool{}
	for _, r := range required {
		gotRequired[r] = true
	}
	if !gotRequired["remarks"] || !gotRequired["date"] {
		t.Errorf("expected 'remarks' and 'date' in required, got %v", required)
	}
}

func TestTranslate_KeepsArrayBodyWrapped(t *testing.T) {
	defs := loadFixture(t)
	td, ok := findTool(defs, "postBulkUpdates")
	if !ok {
		t.Fatal("postBulkUpdates not found")
	}
	props := td.ArgsSchema["properties"].(map[string]any)
	body, present := props["body"]
	if !present {
		t.Fatalf("array body must stay wrapped under 'body', got props %v", keys(props))
	}
	bodyMap := body.(map[string]any)
	if bodyMap["type"] != "array" {
		t.Errorf("expected body.type=array, got %v", bodyMap["type"])
	}
}

func TestTranslate_InlinesRefs(t *testing.T) {
	defs := loadFixture(t)
	td, _ := findTool(defs, "postUpdate")
	raw, _ := json.Marshal(td.ArgsSchema)
	if strings.Contains(string(raw), "$ref") {
		t.Errorf("expected no $ref in inlined schema, got: %s", string(raw))
	}
}

func TestTranslate_PathAndQueryParams(t *testing.T) {
	defs := loadFixture(t)
	td, ok := findTool(defs, "getEmployeeTimeline")
	if !ok {
		t.Fatal("getEmployeeTimeline not found")
	}
	byName := map[string]tools.ParamRef{}
	for _, p := range td.Params {
		byName[p.Name] = p
	}
	if byName["id"].Location != tools.ParamPath {
		t.Errorf("id should be path param, got %v", byName["id"].Location)
	}
	if byName["startDate"].Location != tools.ParamQuery {
		t.Errorf("startDate should be query param, got %v", byName["startDate"].Location)
	}
}

// requiredFields extracts the "required" list from a JSON-schema map,
// tolerating both []string (set by our translator) and []any (after a JSON
// round-trip).
func requiredFields(schema map[string]any) []string {
	switch req := schema["required"].(type) {
	case []string:
		return req
	case []any:
		out := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
