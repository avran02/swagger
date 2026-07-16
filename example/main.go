package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/dto"
	"github.com/avran02/swagger/example/handler"
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

func main() {
	ctrl := handler.NewFolderController()

	routes := []route{
		// Fluent registration: per-endpoint error codes attach the global
		// ErrorSchema to exactly the operations that can return them.
		{"POST", "/folders", swagger.Register(ctrl.Create).
			WithRequest(dto.FolderCreateRequest{}).
			WithResponse(dto.FolderCreateResponse{}).
			WithSecurity(swagger.Public).
			WithTags("Folders").
			WithSummary("Create a folder").
			WithError(http.StatusConflict, "Folder already exists").
			Handler()},
		{"GET", "/folders/{id}", swagger.RegisterRequestDTO(
			ctrl.Get,
			dto.FolderGetRequest{},
			dto.FolderGetResponse{},
			swagger.Bearer,
			"Folders",
		)},
		{"GET", "/folders", swagger.RegisterRequestDTO(
			ctrl.List,
			dto.FolderListRequest{},
			dto.FolderListResponse{},
			swagger.Bearer,
			"Folders",
		)},
	}

	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.pattern, rt.handler)
	}

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

	cfg := swagger.Config{
		Title:   "Folders API",
		Version: "1.0.0",
		Servers: []swagger.Server{
			{URL: "https://api.prod.example.com/v1", Description: "Production"},
			{URL: "https://api.staging.example.com/v1", Description: "Staging"},
			{URL: "http://localhost:8080/v1", Description: "Local"},
		},
		// All auto-generated 400/401/500 responses carry this schema.
		ErrorSchema: dto.APIError{},
		// Handlers use swagger.BindOr422, so document the 422 response too.
		Document422: true,
	}

	// Serve the spec at /openapi.yaml
	mux.HandleFunc("GET /openapi.yaml", swagger.ServeSpec(cfg))
	mux.HandleFunc("GET /openapi.json", swagger.ServeSpecJSON(cfg))

	// ---------------------------------------------------------------------------
	// Print the spec to stdout on startup so it's visible in this demo.
	// ---------------------------------------------------------------------------
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
	log.Println("Listening on :8080")
	log.Fatal(srv.ListenAndServe())
}
