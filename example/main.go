package main

import (
	"fmt"
	"log"
	"net/http"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/example/dto"
	"github.com/avran02/swagger/example/handler"
)

func main() {
	ctrl := handler.NewFolderController()

	// ---------------------------------------------------------------------------
	// Route registration — path and method are explicit so the spec builder knows
	// them at registration time without any router introspection.
	// ---------------------------------------------------------------------------
	mux := http.NewServeMux()

	mux.HandleFunc("POST /folders", swagger.RegisterRequestDTO(
		ctrl.Create,
		dto.FolderCreateRequest{},
		dto.FolderCreateResponse{},
		swagger.Public,
		"Folders",
	))

	mux.HandleFunc("GET /folders/{id}", swagger.RegisterRequestDTO(
		ctrl.Get,
		dto.FolderGetRequest{},
		dto.FolderGetResponse{},
		swagger.Bearer,
		"Folders",
	))

	mux.HandleFunc("GET /folders", swagger.RegisterRequestDTO(
		ctrl.List,
		dto.FolderListRequest{},
		dto.FolderListResponse{},
		swagger.Bearer,
		"Folders",
	))

	// Serve the spec at /openapi.yaml
	mux.HandleFunc("GET /openapi.yaml", swagger.ServeSpec(swagger.Config{
		Title:   "Folders API",
		Version: "1.0.0",
	}))

	mux.HandleFunc("GET /openapi.json", swagger.ServeSpecJSON(swagger.Config{
		Title:   "Folders API",
		Version: "1.0.0",
	}))

	// ---------------------------------------------------------------------------
	// Print the spec to stdout on startup so it's visible in this demo.
	// ---------------------------------------------------------------------------
	yaml, err := swagger.GenerateOpenAPI(swagger.Config{
		Title:   "Folders API",
		Version: "1.0.0",
		Servers: []swagger.Server{
			{URL: "https://api.prod.example.com/v1", Description: "Production"},
			{URL: "https://api.staging.example.com/v1", Description: "Staging"},
			{URL: "http://localhost:8080/v1", Description: "Local"},
		},
	})
	if err != nil {
		log.Fatalf("generate openapi: %v", err)
	}
	fmt.Println(string(yaml))

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8081", mux))
}
