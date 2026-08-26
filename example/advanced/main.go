// Command advanced exercises every feature of the library: the fluent
// Register builder alongside the positional RegisterRequestDTO shorthand,
// Public/Bearer security, per-endpoint error codes (including a custom
// per-endpoint error schema), the global ErrorSchema + Document422 config,
// automatic 422 validation errors via BindOr422, a self-referential response
// DTO, a pluggable JSON codec, and the Swagger UI.
//
// For the smallest possible integration, see ../basic.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/advanced/dto"
	"github.com/avran02/swagger/example/advanced/handler"
	"github.com/avran02/swagger/registry"
)

// route pairs a method+pattern with its handler so the same slice can drive
// both ServeMux registration and the swagger walker. With chi you don't need
// this — chi.Walk plays the same role (see README).
type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// passthroughJSONCodec adapts encoding/json to swagger.StrictJSONCodec.
// Swap this for a wrapper around goccy/go-json, jsoniter, sonic, etc. via
// swagger.SetJSONCodec — the library never imports those packages itself.
type passthroughJSONCodec struct{}

func (passthroughJSONCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (passthroughJSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func (passthroughJSONCodec) UnmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func main() {
	folders := handler.NewFolderController()
	users := handler.NewUserController()

	routes := []route{
		// Fluent registration: per-endpoint error codes attach a schema to
		// exactly the operations that can return them.
		{"POST", "/folders", swagger.Register(folders.Create).
			WithRequest(dto.FolderCreateRequest{}).
			WithResponse(dto.FolderCreateResponse{}).
			WithSecurity(swagger.Public).
			WithTags("Folders").
			WithSummary("Create a folder").
			WithError(http.StatusConflict, "Folder already exists"). // uses Config.ErrorSchema
			Handler()},

		{"GET", "/folders/{id}", swagger.Register(folders.Get).
			WithRequest(dto.FolderGetRequest{}).
			WithResponse(dto.FolderGetResponse{}).
			WithSecurity(swagger.Bearer).
			WithTags("Folders").
			WithSummary("Get a folder by ID").
			WithError(http.StatusNotFound, "Folder not found").                   // uses Config.ErrorSchema
			WithErrorSchema(http.StatusGone, "Folder archived", dto.GoneError{}). // custom schema for this code
			Handler()},

		// Positional shorthand — equivalent to the fluent builder without
		// per-endpoint errors. Both styles can be mixed freely.
		{"GET", "/folders", swagger.RegisterRequestDTO(
			folders.List,
			dto.FolderListRequest{},
			dto.FolderListResponse{},
			swagger.Bearer,
			"Folders",
		)},

		{"GET", "/folders/{id}/tree", swagger.Register(folders.Tree).
			WithRequest(dto.FolderTreeRequest{}).
			WithResponse(dto.FolderTreeResponse{}).
			WithSecurity(swagger.Public).
			WithTags("Folders").
			WithSummary("Fetch a folder's subtree").
			Handler()},

		// CreateUserRequest binds JSON body + query + path + header + cookie
		// all at once — see dto.CreateUserRequest.
		{"POST", "/users/{id}", swagger.Register(users.Create).
			WithRequest(dto.CreateUserRequest{}).
			WithResponse(dto.CreateUserResponse{}).
			WithSecurity(swagger.Bearer).
			WithTags("Users").
			WithSummary("Create a user").
			WithError(http.StatusConflict, "User already exists").
			Handler()},
	}

	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.pattern, rt.handler)
	}

	// http.ServeMux (Go 1.22+) exposes path params via r.PathValue; with chi
	// this would be chi.URLParam instead: swagger.SetURLParamFunc(chi.URLParam).
	swagger.SetURLParamFunc(func(r *http.Request, key string) string {
		return r.PathValue(key)
	})

	// ServeMux has no equivalent of chi.Walk, so the walker simply replays the
	// routes slice. This resolves path+method for every registered handler.
	swagger.SetRouter(func(fn registry.WalkFunc) error {
		for _, rt := range routes {
			if err := fn(rt.method, rt.pattern, rt.handler); err != nil {
				return err
			}
		}
		return nil
	})

	// Swap the JSON implementation used for request bodies and error
	// responses. Here it's a no-op passthrough to encoding/json — the point
	// is that the extension point exists without a hard dependency.
	swagger.SetJSONCodec(passthroughJSONCodec{})

	cfg := swagger.Config{
		Title:   "Folders API",
		Version: "1.0.0",
		Servers: []swagger.Server{
			{URL: "https://api.prod.example.com/v1", Description: "Production"},
			{URL: "https://api.staging.example.com/v1", Description: "Staging"},
			{URL: "http://localhost:8080/v1", Description: "Local"},
		},
		// All auto-generated 400/401/404/409/500 responses carry this schema
		// unless a WithErrorSchema override applies (see /folders/{id} above).
		ErrorSchema: dto.APIError{},
		// Handlers use swagger.BindOr422, so document the 422 response too.
		Document422: true,
	}

	// Serve the spec and an interactive UI.
	mux.HandleFunc("GET /openapi.yaml", swagger.ServeSpec(cfg))
	mux.HandleFunc("GET /openapi.json", swagger.ServeSpecJSON(cfg))
	mux.HandleFunc("GET /docs", swagger.ServeUI(swagger.UIConfig{
		Title:   cfg.Title + " – Swagger UI",
		SpecURL: "/openapi.yaml",
	}))

	// Print the spec to stdout on startup so it's visible in this demo.
	yaml, err := swagger.GenerateOpenAPI(cfg)
	if err != nil {
		log.Fatalf("generate openapi: %v", err)
	}
	fmt.Println(string(yaml))

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Println("Listening on :8080 — docs at http://localhost:8080/docs")
	log.Fatal(srv.ListenAndServe())
}
