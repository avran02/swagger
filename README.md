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
go get github.com/your-org/goswagger
```

Requires Go 1.22+. No external dependencies beyond chi itself.

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
| `default:"value"` | Sets the default value in the schema |
| `example:"value"` | Sets the example value |
| `description:"text"` | Sets the field description |
| `enum:"a,b,c"` | Restricts allowed values |
| `format:"uuid"` | Overrides the schema format |

Pointer fields (`*T`) are always optional and nullable — no tag needed.

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
└── builder/            assembles OpenAPISpec from registry + schema generator; custom YAML serialiser
```

Zero non-stdlib dependencies. The YAML serialiser is built in — no `gopkg.in/yaml.v3` required.

## Notes

**Function pointer matching.** `RegisterRequestDTO` keys metadata by `reflect.ValueOf(handler).Pointer()`. This works correctly for named functions and methods on structs (the typical case). Avoid registering anonymous closures that capture different variables under the same pointer — use named controller methods instead.

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