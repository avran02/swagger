package builder_test

import (
	"net/http"
	"testing"

	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
	"gopkg.in/yaml.v3"
)

type createReq struct {
	Name string `json:"name" example:"Alice"`
	Ref  string `query:"ref" required:"false"`
}

type createResp struct {
	ID string `json:"id"`
}

func buildSpec(t *testing.T) *builder.OpenAPISpec {
	t.Helper()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	listH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, createReq{}, createResp{}, types.Bearer, []string{"Things"})
	reg.RegisterRequestDTO(listH, nil, nil, types.Public, []string{"Things"})

	reg.SetWalker(func(fn registry.WalkFunc) error {
		if err := fn("POST", "/things/{id:[0-9]+}", h); err != nil {
			return err
		}
		return fn("GET", "/things", listH)
	})

	spec, err := builder.Build(reg, builder.Config{Title: "Test API", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return spec
}

func TestBuild_Paths(t *testing.T) {
	t.Parallel()
	spec := buildSpec(t)

	// chi regex pattern must be normalised to plain {id}.
	pi, ok := spec.Paths["/things/{id}"]
	if !ok {
		t.Fatalf("path /things/{id} missing; have %v", builder.SortedPaths(spec.Paths))
	}
	op := pi.Post
	if op == nil {
		t.Fatal("POST operation missing")
	}

	if op.OperationID != "postThingsById" {
		t.Errorf("operationId = %q, want postThingsById", op.OperationID)
	}

	// Bearer endpoint: security + 401.
	if len(op.Security) != 1 {
		t.Fatalf("want 1 security requirement, got %+v", op.Security)
	}
	if _, ok := op.Security[0]["bearerAuth"]; !ok {
		t.Error("bearerAuth security requirement missing")
	}
	if _, ok := op.Responses["401"]; !ok {
		t.Error("401 response missing for bearer endpoint")
	}

	// Request body from json fields, query param separate.
	if op.RequestBody == nil {
		t.Fatal("requestBody missing")
	}
	if len(op.Parameters) != 1 || op.Parameters[0].Name != "ref" {
		t.Errorf("want single query param ref, got %+v", op.Parameters)
	}

	// 200 with response schema.
	if op.Responses["200"] == nil {
		t.Error("200 response missing")
	}

	// Public GET endpoint without DTOs: no security, 204.
	get := spec.Paths["/things"].Get
	if get == nil {
		t.Fatal("GET /things missing")
	}
	if len(get.Security) != 0 {
		t.Errorf("public endpoint must have empty security, got %+v", get.Security)
	}
	if get.Responses["204"] == nil {
		t.Error("204 response missing for endpoint without response DTO")
	}

	// Components registered.
	if spec.Components.Schemas["builder_test.createReq"] == nil {
		t.Error("createReq component missing")
	}
	if spec.Components.SecuritySchemes["bearerAuth"] == nil {
		t.Error("bearerAuth security scheme missing")
	}
}

func TestMarshalYAML_RoundTrip(t *testing.T) {
	t.Parallel()
	spec := buildSpec(t)

	data, err := builder.MarshalYAML(spec)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, data)
	}
	if decoded["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v, want 3.0.3", decoded["openapi"])
	}
	if _, ok := decoded["paths"].(map[string]any)["/things/{id}"]; !ok {
		t.Error("paths./things/{id} missing in YAML output")
	}
}

// TestMarshalYAML_SpecialCharacters guards against the escaping bugs the old
// hand-rolled marshaller had: backslashes, quotes and newlines in strings.
func TestMarshalYAML_SpecialCharacters(t *testing.T) {
	t.Parallel()

	title := "Tricky \"API\" \\ with: everything\nand a second line"
	spec := &builder.OpenAPISpec{
		OpenAPI: "3.0.3",
		Info:    builder.Info{Title: title, Version: "1.0.0"},
		Paths:   map[string]*builder.PathItem{},
	}

	data, err := builder.MarshalYAML(spec)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}

	var decoded struct {
		Info builder.Info `yaml:"info"`
	}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, data)
	}
	if decoded.Info.Title != title {
		t.Errorf("title round-trip failed:\n got %q\nwant %q", decoded.Info.Title, title)
	}
}

func TestBuild_ServersFromBasePath(t *testing.T) {
	t.Parallel()

	reg := registry.NewRegistry()
	spec, err := builder.Build(reg, builder.Config{Title: "T", Version: "1", BasePath: "svc/"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "/svc" {
		t.Errorf("servers = %+v, want [{/svc}]", spec.Servers)
	}

	spec, err = builder.Build(reg, builder.Config{
		Title: "T", Version: "1",
		BasePath: "/ignored",
		Servers:  []builder.Server{{URL: "https://x"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "https://x" {
		t.Errorf("explicit Servers must win over BasePath, got %+v", spec.Servers)
	}
}
