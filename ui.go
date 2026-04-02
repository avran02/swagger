package swagger

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// UIConfig controls the Swagger UI appearance and behaviour.
// All fields are optional — the defaults produce a working UI out of the box.
type UIConfig struct {
	// Title is the browser tab title. Defaults to the spec's Info.Title + " – Swagger UI".
	Title string

	// SpecURL is the URL the UI will fetch the OpenAPI spec from.
	// Defaults to "./openapi.yaml" (relative to the UI mount point).
	SpecURL string

	// CDN is the base URL for swagger-ui assets.
	// Defaults to unpkg (https://unpkg.com/swagger-ui-dist@latest).
	// Pin a specific version for reproducible builds:
	//   CDN: "https://unpkg.com/swagger-ui-dist@5.17.14"
	CDN string

	// DeepLinking enables deep linking for tags and operations.
	// Default: true.
	DeepLinking *bool

	// DefaultModelsExpandDepth controls schema expansion in the UI.
	//   1  = expand top-level only (default)
	//   0  = collapse all
	//  -1  = hide Models section entirely
	DefaultModelsExpandDepth *int

	// DocExpansion controls how operations are displayed on load.
	//   "list"  = shows all operations collapsed (default)
	//   "full"  = shows all operations expanded
	//   "none"  = collapses everything
	DocExpansion string

	// Filter enables the search/filter bar. Default: true.
	Filter *bool

	// PersistAuthorization keeps auth tokens across browser refreshes. Default: true.
	PersistAuthorization *bool

	// TryItOutEnabled pre-enables "Try it out" for all operations. Default: false.
	TryItOutEnabled *bool
}

const defaultCDN = "https://unpkg.com/swagger-ui-dist@5.17.14"

// ServeUI returns an http.HandlerFunc that serves the Swagger UI.
// Assets are loaded from unpkg CDN — no files to embed or deploy.
//
// Mount it at whatever path you like:
//
//	mux.Get("/docs", swagger.ServeUI(swagger.UIConfig{}))
//	mux.Get("/docs", swagger.ServeUI(swagger.UIConfig{
//	    Title:   "My API Docs",
//	    SpecURL: "/openapi.yaml",
//	}))
//
// The UI and the spec can be served from separate paths:
//
//	mux.Get("/docs/openapi.yaml", swagger.ServeSpec(cfg))
//	mux.Get("/docs",              swagger.ServeUI(swagger.UIConfig{SpecURL: "/docs/openapi.yaml"}))
func ServeUI(uiCfg UIConfig) http.HandlerFunc {
	// Apply defaults once at construction time — not per request.
	applyUIDefaults(&uiCfg)
	html := renderUIHTML(uiCfg)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, html) //nolint:errcheck
	}
}

// MountDocs registers both the spec endpoint and the Swagger UI under a common
// prefix using a chi-compatible router. This is the one-liner DX:
//
//	swagger.MountDocs(r, "/docs", cfg, swagger.UIConfig{})
//
// Registers:
//
//	GET /docs           → Swagger UI  (redirects /docs → /docs/)
//	GET /docs/          → Swagger UI
//	GET /docs/openapi.yaml → OpenAPI YAML spec
//	GET /docs/openapi.json → OpenAPI JSON spec
//
// uiCfg.SpecURL defaults to "./openapi.yaml" so the UI finds the spec automatically.
func MountDocs(r interface {
	Get(pattern string, handlerFn http.HandlerFunc)
	Handle(pattern string, handler http.Handler)
}, prefix string, specCfg Config, uiCfg UIConfig) {
	prefix = strings.TrimRight(prefix, "/")

	if uiCfg.SpecURL == "" {
		uiCfg.SpecURL = "./openapi.yaml"
	}
	if uiCfg.Title == "" && specCfg.Title != "" {
		uiCfg.Title = specCfg.Title + " – Swagger UI"
	}

	uiHandler := ServeUI(uiCfg)

	r.Get(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
	})
	r.Get(prefix+"/", uiHandler)
	r.Get(prefix+"/openapi.yaml", ServeSpec(specCfg))
	r.Get(prefix+"/openapi.json", ServeSpecJSON(specCfg))
}

