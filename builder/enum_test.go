package builder_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/avran02/swagger/types"
	"gopkg.in/yaml.v3"
)

type enumRequest struct {
	Status string `query:"status" required:"false" enum:"draft,published"`
	Rating int    `json:"rating" enum:"1,2,3,4,5"`
}
type enumResponse struct {
	Status string `json:"status" enum:"draft,published"`
}

func TestBuild_SerializesEnums(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, enumRequest{}, enumResponse{}, types.Public, []string{"Enums"})
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("POST", "/enums", h) })
	spec, err := builder.Build(reg, builder.Config{Title: "Enums", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	formats := []struct {
		name      string
		marshal   func() ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		{"JSON", func() ([]byte, error) { return json.Marshal(spec) }, json.Unmarshal},
		{"YAML", func() ([]byte, error) { return builder.MarshalYAML(spec) }, yaml.Unmarshal},
	}
	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			wire, err := format.marshal()
			if err != nil {
				t.Fatal(err)
			}
			var decoded builder.OpenAPISpec
			if err = format.unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			op := decoded.Paths["/enums"].Post
			if len(op.Parameters) != 1 {
				t.Fatalf("parameters = %+v", op.Parameters)
			}
			if !reflect.DeepEqual(op.Parameters[0].Schema.Enum, []any{"draft", "published"}) {
				t.Fatalf("query enum = %v", op.Parameters[0].Schema.Enum)
			}
			status := decoded.Components.Schemas["builder_test.enumResponse"].Properties["status"]
			if !reflect.DeepEqual(status.Enum, []any{"draft", "published"}) {
				t.Fatalf("response enum = %v", status.Enum)
			}
			rating := decoded.Components.Schemas["builder_test.enumRequest"].Properties["rating"]
			actual, err := json.Marshal(rating.Enum)
			if err != nil || string(actual) != "[1,2,3,4,5]" {
				t.Fatalf("numeric enum = %s; error = %v", actual, err)
			}
		})
	}
}

type thirdPartyStatus string

type configuredEnumDTO struct {
	Status thirdPartyStatus `json:"status"`
}

func TestBuild_ConfiguredTypeEnum(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, configuredEnumDTO{}, types.Public, nil)
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("GET", "/configured", h) })
	cfg := builder.Config{SchemaOptions: []schema.Option{schema.WithEnum[thirdPartyStatus]("draft", "published")}}
	spec, err := builder.Build(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := spec.Components.Schemas["builder_test.configuredEnumDTO"].Properties["status"].Enum
	if !reflect.DeepEqual(got, []any{"draft", "published"}) {
		t.Fatalf("enum = %v", got)
	}
	// A separately built specification is not affected by cfg.
	plain, err := builder.Build(reg, builder.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Components.Schemas["builder_test.configuredEnumDTO"].Properties["status"].Enum; len(got) != 0 {
		t.Fatalf("leaked enum = %v", got)
	}
	cfg.SchemaOptions = []schema.Option{schema.WithEnum[thirdPartyStatus]()}
	if _, err := builder.Build(reg, cfg); err == nil {
		t.Fatal("invalid definition must fail specification generation")
	}
}
