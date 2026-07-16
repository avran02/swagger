package schema_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/avran02/swagger/schema"
)

type simpleDTO struct {
	Name      string    `json:"name"`
	Age       int32     `json:"age"`
	Score     *float64  `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Hidden    string    `json:"-"`
	Optional  string    `json:"optional" required:"false"`
	unexp     string    //nolint:unused
}

type nestedDTO struct {
	Inner simpleDTO `json:"inner" description:"nested object"`
}

type treeNode struct {
	ID       int64       `json:"id"`
	Children []*treeNode `json:"children"`
}

func TestSchemaForType_Primitives(t *testing.T) {
	t.Parallel()
	g := schema.NewGenerator()

	cases := []struct {
		typ        reflect.Type
		wantType   string
		wantFormat string
	}{
		{reflect.TypeOf(""), "string", ""},
		{reflect.TypeOf(true), "boolean", ""},
		{reflect.TypeOf(int32(0)), "integer", "int32"},
		{reflect.TypeOf(int64(0)), "integer", "int64"},
		{reflect.TypeOf(float32(0)), "number", "float"},
		{reflect.TypeOf(float64(0)), "number", "double"},
		{reflect.TypeOf(time.Time{}), "string", "date-time"},
	}
	for _, c := range cases {
		s := g.SchemaForType(c.typ)
		if s.Type != c.wantType || s.Format != c.wantFormat {
			t.Errorf("%v → type=%q format=%q, want type=%q format=%q",
				c.typ, s.Type, s.Format, c.wantType, c.wantFormat)
		}
	}
}

func TestSchemaForType_StructRefAndRequired(t *testing.T) {
	t.Parallel()
	g := schema.NewGenerator()

	s := g.SchemaForType(reflect.TypeOf(simpleDTO{}))
	if s.Ref == "" {
		t.Fatalf("want $ref for struct, got %+v", s)
	}

	comp, ok := g.Components["schema_test.simpleDTO"]
	if !ok {
		t.Fatalf("component not registered; have %v", keys(g.Components))
	}

	if _, ok := comp.Properties["name"]; !ok {
		t.Error("missing property name")
	}
	if _, ok := comp.Properties["-"]; ok {
		t.Error("json:\"-\" field must be skipped")
	}
	if _, ok := comp.Properties["unexp"]; ok {
		t.Error("unexported field must be skipped")
	}

	wantRequired := map[string]bool{"name": true, "age": true, "created_at": true}
	for _, r := range comp.Required {
		if !wantRequired[r] {
			t.Errorf("field %q must not be required (pointer/omitempty/required:false)", r)
		}
		delete(wantRequired, r)
	}
	for r := range wantRequired {
		t.Errorf("field %q missing from required", r)
	}
}

func TestSchemaForType_RecursiveType(t *testing.T) {
	t.Parallel()
	g := schema.NewGenerator()

	s := g.SchemaForType(reflect.TypeOf(treeNode{}))
	if s.Ref == "" {
		t.Fatal("want $ref for recursive struct")
	}
	comp := g.Components["schema_test.treeNode"]
	if comp == nil {
		t.Fatal("component not registered")
	}
	children := comp.Properties["children"]
	if children == nil || children.Type != "array" {
		t.Fatalf("children should be array, got %+v", children)
	}
	items := children.Items
	if items == nil {
		t.Fatal("children.items missing")
	}
	// []*treeNode → nullable ref must be wrapped in allOf (no $ref siblings).
	if items.Ref != "" && items.Nullable {
		t.Error("nullable set as sibling of $ref — must use allOf wrapper")
	}
	if len(items.AllOf) == 1 && items.AllOf[0].Ref == "" {
		t.Error("allOf wrapper must contain the $ref")
	}
}

func TestFieldAnnotations_WrapRefInAllOf(t *testing.T) {
	t.Parallel()
	g := schema.NewGenerator()

	s := g.SchemaForType(reflect.TypeOf(nestedDTO{}))
	if s.Ref == "" {
		t.Fatal("want $ref")
	}
	comp := g.Components["schema_test.nestedDTO"]
	inner := comp.Properties["inner"]
	if inner == nil {
		t.Fatal("inner property missing")
	}
	if inner.Ref != "" {
		t.Errorf("annotated struct field must not keep bare $ref (siblings are ignored in OpenAPI 3.0), got $ref=%q", inner.Ref)
	}
	if len(inner.AllOf) != 1 || inner.AllOf[0].Ref == "" {
		t.Fatalf("want allOf:[{$ref}], got %+v", inner)
	}
	if inner.Description != "nested object" {
		t.Errorf("Description = %q", inner.Description)
	}
	// The shared component must stay clean.
	if g.Components["schema_test.simpleDTO"].Description != "" {
		t.Error("per-field description leaked into shared component")
	}
}

type paramDTO struct {
	ID      string `path:"id"     required:"false"` // path params are always required
	Page    int    `query:"page"  default:"1" example:"2"`
	Root    *int64 `query:"root"`
	Token   string `header:"X-Token" required:"false" description:"auth token"`
	Session string `cookie:"session"`
}

func TestParametersForType(t *testing.T) {
	t.Parallel()
	g := schema.NewGenerator()

	params := g.ParametersForType(reflect.TypeOf(paramDTO{}))
	byName := map[string]schema.Parameter{}
	for _, p := range params {
		byName[p.In+":"+p.Name] = p
	}

	id, ok := byName["path:id"]
	if !ok || !id.Required {
		t.Errorf("path:id must exist and be required even with required:false, got %+v", id)
	}

	page, ok := byName["query:page"]
	if !ok || !page.Required {
		t.Errorf("query:page must be required (non-pointer, no tag), got %+v", page)
	}
	if page.Schema == nil || page.Schema.Default != "1" {
		t.Errorf("query:page default = %+v, want 1", page.Schema)
	}
	if page.Example != "2" {
		t.Errorf("query:page example = %v, want 2", page.Example)
	}

	if root := byName["query:root"]; root.Required {
		t.Error("pointer query:root must be optional")
	}
	tok := byName["header:X-Token"]
	if tok.Required {
		t.Error("header:X-Token has required:false, must be optional")
	}
	if tok.Description != "auth token" {
		t.Errorf("header description = %q", tok.Description)
	}
	if _, ok := byName["cookie:session"]; !ok {
		t.Error("cookie:session parameter missing")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
