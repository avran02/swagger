package builder

import (
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/avran02/swagger/binder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/avran02/swagger/types"
)

// OpenAPISpec is the top-level OpenAPI 3.0 document.
type OpenAPISpec struct {
	OpenAPI    string                `yaml:"openapi" json:"openapi"`
	Info       Info                  `yaml:"info" json:"info"`
	Servers    []Server              `yaml:"servers,omitempty" json:"servers,omitempty"`
	Paths      map[string]*PathItem  `yaml:"paths" json:"paths"`
	Components Components            `yaml:"components" json:"components"`
	Security   []SecurityRequirement `yaml:"security,omitempty" json:"security,omitempty"`
}

// Server represents an OpenAPI 3.0 Server Object.
type Server struct {
	URL         string `yaml:"url" json:"url"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

type Info struct {
	Title   string `yaml:"title" json:"title"`
	Version string `yaml:"version" json:"version"`
}

type PathItem struct {
	Get     *Operation `yaml:"get,omitempty" json:"get,omitempty"`
	Post    *Operation `yaml:"post,omitempty" json:"post,omitempty"`
	Put     *Operation `yaml:"put,omitempty" json:"put,omitempty"`
	Patch   *Operation `yaml:"patch,omitempty" json:"patch,omitempty"`
	Delete  *Operation `yaml:"delete,omitempty" json:"delete,omitempty"`
	Options *Operation `yaml:"options,omitempty" json:"options,omitempty"`
	Head    *Operation `yaml:"head,omitempty" json:"head,omitempty"`
}

type Operation struct {
	Tags        []string              `yaml:"tags,omitempty" json:"tags,omitempty"`
	Summary     string                `yaml:"summary,omitempty" json:"summary,omitempty"`
	OperationID string                `yaml:"operationId,omitempty" json:"operationId,omitempty"`
	Parameters  []schema.Parameter    `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	RequestBody *RequestBody          `yaml:"requestBody,omitempty" json:"requestBody,omitempty"`
	Responses   map[string]*Response  `yaml:"responses" json:"responses"`
	Security    []SecurityRequirement `yaml:"security,omitempty" json:"security,omitempty"`
}

type RequestBody struct {
	Required bool                  `yaml:"required,omitempty" json:"required,omitempty"`
	Content  map[string]*MediaType `yaml:"content" json:"content"`
}

type MediaType struct {
	Schema *schema.Schema `yaml:"schema,omitempty" json:"schema,omitempty"`
}

type Response struct {
	Description string                `yaml:"description" json:"description"`
	Content     map[string]*MediaType `yaml:"content,omitempty" json:"content,omitempty"`
	Headers     map[string]*Header    `yaml:"headers,omitempty" json:"headers,omitempty"`
}

type Header struct {
	Description string         `yaml:"description,omitempty" json:"description,omitempty"`
	Schema      *schema.Schema `yaml:"schema,omitempty" json:"schema,omitempty"`
	Example     any            `yaml:"example,omitempty" json:"example,omitempty"`
}

type Components struct {
	Schemas         map[string]*schema.Schema  `yaml:"schemas,omitempty" json:"schemas,omitempty"`
	SecuritySchemes map[string]*SecurityScheme `yaml:"securitySchemes,omitempty" json:"securitySchemes,omitempty"`
}

type SecurityScheme struct {
	Type         string `yaml:"type" json:"type"`
	Scheme       string `yaml:"scheme,omitempty" json:"scheme,omitempty"`
	BearerFormat string `yaml:"bearerFormat,omitempty" json:"bearerFormat,omitempty"`
	In           string `yaml:"in,omitempty" json:"in,omitempty"`
	Name         string `yaml:"name,omitempty" json:"name,omitempty"`
}

type SecurityRequirement map[string][]string

// Config controls the generated spec metadata.
type Config struct {
	Title   string
	Version string

	// BasePath is the URL prefix that an external reverse proxy (e.g. Caddy)
	// routes to this service. It is emitted as an OpenAPI servers entry so that
	// Swagger UI sends requests to the correct path.
	//
	// The service itself never sees this prefix — Caddy strips it before
	// forwarding. Paths in the spec remain relative to the service root.
	//
	// Example:
	//   BasePath: "/nomenclature-service"
	//   → servers: [{url: "/nomenclature-service"}]
	//   Swagger UI will call /nomenclature-service/v1/folders
	//   Caddy strips /nomenclature-service → service receives /v1/folders
	//
	// Leave empty to omit the servers block (default: relative root "/").
	BasePath string

	// Servers allows full control over the servers block when BasePath is not
	// sufficient (e.g. multiple environments). If set, BasePath is ignored.
	Servers []Server

	// ErrorSchema, when set, is reflected like a response DTO and attached as
	// the application/json body of every auto-generated error response
	// (400, 401, 500). Pass a value of your API's error type:
	//
	//	swagger.Config{ErrorSchema: dto.APIError{}}
	ErrorSchema any

	// Document422 adds a "422 Unprocessable Entity" response (with the
	// binder.ValidationError schema, FastAPI-style) to every operation that
	// has a request DTO. Enable it when handlers use swagger.BindOr422.
	Document422 bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{Title: "API", Version: "1.0.0"}
}