// ── internals ────────────────────────────────────────────────────────────────

func applyUIDefaults(c *UIConfig) {
	if c.CDN == "" {
		c.CDN = defaultCDN
	}
	if c.SpecURL == "" {
		c.SpecURL = "./openapi.yaml"
	}
	if c.DeepLinking == nil {
		c.DeepLinking = boolPtr(true)
	}
	if c.DefaultModelsExpandDepth == nil {
		c.DefaultModelsExpandDepth = intPtr(1)
	}
	if c.DocExpansion == "" {
		c.DocExpansion = "list"
	}
	if c.Filter == nil {
		c.Filter = boolPtr(true)
	}
	if c.PersistAuthorization == nil {
		c.PersistAuthorization = boolPtr(true)
	}
	if c.TryItOutEnabled == nil {
		c.TryItOutEnabled = boolPtr(false)
	}
}

var uiTemplate = template.Must(template.New("swagger-ui").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="{{.CDN}}/swagger-ui.css">
  <style>
    html { box-sizing: border-box; overflow-y: scroll; }
    *, *:before, *:after { box-sizing: inherit; }
    body { margin: 0; padding: 0; }
  </style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="{{.CDN}}/swagger-ui-bundle.js" crossorigin></script>
<script src="{{.CDN}}/swagger-ui-standalone-preset.js" crossorigin></script>
<script>
window.onload = function() {
  window.ui = SwaggerUIBundle({
    url:          {{.SpecURLJSON}},
    dom_id:       '#swagger-ui',
    deepLinking:  {{.DeepLinkingJSON}},
    filter:       {{.FilterJSON}},
    tryItOutEnabled:          {{.TryItOutEnabledJSON}},
    persistAuthorization:     {{.PersistAuthorizationJSON}},
    defaultModelsExpandDepth: {{.DefaultModelsExpandDepthJSON}},
    docExpansion: {{.DocExpansionJSON}},
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });
};
</script>
</body>
</html>
`))

// uiTemplateData holds pre-serialised JSON values for the template.
type uiTemplateData struct {
	Title                        string
	CDN                          string
	SpecURLJSON                  template.JS
	DeepLinkingJSON              template.JS
	FilterJSON                   template.JS
	TryItOutEnabledJSON          template.JS
	PersistAuthorizationJSON     template.JS
	DefaultModelsExpandDepthJSON template.JS
	DocExpansionJSON             template.JS
}

func renderUIHTML(c UIConfig) string {
	title := c.Title
	if title == "" {
		title = "API – Swagger UI"
	}

	data := uiTemplateData{
		Title:                        title,
		CDN:                          c.CDN,
		SpecURLJSON:                  template.JS(jsonString(c.SpecURL)),
		DeepLinkingJSON:              template.JS(jsonBool(*c.DeepLinking)),
		FilterJSON:                   template.JS(jsonBool(*c.Filter)),
		TryItOutEnabledJSON:          template.JS(jsonBool(*c.TryItOutEnabled)),
		PersistAuthorizationJSON:     template.JS(jsonBool(*c.PersistAuthorization)),
		DefaultModelsExpandDepthJSON: template.JS(fmt.Sprintf("%d", *c.DefaultModelsExpandDepth)),
		DocExpansionJSON:             template.JS(jsonString(c.DocExpansion)),
	}

	var sb strings.Builder
	if err := uiTemplate.Execute(&sb, data); err != nil {
		// Template is static and validated at init — this should never happen.
		panic("swagger: UI template execution failed: " + err.Error())
	}
	return sb.String()
}

func jsonString(s string) string {
	// Minimal JSON string encoding — only characters that appear in URLs/keywords.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func jsonBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }
