package builder_test

import (
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
	"gopkg.in/yaml.v3"
)

type uploadReq struct {
	File  *multipart.FileHeader   `form:"file" required:"true"`
	Files []*multipart.FileHeader `form:"files" required:"false"`
	Title string                  `form:"title" required:"true"`
}

func TestBuild_MultipartUploadAndBinaryDownload(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterMeta(h, types.EndpointMeta{
		RequestDTO: uploadReq{}, BinaryResponse: true,
		ResponseMediaTypes: []string{"application/pdf", "application/zip"},
		ResponseHeaders: map[string]types.ResponseHeaderSpec{
			"Content-Disposition": {Description: "download", Example: `attachment; filename="report.pdf"`},
		},
	})
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("POST", "/files", h) })
	spec, err := builder.Build(reg, builder.Config{Title: "T", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	op := spec.Paths["/files"].Post
	mt := op.RequestBody.Content["multipart/form-data"]
	if mt == nil || mt.Schema.Properties["file"].Format != "binary" {
		t.Fatalf("multipart schema = %+v", mt)
	}
	if mt.Schema.Properties["files"].Items.Format != "binary" {
		t.Fatalf("files schema = %+v", mt.Schema.Properties["files"])
	}
	resp := op.Responses["200"]
	if resp.Content["application/pdf"].Schema.Format != "binary" || resp.Content["application/zip"] == nil {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Headers["Content-Disposition"].Example == nil {
		t.Fatalf("headers = %+v", resp.Headers)
	}
}

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

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func TestBuild_ErrorSchemaAndDocument422(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	noDTOH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, createReq{}, createResp{}, types.Bearer, []string{"T"})
	reg.RegisterRequestDTO(noDTOH, nil, nil, types.Public, []string{"T"})
	reg.SetWalker(func(fn registry.WalkFunc) error {
		if err := fn("POST", "/things", h); err != nil {
			return err
		}
		return fn("GET", "/plain", noDTOH)
	})

	spec, err := builder.Build(reg, builder.Config{
		Title: "T", Version: "1",
		ErrorSchema: apiError{},
		Document422: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	op := spec.Paths["/things"].Post
	for _, code := range []string{"400", "401", "500"} {
		resp := op.Responses[code]
		if resp == nil || resp.Content == nil {
			t.Errorf("%s response must carry ErrorSchema content, got %+v", code, resp)
			continue
		}
		s := resp.Content["application/json"].Schema
		if s == nil || s.Ref != "#/components/schemas/builder_test.apiError" {
			t.Errorf("%s schema = %+v, want $ref to apiError", code, s)
		}
	}

	v := op.Responses["422"]
	if v == nil || v.Content == nil {
		t.Fatalf("422 response missing for endpoint with request DTO: %+v", v)
	}
	s := v.Content["application/json"].Schema
	if s == nil || s.Ref != "#/components/schemas/binder.ValidationError" {
		t.Errorf("422 schema = %+v, want $ref to binder.ValidationError", s)
	}
	if spec.Components.Schemas["binder.ValidationError"] == nil {
		t.Error("binder.ValidationError component missing")
	}

	// Endpoint without request DTO must not get a 422.
	if spec.Paths["/plain"].Get.Responses["422"] != nil {
		t.Error("422 must not be documented for endpoints without a request DTO")
	}
}

type goneError struct {
	Reason string `json:"reason"`
}

func TestBuild_EndpointErrors_FallbackAndOverride(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterMeta(h, types.EndpointMeta{
		Security: types.Public,
		Tags:     []string{"T"},
		Errors: []types.ErrorSpec{
			{Code: 409}, // description falls back to StatusText
			{Code: 400, Description: "Custom bad request"},        // overrides the auto-generated 400
			{Code: 410, Description: "Gone", Schema: goneError{}}, // per-code schema override
		},
	})
	reg.SetWalker(func(fn registry.WalkFunc) error {
		return fn("DELETE", "/things/{id}", h)
	})

	spec, err := builder.Build(reg, builder.Config{
		Title: "T", Version: "1",
		ErrorSchema: apiError{},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := spec.Paths["/things/{id}"].Delete

	conflict := op.Responses["409"]
	if conflict == nil || conflict.Description != "Conflict" {
		t.Errorf("409 description must fall back to StatusText, got %+v", conflict)
	}
	if conflict != nil && conflict.Content["application/json"].Schema.Ref != "#/components/schemas/builder_test.apiError" {
		t.Errorf("409 must use global ErrorSchema, got %+v", conflict.Content)
	}

	if bad := op.Responses["400"]; bad == nil || bad.Description != "Custom bad request" {
		t.Errorf("declared 400 must override the auto response, got %+v", bad)
	}

	gone := op.Responses["410"]
	if gone == nil || gone.Content["application/json"].Schema.Ref != "#/components/schemas/builder_test.goneError" {
		t.Errorf("410 must use its own schema, got %+v", gone)
	}
}

func TestBuild_NoErrorSchema_PlainResponses(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, createReq{}, nil, types.Public, []string{"T"})
	reg.SetWalker(func(fn registry.WalkFunc) error {
		return fn("POST", "/things", h)
	})

	spec, err := builder.Build(reg, builder.Config{Title: "T", Version: "1"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	op := spec.Paths["/things"].Post
	if op.Responses["400"].Content != nil {
		t.Error("without ErrorSchema the 400 response must have no content")
	}
	if op.Responses["422"] != nil {
		t.Error("422 must not appear unless Document422 is set")
	}
}

// fileNode mirrors the structure printed by the linux `tree` command: a
// directory entry that recursively contains more entries of the same type.
type fileNode struct {
	Name     string      `json:"name"`
	IsDir    bool        `json:"isDir"`
	Children []*fileNode `json:"children,omitempty"`
}

func TestBuild_RecursiveComponent_TreeEndpoint(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, fileNode{}, types.Public, []string{"Tree"})
	reg.SetWalker(func(fn registry.WalkFunc) error {
		return fn("GET", "/tree", h)
	})

	spec, err := builder.Build(reg, builder.Config{Title: "T", Version: "1"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	op := spec.Paths["/tree"].Get
	if op == nil {
		t.Fatal("GET /tree missing")
	}
	resp := op.Responses["200"]
	if resp == nil || resp.Content == nil {
		t.Fatal("200 response missing")
	}
	respSchema := resp.Content["application/json"].Schema
	if respSchema == nil || respSchema.Ref != "#/components/schemas/builder_test.fileNode" {
		t.Fatalf("200 schema = %+v, want $ref to fileNode", respSchema)
	}

	comp := spec.Components.Schemas["builder_test.fileNode"]
	if comp == nil {
		t.Fatal("fileNode component missing")
	}
	children := comp.Properties["children"]
	if children == nil || children.Type != "array" {
		t.Fatalf("children should be array, got %+v", children)
	}
	items := children.Items
	if items == nil {
		t.Fatal("children.items missing")
	}
	// []*fileNode → nullable ref must be wrapped in allOf (no $ref siblings).
	itemRef := items.Ref
	if itemRef == "" && len(items.AllOf) == 1 {
		itemRef = items.AllOf[0].Ref
	}
	if itemRef != "#/components/schemas/builder_test.fileNode" {
		t.Fatalf("children.items must $ref back to fileNode, got %+v", items)
	}

	// The recursive component must not blow up YAML marshalling (no infinite
	// expansion / cyclic struct walk).
	if _, err := builder.MarshalYAML(spec); err != nil {
		t.Fatalf("MarshalYAML with recursive component: %v", err)
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
