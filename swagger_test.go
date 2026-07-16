package swagger_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/registry"
)

type pageReq struct {
	Page  int  `query:"page"`
	Force bool `query:"force"`
}

func TestBindOr422_Success(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/?page=3", nil)
	w := httptest.NewRecorder()

	var req pageReq
	if !swagger.BindOr422(w, r, &req) {
		t.Fatalf("BindOr422 = false, body: %s", w.Body)
	}
	if req.Page != 3 {
		t.Errorf("Page = %d, want 3", req.Page)
	}
	if w.Code != http.StatusOK {
		t.Errorf("nothing should be written on success, got status %d", w.Code)
	}
}

type apiErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type createResp struct {
	ID string `json:"id"`
}

// TestRegister_FluentWithErrors drives the fluent API end-to-end through the
// global registry: registration → walker → generated spec.
func TestRegister_FluentWithErrors(t *testing.T) { //nolint:paralleltest // uses the global registry
	registry.Global.Reset()
	defer registry.Global.Reset()

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	got := swagger.Register(h).
		WithRequest(pageReq{}).
		WithResponse(createResp{}).
		WithSecurity(swagger.Bearer).
		WithTags("Things").
		WithSummary("Create a thing").
		WithError(http.StatusConflict, "Thing already exists").
		Handler()

	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(h).Pointer() {
		t.Fatal("Handler() must return the original handler unchanged")
	}

	swagger.SetRouter(func(fn registry.WalkFunc) error {
		return fn("POST", "/things", h)
	})

	data, err := swagger.GenerateOpenAPIJSON(swagger.Config{
		Title: "T", Version: "1",
		ErrorSchema: apiErr{},
	})
	if err != nil {
		t.Fatalf("GenerateOpenAPIJSON: %v", err)
	}

	var spec struct {
		Paths map[string]map[string]struct {
			Summary   string `json:"summary"`
			Responses map[string]struct {
				Description string `json:"description"`
				Content     map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}

	op, ok := spec.Paths["/things"]["post"]
	if !ok {
		t.Fatalf("POST /things missing in spec: %s", data)
	}
	if op.Summary != "Create a thing" {
		t.Errorf("summary = %q", op.Summary)
	}

	conflict, ok := op.Responses["409"]
	if !ok {
		t.Fatal("409 response missing")
	}
	if conflict.Description != "Thing already exists" {
		t.Errorf("409 description = %q", conflict.Description)
	}
	if ref := conflict.Content["application/json"].Schema.Ref; ref != "#/components/schemas/swagger_test.apiErr" {
		t.Errorf("409 schema ref = %q, want ErrorSchema $ref", ref)
	}
}

func TestWriteError(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	swagger.WriteError(w, http.StatusConflict, apiErr{Code: "already_exists", Message: "nope"})

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body apiErr
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if body.Code != "already_exists" || body.Message != "nope" {
		t.Errorf("body = %+v", body)
	}
}

func TestBindOr422_WritesFastAPIStyle422(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/?page=abc&force=xyz", nil)
	w := httptest.NewRecorder()

	var req pageReq
	if swagger.BindOr422(w, r, &req) {
		t.Fatal("BindOr422 must return false on invalid input")
	}

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var body swagger.ValidationError
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, w.Body)
	}
	if len(body.Detail) != 2 {
		t.Fatalf("want 2 detail entries, got %d: %+v", len(body.Detail), body.Detail)
	}
	first := body.Detail[0]
	if len(first.Loc) != 2 || first.Loc[0] != "query" {
		t.Errorf("loc = %v, want [query <name>]", first.Loc)
	}
	if first.Msg == "" || first.Type == "" {
		t.Errorf("msg/type must be filled: %+v", first)
	}
}
