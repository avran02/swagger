// Command basic is the smallest useful integration of the library: one DTO,
// one handler, one registered route, one served spec.
//
// For everything else the library can do (fluent registration, security,
// per-endpoint errors, validation, a custom JSON codec, the Swagger UI, ...)
// see ../advanced.
package main

import (
	"fmt"
	"log"
	"net/http"

	swagger "github.com/avran02/swagger"
	"github.com/avran02/swagger/registry"
)

// GreetRequest is bound from the request: Name from the URL query string,
// Lang from the JSON body.
type GreetRequest struct {
	Name string `query:"name" example:"World"`
	Lang string `json:"lang,omitempty" example:"en"`
}

// GreetResponse is the response DTO, reflected into the spec's response schema.
type GreetResponse struct {
	Message string `json:"message" example:"Hello, World!"`
}

func greet(w http.ResponseWriter, r *http.Request) {
	var req GreetRequest
	// Binds the request into req, or writes a FastAPI-style 422 and returns false.
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	resp := GreetResponse{Message: fmt.Sprintf("Hello, %s!", req.Name)}
	fmt.Fprintf(w, `{"message":%q}`+"\n", resp.Message)
}

func main() {
	mux := http.NewServeMux()

	// Register the handler with the DTOs the spec should reflect. This
	// returns the original handler unchanged, so it plugs into any router.
	handler := swagger.RegisterRequestDTO(greet, GreetRequest{}, GreetResponse{}, swagger.Public, "Greetings")
	mux.HandleFunc("GET /greet", handler)

	// http.ServeMux has no chi.Walk equivalent, so the walker just replays
	// the one route we registered. With chi this is simply:
	//
	//	swagger.SetRouter(func(fn registry.WalkFunc) error {
	//	    return chi.Walk(chiMux, chi.WalkFunc(fn))
	//	})
	swagger.SetRouter(func(fn registry.WalkFunc) error {
		return fn("GET", "/greet", handler)
	})

	mux.HandleFunc("GET /openapi.yaml", swagger.ServeSpec(swagger.Config{
		Title:   "Greetings API",
		Version: "1.0.0",
	}))

	log.Println("Listening on :8080 — try: curl 'http://localhost:8080/greet?name=World'")
	log.Println("Spec at http://localhost:8080/openapi.yaml")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