// Build assembles an OpenAPISpec from the given registry using cfg for metadata.
func Build(reg *registry.Registry, cfg Config) (*OpenAPISpec, error) {
	gen := schema.NewGenerator()

	spec := &OpenAPISpec{
		OpenAPI: "3.0.3",
		Info:    Info{Title: cfg.Title, Version: cfg.Version},
		Paths:   make(map[string]*PathItem),
		Components: Components{
			Schemas: gen.Components,
			SecuritySchemes: map[string]*SecurityScheme{
				"bearerAuth": {
					Type:         "http",
					Scheme:       "bearer",
					BearerFormat: "JWT",
				},
			},
		},
	}

	// Servers: explicit list wins; fall back to BasePath shorthand.
	switch {
	case len(cfg.Servers) > 0:
		spec.Servers = cfg.Servers
	case cfg.BasePath != "":
		spec.Servers = []Server{{URL: cleanBasePath(cfg.BasePath)}}
	}

	endpoints, err := reg.Endpoints()
	if err != nil {
		return nil, err
	}

	for _, ep := range endpoints {
		if ep.Path == "" || ep.Method == "" {
			continue
		}
		path := chiPathToOpenAPI(ep.Path)
		if _, ok := spec.Paths[path]; !ok {
			spec.Paths[path] = &PathItem{}
		}
		op := buildOperation(gen, ep, cfg)
		setOperation(spec.Paths[path], ep.Method, op)
	}

	return spec, nil
}

// buildOperation creates a single OpenAPI Operation from endpoint metadata.
func buildOperation(gen *schema.Generator, ep types.EndpointMeta, cfg Config) *Operation {
	op := &Operation{
		Tags:        ep.Tags,
		Summary:     ep.Summary,
		OperationID: operationID(ep.Method, ep.Path),
		Responses:   make(map[string]*Response),
	}

	// Security requirement
	switch ep.Security {
	case types.Bearer:
		op.Security = []SecurityRequirement{{"bearerAuth": {}}}
	case types.Public:
		op.Security = []SecurityRequirement{}
	}

	// Parameters (query / path / header / cookie) from request DTO
	if ep.RequestDTO != nil {
		rt := reflect.TypeOf(ep.RequestDTO)
		params := gen.ParametersForType(rt)
		op.Parameters = params

		// Multipart takes precedence when form fields are present.
		if hasFormFields(rt) && methodHasBody(ep.Method) {
			op.RequestBody = &RequestBody{Required: true, Content: map[string]*MediaType{
				"multipart/form-data": {Schema: gen.MultipartSchemaForType(rt)},
			}}
		} else if hasJSONFields(rt) && methodHasBody(ep.Method) {
			bodySchema := gen.SchemaForType(rt)
			op.RequestBody = &RequestBody{
				Required: true,
				Content: map[string]*MediaType{
					"application/json": {Schema: bodySchema},
				},
			}
		}
	}

	// Successful response, including explicitly documented binary downloads.
	if ep.ResponseDTO != nil || ep.BinaryResponse {
		respSchema := &schema.Schema{Type: "string", Format: "binary"}
		if !ep.BinaryResponse {
			respSchema = gen.SchemaForType(reflect.TypeOf(ep.ResponseDTO))
		}
		mediaTypes := ep.ResponseMediaTypes
		if len(mediaTypes) == 0 {
			mediaTypes = []string{"application/json"}
		}
		content := make(map[string]*MediaType, len(mediaTypes))
		for _, mediaType := range mediaTypes {
			if mediaType != "" {
				content[mediaType] = &MediaType{Schema: respSchema}
			}
		}
		resp := &Response{Description: "Success", Content: content}
		if len(ep.ResponseHeaders) > 0 {
			resp.Headers = make(map[string]*Header, len(ep.ResponseHeaders))
			for name, h := range ep.ResponseHeaders {
				resp.Headers[name] = &Header{Description: h.Description, Schema: &schema.Schema{Type: "string"}, Example: h.Example}
			}
		}
		op.Responses["200"] = resp
	} else {
		op.Responses["204"] = &Response{Description: "No Content"}
	}

	// Auto-generated error responses. When cfg.ErrorSchema is set they carry
	// its schema as the JSON body.
	errResp := func(desc string) *Response {
		resp := &Response{Description: desc}
		if cfg.ErrorSchema != nil {
			resp.Content = map[string]*MediaType{
				"application/json": {Schema: gen.SchemaForType(reflect.TypeOf(cfg.ErrorSchema))},
			}
		}
		return resp
	}
	op.Responses["400"] = errResp("Bad Request")
	op.Responses["500"] = errResp("Internal Server Error")

	if ep.Security == types.Bearer {
		op.Responses["401"] = errResp("Unauthorized")
	}

	// FastAPI-style validation error, documented for operations that bind a
	// request DTO (see swagger.BindOr422).
	if cfg.Document422 && ep.RequestDTO != nil {
		op.Responses["422"] = &Response{
			Description: "Validation Error",
			Content: map[string]*MediaType{
				"application/json": {Schema: gen.SchemaForType(reflect.TypeOf(binder.ValidationError{}))},
			},
		}
	}

	// Per-endpoint error responses declared via swagger.Register(...).WithError.
	// They override the auto-generated response for the same code.
	for _, e := range ep.Errors {
		desc := e.Description
		if desc == "" {
			desc = http.StatusText(e.Code)
		}
		resp := &Response{Description: desc}
		errSchema := e.Schema
		if errSchema == nil {
			errSchema = cfg.ErrorSchema
		}
		if errSchema != nil {
			resp.Content = map[string]*MediaType{
				"application/json": {Schema: gen.SchemaForType(reflect.TypeOf(errSchema))},
			}
		}
		op.Responses[strconv.Itoa(e.Code)] = resp
	}

	return op
}

