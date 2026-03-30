package builder

import (
	"reflect"
	"regexp"
	"sort"
	"strings"

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
		op := buildOperation(gen, ep)
		setOperation(spec.Paths[path], ep.Method, op)
	}

	return spec, nil
}

// buildOperation creates a single OpenAPI Operation from endpoint metadata.
func buildOperation(gen *schema.Generator, ep types.EndpointMeta) *Operation {
	op := &Operation{
		Tags:        ep.Tags,
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

		// Request body — only when the struct has json tags
		if hasJSONFields(rt) && methodHasBody(ep.Method) {
			bodySchema := gen.SchemaForType(rt)
			op.RequestBody = &RequestBody{
				Required: true,
				Content: map[string]*MediaType{
					"application/json": {Schema: bodySchema},
				},
			}
		}
	}

	// 200 response from response DTO
	if ep.ResponseDTO != nil {
		rt := reflect.TypeOf(ep.ResponseDTO)
		respSchema := gen.SchemaForType(rt)
		op.Responses["200"] = &Response{
			Description: "Success",
			Content: map[string]*MediaType{
				"application/json": {Schema: respSchema},
			},
		}
	} else {
		op.Responses["204"] = &Response{Description: "No Content"}
	}

	op.Responses["400"] = &Response{Description: "Bad Request"}
	op.Responses["500"] = &Response{Description: "Internal Server Error"}

	if ep.Security == types.Bearer {
		op.Responses["401"] = &Response{Description: "Unauthorized"}
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
	p = reChiParam.ReplaceAllString(p, `By$1`)
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
