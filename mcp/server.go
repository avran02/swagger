// Package swaggermcp generates MCP tools from swagger's runtime OpenAPI model.
package swaggermcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/schema"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config is evaluated once at startup. Rebuild the handler after changing routes.
type Config struct {
	// BaseURL is the trusted API origin, including any gateway prefix.
	BaseURL  string
	Registry *registry.Registry // nil uses registry.Global
	Spec     builder.Config
	// Include selects operations. nil exposes only GET and HEAD.
	Include func(method, path string, op *builder.Operation) bool
	// Authorize supplies API credentials from trusted server-side context.
	// It must validate caller identity/tenant before granting API access.
	// MCP Authorization headers are deliberately not forwarded automatically.
	Authorize        func(context.Context, *mcp.CallToolRequest, *http.Request) error
	Client           *http.Client
	MaxResponseBytes int64 // default 1 MiB
}

// NewHandler builds a stateless Streamable HTTP MCP endpoint. Mount at /mcp.
// Protect remote deployments with authentication, Origin/Host validation and TLS.
func NewHandler(cfg Config) (http.Handler, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("MCP BaseURL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	reg := cfg.Registry
	if reg == nil {
		reg = registry.Global
	}
	spec, err := builder.Build(reg, cfg.Spec)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 30 * time.Second}
	if cfg.Client != nil {
		client = *cfg.Client
	}
	// Never follow redirects: they can change the destination of credentials or writes.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	if cfg.MaxResponseBytes < 0 || cfg.MaxResponseBytes > 1<<30 {
		return nil, fmt.Errorf("MaxResponseBytes must be between 1 and 1 GiB")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "swagger-mcp", Version: "1.0.0"}, nil)
	for _, path := range builder.SortedPaths(spec.Paths) {
		item := spec.Paths[path]
		for _, entry := range []struct {
			method string
			op     *builder.Operation
		}{
			{"GET", item.Get}, {"HEAD", item.Head}, {"POST", item.Post}, {"PUT", item.Put}, {"PATCH", item.Patch}, {"DELETE", item.Delete}, {"OPTIONS", item.Options},
		} {
			method, op := entry.method, entry.op
			if op == nil {
				continue
			}
			include := method == "GET" || method == "HEAD"
			if cfg.Include != nil {
				include = cfg.Include(method, path, op)
			}
			if !include {
				continue
			}
			input, err := inputSchema(op, spec.Components.Schemas)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			data, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			var js jsonschema.Schema
			if err := json.Unmarshal(data, &js); err != nil {
				return nil, err
			}
			resolved, err := js.Resolve(nil)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			description := op.Summary
			if description == "" {
				description = method + " " + path
			}
			readOnly := method == "GET" || method == "HEAD"
			server.AddTool(&mcp.Tool{Name: toolName(method, path), Description: description, InputSchema: input,
				Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}},
				func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					var args map[string]any
					raw := call.Params.Arguments
					if len(raw) == 0 {
						raw = json.RawMessage(`{}`)
					}
					decoder := json.NewDecoder(bytes.NewReader(raw))
					decoder.UseNumber()
					if err := decoder.Decode(&args); err != nil {
						return failure("invalid arguments"), nil
					}
					var validationArgs any
					if err := json.Unmarshal(raw, &validationArgs); err != nil {
						return failure("invalid arguments"), nil
					}
					if err := resolved.Validate(validationArgs); err != nil {
						return failure("invalid arguments: " + err.Error()), nil
					}
					request, err := makeRequest(ctx, base, method, path, op, args)
					if err != nil {
						return failure(err.Error()), nil
					}
					if cfg.Authorize != nil {
						if err := cfg.Authorize(ctx, call, request); err != nil {
							return failure("API authorization denied"), nil
						}
					} else if len(op.Security) > 0 {
						return failure("API authorization is not configured"), nil
					}
					response, err := client.Do(request)
					if err != nil {
						return failure("API request failed"), nil
					}
					defer response.Body.Close()
					body, err := io.ReadAll(io.LimitReader(response.Body, cfg.MaxResponseBytes+1))
					if err != nil {
						return failure("API response could not be read"), nil
					}
					if int64(len(body)) > cfg.MaxResponseBytes {
						return failure("API response exceeds configured limit; narrow the request"), nil
					}
					return &mcp.CallToolResult{IsError: response.StatusCode < 200 || response.StatusCode >= 300,
						Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("HTTP %d\n%s", response.StatusCode, body)}}}, nil
				})
		}
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}), nil
}

