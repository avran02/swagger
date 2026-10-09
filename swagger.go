// Package swagger provides runtime OpenAPI 3.0 spec generation and HTTP
// request binding driven entirely by Go types and handler registration —
// no code generation, no comment parsing.
package swagger

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/avran02/swagger/binder"
	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/codec"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/avran02/swagger/types"
)

// SecurityType is re-exported so callers don't need to import the types package.
type SecurityType = types.SecurityType

const (
	Public SecurityType = types.Public
	Bearer SecurityType = types.Bearer
)

// Config and Server are re-exported for convenience.
type (
	Config = builder.Config
	Server = builder.Server
)

// ValidationError and FieldError are re-exported so callers can inspect
// binding failures without importing the binder package.
type (
	ValidationError = binder.ValidationError
	FieldError      = binder.FieldError
)

// JSONCodec and StrictJSONCodec are re-exported so callers can plug in a
// faster JSON implementation without importing the codec package directly.
type (
	JSONCodec       = codec.Codec
	StrictJSONCodec = codec.StrictCodec
)

// DefaultConfig returns sensible spec metadata defaults.
func DefaultConfig() Config { return builder.DefaultConfig() }

// SetURLParamFunc injects the path-parameter extractor (e.g. chi.URLParam).
// Call once during application startup:
//
//	swagger.SetURLParamFunc(chi.URLParam)
func SetURLParamFunc(fn func(r *http.Request, key string) string) {
	binder.SetURLParamFunc(fn)
}

// SetJSONCodec replaces the JSON implementation used for request bodies and
// error responses (encoding/json by default) with c — e.g. an adapter around
// goccy/go-json, jsoniter, or sonic. Call once during application startup,
// before serving requests.
//
// Implement StrictJSONCodec if your codec can reject unknown body fields;
// codecs that only implement JSONCodec fall back to permissive decoding —
// unknown fields are silently ignored instead of producing a 422.
func SetJSONCodec(c JSONCodec) {
	codec.Set(c)
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
// It is a positional shorthand for the fluent Register builder — use Register
// when you need per-endpoint error codes, summaries, or custom schemas.
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

// BindOr422 binds dst and validates enum values. On invalid input it writes an
// HTTP 422 Unprocessable Entity response with a FastAPI-style JSON body
// ({"detail": [{"loc": ["query","page"], "msg": ..., "type": ...}, ...]}) and
// returns false — the handler should simply return:
//
//	func (c *Controller) Create(w http.ResponseWriter, r *http.Request) {
//	    var req dto.CreateRequest
//	    if !swagger.BindOr422(w, r, &req) {
//	        return
//	    }
//	    // req is fully populated here
//	}
//
// Pair it with Config.Document422 so the 422 response appears in the spec.
// Pass Config.SchemaOptions for externally registered enum types. Invalid enum
// definitions produce a generic HTTP 500 response instead of an input error.
func BindOr422(w http.ResponseWriter, r *http.Request, dst any, options ...SchemaOption) bool {
	if err := binder.BindAndValidate(r, dst, options...); err != nil {
		var definitionError *binder.EnumDefinitionError
		if errors.As(err, &definitionError) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return false
		}
		WriteValidationError(w, err)
		return false
	}
	return true
}

// WriteValidationError writes err as an HTTP 422 response in the FastAPI
// validation-error format. Errors that are not *ValidationError are wrapped
// into a single generic detail entry.
func WriteValidationError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	if !errors.As(err, &verr) {
		verr = &ValidationError{Detail: []FieldError{{
			Loc:  []string{"body"},
			Msg:  err.Error(),
			Type: "validation_error",
		}}}
	}
	data, merr := codec.Marshal(verr)
	if merr != nil {
		http.Error(w, merr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write(data)
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
		_, _ = w.Write(data)
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
		_, _ = w.Write(data)
	}
}

// EnumValues is the optional type-level enum metadata interface. Implementing
// it supplies allowed values to schemas and BindOr422 / BindAndValidate.
type EnumValues = schema.EnumValues

// EnumDefinitionError reports invalid application enum metadata, not input.
type EnumDefinitionError = binder.EnumDefinitionError

// SchemaOption configures schema generation and optional binding validation.
type SchemaOption = schema.Option

// Enum declares allowed values for a scalar type without modifying that type.
// Pass the result in Config.SchemaOptions. Use an explicit type argument for
// untyped constants, e.g. Enum[ExternalStatus](Draft, Published).
func Enum[T schema.EnumScalar](values ...T) SchemaOption { return schema.WithEnum(values...) }

// BindAndValidate binds dst and validates enum values without writing a response.
// Pass the same SchemaOptions used to build the specification for external types.
func BindAndValidate(r *http.Request, dst any, options ...SchemaOption) error {
	return binder.BindAndValidate(r, dst, options...)
}
