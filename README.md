# goswagger

Runtime OpenAPI 3.0 spec generation and HTTP request binding for Go.

**No comments. No codegen. No AST parsing.** The spec is derived entirely from your DTO types and handler registrations at runtime via reflection.

Works with [chi](https://github.com/go-chi/chi) out of the box. No changes to your existing router setup required.

## How it works

```
RegisterRequestDTO(ctrl.Create, dto.Req{}, dto.Resp{}, swagger.Bearer, "Articles")
  → stores metadata in registry, keyed by handler function pointer

swagger.SetRouter(func(fn registry.WalkFunc) error {
    return chi.Walk(chiMux, chi.WalkFunc(fn))
})
  → walks the chi route tree, matches each handler by pointer
  → fills in Path + Method for every registered endpoint

swagger.GenerateOpenAPI()
  → builds the full OpenAPI 3.0 spec from resolved endpoints + reflected DTO types
```

Path and method come from chi — one source of truth, no duplication, no drift.

## Installation

```bash
go get github.com/avran02/swagger
```

Requires Go 1.22+. The only dependency is `gopkg.in/yaml.v3` (for YAML serialisation); the router (chi) is injected by your code and never imported by the library.

## Examples

Two runnable programs live under `example/`:

- [example/basic](example/basic/main.go) — the smallest useful integration: one DTO, one handler, one route, one served spec.
- [example/advanced](example/advanced/main.go) — every feature exercised: the fluent `Register` builder, `Public`/`Bearer` security, per-endpoint error codes with a custom per-endpoint schema, `Document422`, a self-referential response DTO, a pluggable JSON codec, and the Swagger UI.

```bash
go run ./example/basic     # http://localhost:8080/greet?name=World
go run ./example/advanced  # http://localhost:8080/docs
```

## Quick start

### 1. Register handlers

Wrap your handlers with `swagger.RegisterRequestDTO` wherever you define routes. The signature is identical to `http.HandlerFunc` — chi accepts it directly:

```go
func SetupArticlesRoutes(r *chi.Mux, ctrl *ArticlesController) {
    r.Route("/articles", func(r chi.Router) {
        r.Post("/", swagger.RegisterRequestDTO(
            ctrl.Create,
            dto.ArticleCreateRequest{},
            dto.ArticleCreateResponse{},
            swagger.Public,
            "Articles",
        ))
        r.Get("/", swagger.RegisterRequestDTO(
            ctrl.List,
            dto.ArticleListRequest{},
            dto.ArticleListResponse{},
            swagger.Bearer,
            "Articles",
        ))
        r.Get("/{id}", swagger.RegisterRequestDTO(
            ctrl.Get,
            dto.ArticleGetRequest{},
            dto.ArticleGetResponse{},
            swagger.Bearer,
            "Articles",
        ))
    })
}
```

`RegisterRequestDTO` returns the original handler unchanged. Chi never knows the difference.

### 2. Connect the router

Call `SetRouter` once after all routes are mounted. This tells the library how to walk the route tree to resolve paths:

```go
func NewRouter(ctrl *Controller) *chi.Mux {
    mux := chi.NewRouter()

    v1 := chi.NewMux()
    SetupArticlesRoutes(v1, ctrl.Articles)
    SetupFoldersRoutes(v1, ctrl.Folders)
    mux.Mount("/v1", v1)

    swagger.SetURLParamFunc(chi.URLParam)
    swagger.SetRouter(func(fn registry.WalkFunc) error {
        return chi.Walk(mux, chi.WalkFunc(fn))
    })

    mux.Get("/openapi.yaml", swagger.ServeSpec(swagger.Config{
        Title:   "My API",
        Version: "1.0.0",
    }))

    return mux
}
```

### 3. Bind requests

Use `BindRequest` inside your handlers to fill a struct from the incoming request:

```go
func (c *ArticlesController) Create(w http.ResponseWriter, r *http.Request) {
    var req dto.ArticleCreateRequest
    if err := swagger.BindRequest(r, &req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    // req.Title, req.Body, req.AuthorID are all populated
}
```

## DTOs

### Binding tags

Fields are populated from the request based on their struct tags:

| Tag | Source | Example |
|---|---|---|
| `json` | Request body (JSON) | `json:"name"` |
| `query` | URL query parameters | `query:"page"` |
| `path` | Chi URL parameters | `path:"id"` |
| `header` | HTTP headers | `header:"X-Company-ID"` |
| `cookie` | Cookies | `cookie:"session"` |

A single struct can mix all sources:

```go
type UpdateArticleRequest struct {
    // From path
    ID string `path:"id"`

    // From headers
    CompanyID int64 `header:"X-Company-ID" required:"false" default:"0"`

    // From query
    Notify bool `query:"notify" required:"false" default:"false"`

    // From body
    Title string  `json:"title"`
    Body  string  `json:"body"`
    Draft *bool   `json:"draft,omitempty"`
}
```

### OpenAPI metadata tags

These tags control how the field appears in the generated spec:

| Tag | Effect |
|---|---|
| `required:"false"` | Marks the parameter as optional (non-pointer fields are required by default) |
| `default:"value"` | Sets the default value in the schema; `BindRequest` also applies it when the parameter is missing from the request |
| `example:"value"` | Sets the example value |
| `description:"text"` | Sets the field description |
| `enum:"a,b,c"` | Restricts allowed values |
| `format:"uuid"` | Overrides the schema format |

Pointer fields (`*T`) are optional and nullable by default. `required:"true"` requires the key to be present; its value may still be `null`.

### Multipart uploads

Use `form` tags for `multipart/form-data`. File fields may be
`*multipart.FileHeader` or `[]*multipart.FileHeader`; scalar form fields use the
same conversions and `required`/`default` rules as query parameters.

```go
type UploadRequest struct {
    File        *multipart.FileHeader   `form:"file" required:"true"`
    Attachments []*multipart.FileHeader `form:"attachments" required:"false"`
    Title       string                  `form:"title" required:"true"`
}
```

`BindOr422` parses the multipart body and returns `422` for a missing required
part. The generated request schema uses `format: binary` for file parts. The
`multipart` tag is accepted as an alias for `form`.

### Binary downloads

Document streamed files with the fluent registration API:

```go
r.Get("/report", swagger.Register(downloadReport).
    WithDownload("application/pdf", `attachment; filename="report.pdf"`).
    Handler())
```

This emits a `200` response with `type: string`, `format: binary`, the selected
media type, and a documented `Content-Disposition` header. For several media
types or custom headers, use `WithBinaryResponse(...)`,
`WithResponseMediaTypes(...)`, and `WithResponseHeader(name, description, example)`.

### Schema generation rules

| Go type | OpenAPI type |
|---|---|
| `string` | `string` |
| `int`, `int64` | `integer` (format: `int64`) |
| `int32` | `integer` (format: `int32`) |
| `float32` | `number` (format: `float`) |
| `float64` | `number` (format: `double`) |
| `bool` | `boolean` |
| `time.Time` | `string` (format: `date-time`) |
| `[]T` | `array` |
| `[]*T` | `array` with `$ref` items |
| `*T` | nullable `$ref` or primitive |
| `struct` | `$ref` → `components/schemas` |
| `map[K]V` | `object` with `additionalProperties` |

Recursive types (e.g. tree nodes that reference themselves) are handled correctly via `$ref`.

When a field pointing to a named struct carries per-field annotations (`example`, `description`, nullability from `*T`, …), the `$ref` is wrapped in `allOf` — OpenAPI 3.0 ignores sibling fields placed next to a bare `$ref`.

### Binder limitations

`BindRequest` covers strings, bools, ints, uints and floats (plus pointers to them). Not supported yet:

- slice parameters (`?tag=a&tag=b`) — only the first value is read;
- `time.Time` in query/path/header/cookie parameters (fine in JSON bodies);
- custom struct-shaped JSON representations need to match the declared JSON fields (apart from the built-in `time.Time` support).

An explicitly empty parameter (`?q=`) is different from an absent one: string fields receive an empty string, numeric fields fail conversion, and defaults apply only when the parameter is absent.

### Strict request contract

`BindRequest` rejects malformed or ambiguous input instead of silently repairing it:

- A non-empty JSON body requires `Content-Type: application/json` (media-type parameters such as `charset` are accepted). Multipart DTOs require `multipart/form-data`. A different or missing media type produces a binding error; `BindOr422` returns 422.
- A JSON body must contain exactly one object. Trailing values, trailing garbage and duplicate object keys are rejected.
- Only explicitly declared `json` fields are accepted, with the exact declared spelling. Exported Go names, case-insensitive aliases and fields belonging to query/path/header/cookie sources cannot be supplied through JSON. This validation also applies when a custom JSON codec is installed.
- Required fields are checked recursively in objects, arrays and maps. `null` is accepted for pointers and interfaces; non-pointer concrete fields require a non-null value. Use a pointer when null is part of the contract.
- Numeric parameters must fit the destination Go type; overflow, NaN and infinity are errors.
- A `-` binding tag excludes that source. Empty non-JSON binding names, multiple sources on one field, and DTOs mixing JSON with multipart body fields are configuration errors reported by `BindRequest`.

These rules intentionally reject some requests previously accepted by the binder. For mixed DTOs, keep body fields explicitly tagged and use `json:"-"` on external-source fields when the DTO is also used with a standalone JSON decoder.

### Full example DTO

```go
type FolderTreeRequest struct {
    CompanyID int64  `header:"X-Company-ID" required:"false" default:"0"   description:"Company scope"`
    RootID    *int64 `query:"root"          required:"false"                description:"Root folder ID, omit for full tree"`
    Depth     int    `query:"depth"         required:"false" default:"4"    description:"Max depth, 0 = unlimited"`
}

type FolderTreeNodeResponse struct {
    ID             int64                     `json:"id"`
    ParentFolderID *int64                    `json:"parent_folder_id"`
    Title          string                    `json:"title"`
    Children       []*FolderTreeNodeResponse `json:"children"` // recursive, generates $ref
}
```

## Security

Pass `swagger.Public` or `swagger.Bearer` as the `authType` argument to `RegisterRequestDTO`:

```go
// No auth required
swagger.RegisterRequestDTO(ctrl.List, dto.Req{}, dto.Resp{}, swagger.Public, "Tag")

// JWT bearer token required
swagger.RegisterRequestDTO(ctrl.Create, dto.Req{}, dto.Resp{}, swagger.Bearer, "Tag")
```

Bearer endpoints emit `security: [{bearerAuth: []}]` and a `401` response. The `bearerAuth` security scheme is included in `components/securitySchemes` automatically.

## Error responses

Every operation automatically gets `400`, `500` (and `401` for bearer endpoints) responses. To attach your API's error body to them, set `ErrorSchema` — no changes at registration call sites:

```go
type APIError struct {
    Code    string `json:"code"    example:"not_found"`
    Message string `json:"message"`
}

swagger.Config{
    Title:       "My API",
    Version:     "1.0.0",
    ErrorSchema: dto.APIError{},
}
```

The type is reflected like any response DTO and referenced via `$ref` from all auto-generated error responses.

### Per-endpoint error codes — fluent API

Not every endpoint returns a `409` or `404`. Declare endpoint-specific codes with the fluent `Register` builder — `.Handler()` returns the original handler, so chi and the pointer-based matching work exactly as before:

```go
r.Post("/", swagger.Register(ctrl.Create).
    WithRequest(dto.CreateRequest{}).
    WithResponse(dto.CreateResponse{}).
    WithSecurity(swagger.Bearer).
    WithTags("Quizzes").
    WithSummary("Create a quiz").
    WithError(http.StatusConflict, "Quiz already exists").      // uses Config.ErrorSchema
    WithError(http.StatusNotFound, "Course not found").
    WithErrorSchema(http.StatusGone, "Archived", dto.GoneError{}). // custom schema for this code
    Handler())
```

Declared codes are added to the operation's responses (overriding the auto-generated one for the same code). In the handler, write the error with the matching helper:

```go
if exists {
    swagger.WriteError(w, http.StatusConflict, dto.APIError{
        Code:    "already_exists",
        Message: "quiz already exists",
    })
    return
}
```

`RegisterRequestDTO(handler, req, resp, security, tags...)` remains supported — it is a positional shorthand for the same builder, so both styles can be mixed freely during migration.

## Validation errors — FastAPI-style 422

`BindRequest` collects **all** binding failures and returns them as a `*swagger.ValidationError`. The `BindOr422` helper turns that into an automatic `422 Unprocessable Entity` response:

```go
func (c *ArticlesController) Create(w http.ResponseWriter, r *http.Request) {
    var req dto.ArticleCreateRequest
    if !swagger.BindOr422(w, r, &req) {
        return // 422 with a JSON body has already been written
    }
    // req is fully populated here
}
```

The 422 body matches FastAPI's format:

```json
{
  "detail": [
    {"loc": ["query", "page"], "msg": "strconv.ParseInt: parsing \"abc\": invalid syntax", "type": "int_parsing"},
    {"loc": ["header", "X-Company-ID"], "msg": "...", "type": "int_parsing"}
  ]
}
```

Set `Document422: true` in the config to document the 422 response (with the `ValidationError` schema) for every operation that has a request DTO:

```go
swagger.Config{
    Title:       "My API",
    Version:     "1.0.0",
    ErrorSchema: dto.APIError{},
    Document422: true,
}
```

If you need custom handling, call `BindRequest` yourself and use `errors.As` to get the `*swagger.ValidationError`, or `swagger.WriteValidationError(w, err)` to write the 422 manually.

## Serving the spec

```go
cfg := swagger.Config{
    Title:   "My API",
    Version: "1.0.0",
}

// YAML
mux.Get("/openapi.yaml", swagger.ServeSpec(cfg))

// JSON
mux.Get("/openapi.json", swagger.ServeSpecJSON(cfg))
```

Or generate the bytes directly:

```go
yaml, err := swagger.GenerateOpenAPI(cfg)
json, err := swagger.GenerateOpenAPIJSON(cfg)
```

## Reverse proxy / API gateway prefix

If an upstream proxy (Caddy, nginx, etc.) routes to this service by a path prefix and strips it before forwarding, set `BasePath` so Swagger UI sends requests to the right URL:

```go
swagger.Config{
    Title:    "Folders Service",
    Version:  "1.0.0",
    BasePath: "/folders-service",
}
```

This produces:

```yaml
servers:
  - url: /folders-service
paths:
  /v1/folders:   # paths remain relative to the service root
    get: ...
```

Swagger UI concatenates `server.url + path` → `/folders-service/v1/folders`. The proxy strips `/folders-service` and forwards `/v1/folders` to the service. The service itself needs no changes.

For multiple environments use `Servers` directly (takes priority over `BasePath`):

```go
swagger.Config{
    Title:   "Folders Service",
    Version: "1.0.0",
    Servers: []swagger.Server{
        {URL: "https://api.prod.example.com/folders-service", Description: "Production"},
        {URL: "https://api.staging.example.com/folders-service", Description: "Staging"},
        {URL: "/folders-service", Description: "Local"},
    },
}
```

## Architecture

```
swagger/
├── swagger.go          public API: RegisterRequestDTO, BindRequest, SetRouter, GenerateOpenAPI
├── types/              SecurityType, EndpointMeta
├── registry/           stores metadata by handler pointer; resolves path+method via chi.Walk
├── binder/             BindRequest — fills structs from request by tag (json/query/path/header/cookie)
├── schema/             reflects Go types → OpenAPI Schema + Parameter objects
└── builder/            assembles OpenAPISpec from registry + schema generator
```

The only dependency is `gopkg.in/yaml.v3` for YAML serialisation.

## Notes

**Function pointer matching.** `RegisterRequestDTO` keys metadata by `reflect.ValueOf(handler).Pointer()`. This is the *code* pointer, and it has two consequences:

- Anonymous closures created from the same function literal share one pointer — avoid registering them, use named controller methods.
- Method values on **different instances of the same type** (`ctrlA.Create` and `ctrlB.Create`) also share one pointer. Registering both silently overwrites the first registration. If you run several instances of one controller type, give each route its own named handler function.

**Routers without Walk.** Any router works as long as you can enumerate its routes. With `http.ServeMux` keep a slice of `{method, pattern, handler}` and replay it in the walker — see [example/basic/main.go](example/basic/main.go) or [example/advanced/main.go](example/advanced/main.go).

**Call order.** `SetRouter` must be called after all routes are mounted. `GenerateOpenAPI` (and `ServeSpec`) can be called any number of times after that — the Walk result is cached and only re-computed when new handlers are registered.

**Thread safety.** The registry is protected by a `sync.RWMutex`. Safe to call from multiple goroutines.

## Swagger UI

No files to download, embed, or deploy. The UI is served directly from unpkg CDN.

### One-liner setup

```go
swagger.MountDocs(r, "/docs", swagger.Config{
    Title:   "My API",
    Version: "1.0.0",
}, swagger.UIConfig{})
```

Registers four routes automatically:

| Route | Response |
|---|---|
| `GET /docs` | 301 → `/docs/` |
| `GET /docs/` | Swagger UI HTML |
| `GET /docs/openapi.yaml` | OpenAPI spec (YAML) |
| `GET /docs/openapi.json` | OpenAPI spec (JSON) |

### Custom configuration

```go
swagger.MountDocs(r, "/docs",
    swagger.Config{
        Title:    "Folders Service",
        Version:  "1.0.0",
        BasePath: "/folders-service",
    },
    swagger.UIConfig{
        // Pin a specific swagger-ui version for reproducible builds.
        CDN: "https://unpkg.com/swagger-ui-dist@5.17.14",

        // Pre-expand all operations on load.
        DocExpansion: "full",

        // Enable "Try it out" for all operations by default.
        TryItOutEnabled: boolPtr(true),
    },
)
```

### Separate spec and UI paths

```go
mux.Get("/openapi.yaml", swagger.ServeSpec(cfg))
mux.Get("/docs",         swagger.ServeUI(swagger.UIConfig{
    SpecURL: "/openapi.yaml",
}))
```

### UIConfig reference

| Field | Type | Default | Description |
|---|---|---|---|
| `Title` | `string` | `"<spec title> – Swagger UI"` | Browser tab title |
| `SpecURL` | `string` | `"./openapi.yaml"` | URL of the OpenAPI spec |
| `CDN` | `string` | `unpkg.com/swagger-ui-dist@5.17.14` | CDN base URL for assets |
| `DeepLinking` | `*bool` | `true` | Deep links for tags and operations |
| `DefaultModelsExpandDepth` | `*int` | `1` | Schema expansion depth (`-1` hides Models section) |
| `DocExpansion` | `string` | `"list"` | `"list"` / `"full"` / `"none"` |
| `Filter` | `*bool` | `true` | Show search/filter bar |
| `PersistAuthorization` | `*bool` | `true` | Keep auth tokens across browser refreshes |
| `TryItOutEnabled` | `*bool` | `false` | Pre-enable "Try it out" for all operations |

## Automatic MCP server

The optional [MCP adapter](mcp/README.md) builds tools at startup from the same
registered routes and DTOs. It serves Streamable HTTP on a separate listener;
the example uses API port `8080` and MCP port `8081`:

```sh
cd mcp
go run ./example
```

Requires Go 1.25+ only for the adapter; the core library remains Go 1.22.
GET/HEAD tools are enabled by default. Write operations require an explicit
filter; API credentials and tenant headers are supplied server-side. See the
adapter documentation for integration, limitations, and business risks.