// setOperation assigns op to the correct method field of PathItem.
func setOperation(pi *PathItem, method string, op *Operation) {
	switch strings.ToUpper(method) {
	case "GET":
		pi.Get = op
	case "POST":
		pi.Post = op
	case "PUT":
		pi.Put = op
	case "PATCH":
		pi.Patch = op
	case "DELETE":
		pi.Delete = op
	case "OPTIONS":
		pi.Options = op
	case "HEAD":
		pi.Head = op
	}
}

// chiPathToOpenAPI converts chi route patterns to OpenAPI path format.
// e.g. /users/{id} stays the same; /users/{id:[0-9]+} → /users/{id}
var reChiParam = regexp.MustCompile(`\{([^}:]+)(?::[^}]*)?\}`)

func chiPathToOpenAPI(path string) string {
	return reChiParam.ReplaceAllString(path, `{$1}`)
}

// operationID builds a deterministic, human-readable operation ID.
func operationID(method, path string) string {
	method = strings.ToLower(method)
	// Remove leading slash, replace slashes and braces.
	p := strings.TrimPrefix(path, "/")
	// {id} → ById (capitalised so the final ID is camelCase, not "Byid").
	p = reChiParam.ReplaceAllStringFunc(p, func(m string) string {
		name := reChiParam.FindStringSubmatch(m)[1]
		return "By" + strings.ToUpper(name[:1]) + name[1:]
	})
	parts := strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '-' || r == '_'
	})
	var sb strings.Builder
	sb.WriteString(method)
	for _, part := range parts {
		if len(part) > 0 {
			sb.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return sb.String()
}

// hasJSONFields reports whether the type (or its pointed-to value) has any
// exported field with a `json` tag.
func hasJSONFields(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup("json"); ok {
			return true
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if hasJSONFields(f.Type) {
				return true
			}
		}
	}
	return false
}

func hasFormFields(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup("form"); ok {
			return true
		}
		if _, ok := f.Tag.Lookup("multipart"); ok {
			return true
		}
		if f.Anonymous && hasFormFields(f.Type) {
			return true
		}
	}
	return false
}

func methodHasBody(method string) bool {
	switch strings.ToUpper(method) {
	case "POST", "PUT", "PATCH":
		return true
	}
	return false
}

// SortedPaths returns path keys sorted alphabetically for deterministic output.
func SortedPaths(paths map[string]*PathItem) []string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cleanBasePath normalises a base path: ensures leading slash, removes trailing slash.
func cleanBasePath(p string) string {
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
