// Run from mcp/: go run ./example
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	swagger "github.com/avran02/swagger"
	swaggermcp "github.com/avran02/swagger/mcp"
	"github.com/avran02/swagger/registry"
)

type GreetingRequest struct {
	Name string `query:"name" description:"Name of the person to greet"`
}
type GreetingResponse struct {
	Message string `json:"message"`
}

func greet(w http.ResponseWriter, r *http.Request) {
	var req GreetingRequest
	if !swagger.BindOr422(w, r, &req) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GreetingResponse{Message: "Hello, " + req.Name + "!"})
}
func main() {
	api := http.NewServeMux()
	handler := swagger.Register(greet).WithRequest(GreetingRequest{}).WithResponse(GreetingResponse{}).WithSummary("Greet a person by name").Handler()
	api.HandleFunc("GET /greet", handler)
	swagger.SetRouter(func(fn registry.WalkFunc) error { return fn("GET", "/greet", handler) })
	api.HandleFunc("GET /openapi.json", swagger.ServeSpecJSON())
	mcpHandler, err := swaggermcp.NewHandler(swaggermcp.Config{BaseURL: "http://127.0.0.1:8080"})
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	go func() {
		server := &http.Server{Addr: "127.0.0.1:8081", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		log.Println("MCP: http://127.0.0.1:8081/mcp")
		log.Fatal(server.ListenAndServe())
	}()
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: api, ReadHeaderTimeout: 5 * time.Second}
	log.Println("API: http://127.0.0.1:8080/greet?name=World")
	log.Fatal(server.ListenAndServe())
}
