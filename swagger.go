// Package swagger provides runtime OpenAPI 3.0 spec generation and HTTP
// request binding driven entirely by Go types and handler registration —
// no code generation, no comment parsing.
package swagger

import (
	"encoding/json"
	"net/http"

	"github.com/avran02/swagger/binder"
	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
)

// SecurityType is re-exported so callers don't need to import the types package.
type SecurityType = types.SecurityType

const (
	Public SecurityType = types.Public
	Bearer SecurityType = types.Bearer
)

// Config is re-exported for convenience.
type Config = builder.Config
type Server = builder.Server

// DefaultConfig returns sensible spec metadata defaults.
func DefaultConfig() Config { return builder.DefaultConfig() }

// SetURLParamFunc injects the path-parameter extractor (e.g. chi.URLParam).
// Call once during application startup:
//
//	swagger.SetURLParamFunc(chi.URLParam)
func SetURLParamFunc(fn func(r *http.Request, key string) string) {
	binder.SetURLParamFunc(fn)
}

// SetRouter tells the library how to walk the chi route tree to resolve
// path + method for each registered handler. Call once after all routes
// are mounted, before serving the spec.
//
// With chi:
//
//	swagger.SetRouter(func(fn registry.WalkFunc) error {
//	    return chi.Walk(chiMux, chi.WalkFunc(fn))
//	})
//
// The WalkFunc signature is identical to chi.WalkFunc so the cast is safe.
func SetRouter(walker registry.Walker) {
	registry.Global.SetWalker(walker)
}

// RegisterRequestDTO records handler metadata in the global registry and
// returns the original handler unchanged for direct use with chi.
//
// Path and Method are NOT passed here — they are resolved automatically
// by walking the chi router when the spec is generated.
//
// Usage:
//
//	router.Post("/articles", swagger.RegisterRequestDTO(
//	    ctrl.Create,
//	    dto.ArticleCreateRequest{},
//	    dto.ArticleCreateResponse{},
//	    swagger.Public,
//	    "Articles",
//	))
func RegisterRequestDTO(
	handler http.HandlerFunc,
	req any,
	resp any,
	authType SecurityType,
	tags ...string,
) http.HandlerFunc {
	return registry.Global.RegisterRequestDTO(handler, req, resp, authType, tags)
}

// BindRequest populates dst from the incoming HTTP request.
// See binder.BindRequest for full tag semantics.
func BindRequest(r *http.Request, dst any) error {
	return binder.BindRequest(r, dst)
}

// GenerateOpenAPI builds the OpenAPI 3.0 specification from all registered
// endpoints and returns it serialised as YAML.
// Must be called after SetRouter so that path+method can be resolved.
func GenerateOpenAPI(cfg ...Config) ([]byte, error) {
	c := builder.DefaultConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	spec, err := builder.Build(registry.Global, c)
	if err != nil {
		return nil, err
	}
	return builder.MarshalYAML(spec)
}

// GenerateOpenAPIJSON builds the OpenAPI 3.0 specification and returns it
// serialised as JSON.
func GenerateOpenAPIJSON(cfg ...Config) ([]byte, error) {
	c := builder.DefaultConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	spec, err := builder.Build(registry.Global, c)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(spec, "", "  ")
}

// ServeSpec returns an http.HandlerFunc that serves the OpenAPI YAML spec.
func ServeSpec(cfg ...Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := GenerateOpenAPI(cfg...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}
}

// ServeSpecJSON returns an http.HandlerFunc that serves the OpenAPI JSON spec.
func ServeSpecJSON(cfg ...Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := GenerateOpenAPIJSON(cfg...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	}
}
