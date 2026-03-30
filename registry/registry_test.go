package registry_test

import (
	"net/http"
	"testing"

	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
)

func TestRegistry_WalkResolvesPathAndMethod(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, struct {
		Name string `json:"name"`
	}{}, nil, types.Public, []string{"Test"})

	reg.SetWalker(func(fn registry.WalkFunc) error {
		return fn("POST", "/articles", h)
	})

	endpoints, err := reg.Endpoints()
	if err != nil {
		t.Fatalf("Endpoints() error: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(endpoints))
	}
	ep := endpoints[0]
	if ep.Path != "/articles" {
		t.Errorf("want Path=/articles, got %q", ep.Path)
	}
	if ep.Method != "POST" {
		t.Errorf("want Method=POST, got %q", ep.Method)
	}
}

func TestRegistry_MultipleHandlers(t *testing.T) {
	createH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	listH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	getH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(createH, nil, nil, types.Public, []string{"A"})
	reg.RegisterRequestDTO(listH, nil, nil, types.Bearer, []string{"A"})
	reg.RegisterRequestDTO(getH, nil, nil, types.Bearer, []string{"A"})

	reg.SetWalker(func(fn registry.WalkFunc) error {
		routes := []struct {
			m, p string
			h    http.HandlerFunc
		}{
			{"POST", "/items", createH},
			{"GET", "/items", listH},
			{"GET", "/items/{id}", getH},
		}
		for _, r := range routes {
			if err := fn(r.m, r.p, r.h); err != nil {
				return err
			}
		}
		return nil
	})

	endpoints, err := reg.Endpoints()
	if err != nil {
		t.Fatalf("Endpoints() error: %v", err)
	}
	if len(endpoints) != 3 {
		t.Fatalf("want 3 endpoints, got %d", len(endpoints))
	}

	byKey := make(map[string]types.EndpointMeta)
	for _, ep := range endpoints {
		byKey[ep.Method+" "+ep.Path] = ep
	}
	for _, key := range []string{"POST /items", "GET /items", "GET /items/{id}"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("missing endpoint %q", key)
		}
	}
}

func TestRegistry_WalkerNotSet_ReturnsWithoutPathMethod(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, nil, types.Public, []string{"X"})

	// No walker set — should still return endpoints, just without path/method.
	endpoints, err := reg.Endpoints()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Path != "" || endpoints[0].Method != "" {
		t.Error("without walker, path and method should be empty")
	}
}

func TestRegistry_UnmatchedWalkRoute_NotIncluded(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	unrelatedH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, nil, types.Public, []string{"X"})

	// Walker reports an unrelated handler — should not appear in endpoints.
	reg.SetWalker(func(fn registry.WalkFunc) error {
		fn("GET", "/unrelated", unrelatedH) //nolint
		fn("POST", "/matched", h)           //nolint
		return nil
	})

	endpoints, err := reg.Endpoints()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ep := range endpoints {
		if ep.Path == "/unrelated" {
			t.Error("unregistered handler should not appear in endpoints")
		}
	}

	found := false
	for _, ep := range endpoints {
		if ep.Path == "/matched" {
			found = true
		}
	}
	if !found {
		t.Error("registered handler should appear with matched path")
	}
}

func TestRegistry_CacheInvalidatedOnNewRegister(t *testing.T) {
	h1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h1, nil, nil, types.Public, []string{"X"})

	callCount := 0
	reg.SetWalker(func(fn registry.WalkFunc) error {
		callCount++
		fn("GET", "/a", h1) //nolint
		fn("GET", "/b", h2) //nolint
		return nil
	})

	reg.Endpoints() //nolint — first call, populates cache
	reg.Endpoints() //nolint — second call, should use cache

	if callCount != 1 {
		t.Errorf("walker should be called once (cache hit), got %d calls", callCount)
	}

	// Registering a new handler must invalidate the cache.
	reg.RegisterRequestDTO(h2, nil, nil, types.Bearer, []string{"Y"})
	reg.Endpoints() //nolint — should re-walk

	if callCount != 2 {
		t.Errorf("new registration must invalidate cache; want 2 walker calls, got %d", callCount)
	}
}

func TestRegistry_SetWalkerInvalidatesCache(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, nil, types.Public, []string{"X"})

	callCount := 0
	makeWalker := func() registry.Walker {
		return func(fn registry.WalkFunc) error {
			callCount++
			return fn("GET", "/x", h)
		}
	}

	reg.SetWalker(makeWalker())
	reg.Endpoints() //nolint — cache populated

	// Replacing walker must invalidate.
	reg.SetWalker(makeWalker())
	reg.Endpoints() //nolint

	if callCount != 2 {
		t.Errorf("SetWalker must invalidate cache; want 2 calls, got %d", callCount)
	}
}

func TestRegistry_Reset(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, nil, nil, types.Public, []string{"X"})
	reg.Reset()

	endpoints, err := reg.Endpoints()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(endpoints) != 0 {
		t.Errorf("after Reset, expected 0 endpoints, got %d", len(endpoints))
	}
}