func failure(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

var nonName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func toolName(method, path string) string {
	name := strings.ToLower(method) + "_" + strings.Trim(nonName.ReplaceAllString(path, "_"), "_")
	if len(name) > 47 {
		name = name[:47]
	}
	hash := sha256.Sum256([]byte(method + " " + path))
	return fmt.Sprintf("%s_%x", name, hash[:8])
}

func object() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}
func add(obj map[string]any, name string, value any, required bool) {
	obj["properties"].(map[string]any)[name] = value
	if required {
		names, _ := obj["required"].([]string)
		obj["required"] = append(names, name)
	}
}
func inputSchema(op *builder.Operation, components map[string]*schema.Schema) (map[string]any, error) {
	root := object()
	groups := map[string]map[string]any{}
	for _, p := range op.Parameters {
		if p.In == "header" || p.In == "cookie" {
			// These sources carry identity and tenant scope; only Authorize may supply them.
			continue
		}
		if p.In != "path" && p.In != "query" {
			return nil, fmt.Errorf("unsupported parameter source %q", p.In)
		}
		if p.Schema == nil || (p.Schema.Type != "string" && p.Schema.Type != "integer" && p.Schema.Type != "number" && p.Schema.Type != "boolean") {
			return nil, fmt.Errorf("parameter %s requires a scalar schema", p.Name)
		}
		group := groups[p.In]
		if group == nil {
			group = object()
			groups[p.In] = group
		}
		s := convertSchema(p.Schema)
		if p.Description != "" {
			s["description"] = p.Description
		}
		add(group, p.Name, s, p.Required)
	}
	for location, group := range groups {
		_, required := group["required"]
		add(root, location, group, required)
	}
	if op.RequestBody != nil {
		media := op.RequestBody.Content["application/json"]
		if media == nil || media.Schema == nil {
			return nil, fmt.Errorf("only JSON request bodies are supported; exclude this operation with Include")
		}
		add(root, "body", convertSchema(media.Schema), op.RequestBody.Required)
	}
	defs := map[string]any{}
	for name, s := range components {
		defs[name] = convertSchema(s)
	}
	root["$defs"] = defs
	return root, nil
}

// Convert OpenAPI 3.0 references and nullable into standalone JSON Schema.
func convertSchema(s *schema.Schema) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	data, _ := json.Marshal(s)
	result := map[string]any{}
	_ = json.Unmarshal(data, &result)
	delete(result, "nullable")
	// DTO defaults are encoded as tag strings, not necessarily the schema's type.
	// Binding remains responsible for defaults; don't publish invalid JSON Schema defaults.
	delete(result, "default")
	if s.Ref != "" {
		result["$ref"] = strings.Replace(s.Ref, "#/components/schemas/", "#/$defs/", 1)
	}
	if s.Properties != nil {
		props := map[string]any{}
		for name, child := range s.Properties {
			props[name] = convertSchema(child)
		}
		result["properties"] = props
	}
	if s.Items != nil {
		result["items"] = convertSchema(s.Items)
	}
	if s.AdditionalProperties != nil {
		result["additionalProperties"] = convertSchema(s.AdditionalProperties)
	}
	if len(s.AllOf) > 0 {
		list := []any{}
		for _, child := range s.AllOf {
			list = append(list, convertSchema(child))
		}
		result["allOf"] = list
	}
	if s.Nullable {
		return map[string]any{"anyOf": []any{result, map[string]any{"type": "null"}}}
	}
	return result
}

func makeRequest(ctx context.Context, base *url.URL, method, path string, op *builder.Operation, args map[string]any) (*http.Request, error) {
	query := url.Values{}
	for _, p := range op.Parameters {
		group, _ := args[p.In].(map[string]any)
		value, exists := group[p.Name]
		if !exists {
			continue
		}
		text := fmt.Sprint(value)
		switch p.In {
		case "path":
			// Reject path traversal and separator injection across API/proxy routers.
			if text == "" || text == "." || text == ".." || strings.ContainsAny(text, "/\\%") {
				return nil, fmt.Errorf("invalid path parameter %s", p.Name)
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(text))
		case "query":
			query.Set(p.Name, text)
		}
	}
	if strings.ContainsAny(path, "{}*") {
		return nil, fmt.Errorf("route contains unresolved path parameters")
	}
	target := strings.TrimRight(base.String(), "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if value, ok := args["body"]; ok {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}
