package swagger

import (
	"net/http"

	"github.com/avran02/swagger/codec"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
)

// ErrorSpec is re-exported so callers don't need to import the types package.
type ErrorSpec = types.ErrorSpec

// Registration is a fluent builder for endpoint metadata. Create it with
// Register, chain WithX calls, and finish with Handler() — which commits the
// metadata to the registry and returns the ORIGINAL handler, so chi.Walk can
// still match it by function pointer:
//
//	r.Post("/", swagger.Register(ctrl.Create).
//	    WithRequest(dto.CreateRequest{}).
//	    WithResponse(dto.CreateResponse{}).
//	    WithSecurity(swagger.Bearer).
//	    WithTags("Quizzes").
//	    WithError(http.StatusConflict, "Quiz already exists").
//	    Handler())
type Registration struct {
	handler http.HandlerFunc
	meta    types.EndpointMeta
	reg     *registry.Registry
}

// Register starts a fluent endpoint registration for handler.
func Register(handler http.HandlerFunc) *Registration {
	return &Registration{handler: handler, reg: registry.Global}
}

// WithRequest sets the request DTO (body schema + query/path/header/cookie
// parameters are reflected from it).
func (b *Registration) WithRequest(req any) *Registration {
	b.meta.RequestDTO = req
	return b
}

// WithResponse sets the 200-response DTO. Without it the operation documents
// a 204 No Content.
func (b *Registration) WithResponse(resp any) *Registration {
	b.meta.ResponseDTO = resp
	return b
}

// WithResponseMediaTypes overrides the media types of the successful response.
func (b *Registration) WithResponseMediaTypes(mediaTypes ...string) *Registration {
	b.meta.ResponseMediaTypes = append([]string(nil), mediaTypes...)
	return b
}

// WithBinaryResponse documents a downloadable/streamed binary response.
// Common media types are application/octet-stream, application/pdf and image/png.
func (b *Registration) WithBinaryResponse(mediaTypes ...string) *Registration {
	b.meta.BinaryResponse = true
	b.meta.ResponseDTO = nil
	if len(mediaTypes) == 0 {
		mediaTypes = []string{"application/octet-stream"}
	}
	b.meta.ResponseMediaTypes = append([]string(nil), mediaTypes...)
	return b
}

// WithResponseHeader documents a header returned with the successful response.
// example is typically "attachment; filename=report.pdf" for Content-Disposition.
func (b *Registration) WithResponseHeader(name, description, example string) *Registration {
	if b.meta.ResponseHeaders == nil {
		b.meta.ResponseHeaders = make(map[string]types.ResponseHeaderSpec)
	}
	b.meta.ResponseHeaders[name] = types.ResponseHeaderSpec{Description: description, Example: example}
	return b
}

// WithDownload is shorthand for a binary response with Content-Disposition.
func (b *Registration) WithDownload(mediaType, contentDisposition string) *Registration {
	b.WithBinaryResponse(mediaType)
	return b.WithResponseHeader("Content-Disposition", "Download disposition and filename", contentDisposition)
}

// WithSecurity sets the endpoint's security scheme (swagger.Public or
// swagger.Bearer). Default is Public.
func (b *Registration) WithSecurity(t SecurityType) *Registration {
	b.meta.Security = t
	return b
}

// WithTags appends OpenAPI tags.
func (b *Registration) WithTags(tags ...string) *Registration {
	b.meta.Tags = append(b.meta.Tags, tags...)
	return b
}

// WithSummary sets the operation summary shown in Swagger UI.
func (b *Registration) WithSummary(s string) *Registration {
	b.meta.Summary = s
	return b
}

// WithError declares that this endpoint can respond with the given HTTP status
// code. The response body schema is the global Config.ErrorSchema; use
// WithErrorSchema to override it per code. An empty description falls back to
// http.StatusText(code).
func (b *Registration) WithError(code int, description string) *Registration {
	b.meta.Errors = append(b.meta.Errors, types.ErrorSpec{Code: code, Description: description})
	return b
}

// WithErrorSchema is WithError with an explicit body schema for this code,
// overriding the global Config.ErrorSchema.
func (b *Registration) WithErrorSchema(code int, description string, schema any) *Registration {
	b.meta.Errors = append(b.meta.Errors, types.ErrorSpec{Code: code, Description: description, Schema: schema})
	return b
}

// Handler commits the accumulated metadata to the registry and returns the
// original handler for direct use with chi.
func (b *Registration) Handler() http.HandlerFunc {
	return b.reg.RegisterMeta(b.handler, b.meta)
}

// WriteError writes body as a JSON response with the given HTTP status code.
// Pair it with WithError so the code is documented in the spec:
//
//	if exists {
//	    swagger.WriteError(w, http.StatusConflict, dto.APIError{
//	        Code:    "already_exists",
//	        Message: "quiz already exists",
//	    })
//	    return
//	}
func WriteError(w http.ResponseWriter, code int, body any) {
	data, err := codec.Marshal(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}
