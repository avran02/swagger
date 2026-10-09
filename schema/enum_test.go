package schema_test

import (
	"encoding/json"
	"mime/multipart"
	"reflect"
	"testing"

	"github.com/avran02/swagger/schema"
)

type enumString string

func TestGenerator_EnumAcrossSources(t *testing.T) {
	// Identical annotations must survive every binding source, including embedded
	// DTOs, pointers and named string types.
	type EnumFields struct {
		JSON      enumString            `json:"body" enum:"draft, active"`
		Query     *enumString           `query:"status" required:"false" enum:"draft, active"`
		Path      enumString            `path:"status" enum:"draft, active"`
		Header    enumString            `header:"X-Status" enum:"draft, active"`
		Cookie    enumString            `cookie:"status" enum:"draft, active"`
		Form      *enumString           `form:"status" enum:"draft, active"`
		Multipart enumString            `multipart:"mode" enum:"draft, active"`
		Upload    *multipart.FileHeader `form:"file"`
	}
	type request struct{ EnumFields }
	g := schema.NewGenerator()
	g.SchemaForType(reflect.TypeOf(request{}))
	schemas := []*schema.Schema{g.Components["schema_test.request"].Properties["body"]}
	params := g.ParametersForType(reflect.TypeOf(request{}))
	if len(params) != 4 {
		t.Fatalf("parameters = %d, want 4", len(params))
	}
	for _, p := range params {
		schemas = append(schemas, p.Schema)
		if p.Required != (p.In != "query") {
			t.Errorf("%s required = %v", p.In, p.Required)
		}
		if p.In == "query" && !p.Schema.Nullable {
			t.Error("pointer query must remain nullable")
		}
	}
	form := g.MultipartSchemaForType(reflect.TypeOf(request{}))
	schemas = append(schemas, form.Properties["status"], form.Properties["mode"])
	if form.Properties["file"].Format != "binary" {
		t.Error("upload schema lost binary format")
	}
	for _, s := range schemas {
		want := []any{"draft", "active"}
		if s.Nullable {
			want = append(want, nil)
		}
		if s.Type != "string" || !reflect.DeepEqual(s.Enum, want) {
			t.Errorf("schema = %+v", s)
		}
	}
	if !form.Properties["status"].Nullable {
		t.Error("pointer form must remain nullable")
	}
}

func TestGenerator_TypedEnum(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		tag  string
		want string
	}{
		{"string", reflect.TypeOf(enumString("")), "a,b,", `["a","b",""]`},
		{"signed", reflect.TypeOf(int8(0)), "-128,0,127", `[-128,0,127]`},
		{"unsigned", reflect.TypeOf(uint64(0)), "0,18446744073709551615", `[0,18446744073709551615]`},
		{"boolean", reflect.TypeOf(false), "true,false", `[true,false]`},
		{"float32", reflect.TypeOf(float32(0)), "1.5,2.5", `[1.5,2.5]`},
		{"float64", reflect.TypeOf(float64(0)), "-1.5,2.5", `[-1.5,2.5]`},
		{"pointer", reflect.TypeOf((*int)(nil)), "1,2", `[1,2,null]`},
		{"array items", reflect.TypeOf([]enumString{}), "a,b", `["a","b"]`},
		{"nullable array", reflect.TypeOf((*[]enumString)(nil)), "a,b", `["a","b"]`},
		{"pointer items", reflect.TypeOf([]*int{}), "1,2", `[1,2,null]`},
		{"invalid integer", reflect.TypeOf(0), "1,no", `null`},
		{"integer overflow", reflect.TypeOf(int8(0)), "1,128", `null`},
		{"unsigned negative", reflect.TypeOf(uint(0)), "0,-1", `null`},
		{"invalid boolean", reflect.TypeOf(false), "true,no", `null`},
		{"NaN", reflect.TypeOf(float64(0)), "1,NaN", `null`},
		{"infinity", reflect.TypeOf(float64(0)), "1,+Inf", `null`},
		{"float overflow", reflect.TypeOf(float32(0)), "1,1e100", `null`},
		{"object", reflect.TypeOf(struct{}{}), "a,b", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: tc.typ, Tag: reflect.StructTag(`json:"value" query:"value" form:"value" enum:"` + tc.tag + `"`)}})
			g := schema.NewGenerator()
			g.SchemaForType(typ)
			candidates := []*schema.Schema{g.Components[""].Properties["value"], g.ParametersForType(typ)[0].Schema, g.MultipartSchemaForType(typ).Properties["value"]}
			for _, s := range candidates {
				target := s
				if s.Type == "array" {
					if len(s.Enum) != 0 {
						t.Error("item enum must not restrict the whole array")
					}
					target = s.Items
				}
				encoded, err := json.Marshal(target.Enum)
				if err != nil || string(encoded) != tc.want {
					t.Errorf("enum JSON = %s, error = %v; want %s", encoded, err, tc.want)
				}
			}
		})
	}
}
