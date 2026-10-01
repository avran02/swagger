package swaggermcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testRequest struct {
	ID     string `path:"id"`
	Count  int    `query:"count"`
	Tenant string `header:"X-Tenant"`
	Title  string `json:"title"`
}

func TestRoundTrip(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/prefix/items/a b" || r.URL.Query().Get("count") != "2" || r.Header.Get("X-Tenant") != "trusted" {
			t.Errorf("unexpected request: %s %v", r.URL, r.Header)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing content type")
		}
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"exists"}`)
	}))
	defer api.Close()
	reg := registry.NewRegistry()
	handler := reg.RegisterRequestDTO(func(http.ResponseWriter, *http.Request) {}, testRequest{}, nil, types.Bearer, nil)
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("POST", "/items/{id}", handler) })
	h, err := NewHandler(Config{Registry: reg, BaseURL: api.URL + "/prefix", Include: func(string, string, *builder.Operation) bool { return true }, Authorize: func(_ context.Context, _ *mcp.CallToolRequest, r *http.Request) error {
		r.Header.Set("X-Tenant", "trusted")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(h)
	defer endpoint.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("tools: %v %v", list, err)
	}
	name := list.Tools[0].Name
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"path": map[string]any{"id": "a b"}, "query": map[string]any{"count": 2}, "body": map[string]any{"title": "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "HTTP 409") || calls != 1 {
		t.Fatalf("result=%+v text=%s calls=%d", result, result.Content[0].(*mcp.TextContent).Text, calls)
	}
	for _, args := range []map[string]any{
		{"path": map[string]any{"id": "ok"}, "query": map[string]any{"count": "bad"}, "body": map[string]any{"title": "hello"}},
		{"path": map[string]any{"id": ".."}, "query": map[string]any{"count": 2}, "body": map[string]any{"title": "hello"}},
		{"path": map[string]any{"id": "ok"}, "query": map[string]any{"count": 2}, "body": map[string]any{"title": "hello"}, "header": map[string]any{"X-Tenant": "other"}},
	} {
		result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !result.IsError {
			t.Fatalf("invalid arguments accepted: %+v %v", result, err)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid calls reached API: %d", calls)
	}
}

func TestDefaultsAndLimits(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		security types.SecurityType
		want     string
		calls    int
	}{
		{"limit", 200, "123456", types.Public, "exceeds", 1},
		{"auth", 200, "ok", types.Bearer, "authorization", 0},
		{"redirect", 302, "go", types.Public, "HTTP 302", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/other")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer api.Close()
			reg := registry.NewRegistry()
			get := reg.RegisterRequestDTO(func(http.ResponseWriter, *http.Request) {}, nil, nil, test.security, nil)
			post := reg.RegisterRequestDTO(func(http.ResponseWriter, *http.Request) {}, nil, nil, types.Public, nil)
			reg.SetWalker(func(fn registry.WalkFunc) error {
				if err := fn("GET", "/items", get); err != nil {
					return err
				}
				return fn("POST", "/items", post)
			})
			h, err := NewHandler(Config{BaseURL: api.URL, Registry: reg, MaxResponseBytes: 4})
			if err != nil {
				t.Fatal(err)
			}
			endpoint := httptest.NewServer(h)
			defer endpoint.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			list, err := session.ListTools(context.Background(), nil)
			if err != nil || len(list.Tools) != 1 {
				t.Fatalf("%v %v", list, err)
			}
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: list.Tools[0].Name, Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, test.want) || calls != test.calls {
				t.Fatalf("%+v calls=%d", result, calls)
			}
		})
	}
}
