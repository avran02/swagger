package schema_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/avran02/swagger/schema"
)

type status string

const (
	draft     status = "draft"
	published status = "published"
)

func (status) EnumValues() []any { return []any{draft, published, draft} }

type pointerStatus string

func (*pointerStatus) EnumValues() []any { return []any{pointerStatus("on"), pointerStatus("off")} }

type code int8

func (code) EnumValues() []any { return []any{code(-1), code(2)} }

type invalidStatus string

func (invalidStatus) EnumValues() []any { return []any{"draft", 42} }

type emptyStatus string

func (emptyStatus) EnumValues() []any { return nil }

type overflowCode int8

func (overflowCode) EnumValues() []any { return []any{int64(128)} }

type nullableDefinition string

func (nullableDefinition) EnumValues() []any { return []any{"draft", nil} }

type externalStatus string

type typeEnumDTO struct {
	Body      status             `json:"body"`
	Optional  *status            `json:"optional"`
	Query     status             `query:"status"`
	Path      status             `path:"status"`
	Header    status             `header:"X-Status"`
	Cookie    status             `cookie:"status"`
	Form      status             `form:"status"`
	Multipart status             `multipart:"status"`
	Values    []status           `json:"values"`
	Map       map[string]*status `json:"map"`
	Override  status             `json:"override" enum:"published"`
	Pointer   pointerStatus      `json:"pointer"`
}

func enumJSON(t *testing.T, s *schema.Schema, want string) {
	t.Helper()
	if s == nil {
		t.Fatal("missing schema")
	}
	got, err := json.Marshal(s.Enum)
	if err != nil || string(got) != want {
		t.Fatalf("enum = %s, error = %v; want %s", got, err, want)
	}
}

func TestGenerator_TypeEnumsAcrossSources(t *testing.T) {
	g := schema.NewGenerator()
	body := g.SchemaForType(reflect.TypeOf(typeEnumDTO{}))
	props := g.Components[strings.TrimPrefix(body.Ref, "#/components/schemas/")].Properties
	enumJSON(t, props["body"], `["draft","published"]`)
	enumJSON(t, props["optional"], `["draft","published",null]`)
	enumJSON(t, props["values"].Items, `["draft","published"]`)
	enumJSON(t, props["map"].AdditionalProperties, `["draft","published",null]`)
	enumJSON(t, props["override"], `["published"]`)
	enumJSON(t, props["pointer"], `["on","off"]`)
	for _, p := range g.ParametersForType(reflect.TypeOf(typeEnumDTO{})) {
		enumJSON(t, p.Schema, `["draft","published"]`)
	}
	form := g.MultipartSchemaForType(reflect.TypeOf(typeEnumDTO{}))
	enumJSON(t, form.Properties["status"], `["draft","published"]`)
	enumJSON(t, g.SchemaForType(reflect.TypeOf(code(0))), `[-1,2]`)
	if err := g.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerator_EnumOptionsAreIsolated(t *testing.T) {
	values := []externalStatus{"a", "b"}
	option := schema.WithEnum(values...)
	values[0] = "mutated"
	first := schema.NewGenerator(option)
	second := schema.NewGenerator()
	enumJSON(t, first.SchemaForType(reflect.TypeOf(externalStatus(""))), `["a","b"]`)
	enumJSON(t, second.SchemaForType(reflect.TypeOf(externalStatus(""))), `null`)
	// Mutating generated metadata must not affect another use of the same option.
	s := first.SchemaForType(reflect.TypeOf((*externalStatus)(nil)))
	enumJSON(t, s, `["a","b",null]`)
	s.Enum[0] = "changed"
	enumJSON(t, schema.NewGenerator(option).SchemaForType(reflect.TypeOf(externalStatus(""))), `["a","b"]`)
	overrides := schema.NewGenerator(schema.WithEnum[status]("custom"), schema.WithEnum[status]("final"))
	enumJSON(t, overrides.SchemaForType(reflect.TypeOf(status(""))), `["final"]`)
}

func TestGenerator_InvalidTypeEnumReportsError(t *testing.T) {
	cases := []struct {
		name    string
		typ     reflect.Type
		options []schema.Option
	}{
		{"wrong kind", reflect.TypeOf(invalidStatus("")), nil},
		{"empty", reflect.TypeOf(emptyStatus("")), nil},
		{"overflow", reflect.TypeOf(overflowCode(0)), nil},
		{"explicit null", reflect.TypeOf(nullableDefinition("")), nil},
		{"empty option", reflect.TypeOf(externalStatus("")), []schema.Option{schema.WithEnum[externalStatus]()}},
		{"non-finite option", reflect.TypeOf(float64(0)), []schema.Option{schema.WithEnum(math.Inf(1))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := schema.NewGenerator(tc.options...)
			s := g.SchemaForType(tc.typ)
			if g.Err() == nil {
				t.Fatal("expected definition error")
			}
			if len(s.Enum) != 0 {
				t.Fatalf("partial enum = %v", s.Enum)
			}
			if !strings.Contains(g.Err().Error(), tc.typ.String()) {
				t.Fatalf("error lacks type context: %v", g.Err())
			}
		})
	}
}

type objectEnum struct{}

func (objectEnum) EnumValues() []any { return []any{"unexpected"} }

func TestGenerator_RejectsNonScalarEnumField(t *testing.T) {
	type request struct {
		Value objectEnum `json:"value"`
	}
	g := schema.NewGenerator()
	g.SchemaForType(reflect.TypeOf(request{}))
	if err := g.Err(); err == nil || !strings.Contains(err.Error(), "only scalar types") {
		t.Fatalf("error = %v", err)
	}
}
