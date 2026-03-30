package registry

import (
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/avran02/swagger/types"
)

// WalkFunc matches the signature of chi.Walk exactly:
//
//	chi.Walk(router, func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error { ... })
//
// Inject once at startup:
//
//	registry.Global.SetWalkFunc(func(fn registry.WalkFunc) error {
//	    return chi.Walk(chiMux, chi.WalkFunc(fn))
//	})
type WalkFunc func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error

// Walker is a function that accepts a WalkFunc and calls it for every route.
// This is exactly what chi.Walk does when you pass it a mux.
type Walker func(walkFn WalkFunc) error

// Registry stores endpoint metadata keyed by handler function pointer.
// Path and Method are resolved lazily via chi.Walk when the spec is generated.
type Registry struct {
	mu       sync.RWMutex
	byPtr    map[uintptr]*types.EndpointMeta // handler ptr → metadata (path/method not yet set)
	resolved []types.EndpointMeta            // filled after Walk
	walker   Walker
}

// Global is the default singleton registry.
var Global = &Registry{
	byPtr: make(map[uintptr]*types.EndpointMeta),
}

// NewRegistry creates an isolated registry — useful for tests.
func NewRegistry() *Registry {
	return &Registry{byPtr: make(map[uintptr]*types.EndpointMeta)}
}

// SetWalker injects the chi.Walk function. Call once after all routes are registered:
//
//	registry.Global.SetWalker(func(fn registry.WalkFunc) error {
//	    return chi.Walk(chiMux, chi.WalkFunc(fn))
//	})
func (r *Registry) SetWalker(w Walker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.walker = w
	r.resolved = nil // invalidate cache so next Endpoints() call re-walks
}

// RegisterRequestDTO records handler metadata indexed by the handler's function
// pointer. Path and Method are unknown at this point — they are resolved later
// by walking the chi router tree.
func (r *Registry) RegisterRequestDTO(
	handler http.HandlerFunc,
	req any,
	resp any,
	authType types.SecurityType,
	tags []string,
) http.HandlerFunc {
	ptr := funcPtr(handler)
	meta := &types.EndpointMeta{
		HandlerPtr:  ptr,
		RequestDTO:  req,
		ResponseDTO: resp,
		Security:    authType,
		Tags:        tags,
	}
	r.mu.Lock()
	r.byPtr[ptr] = meta
	r.resolved = nil // invalidate
	r.mu.Unlock()
	return handler
}

// Endpoints resolves path+method for every registered handler by walking the
// chi router, then returns the complete snapshot.
// Safe to call multiple times — result is cached until SetWalker or a new
// RegisterRequestDTO call invalidates it.
func (r *Registry) Endpoints() ([]types.EndpointMeta, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.resolved != nil {
		return r.resolved, nil
	}

	if r.walker == nil {
		// No walker set yet — return what we have without path/method.
		// Useful in unit tests that register endpoints without a real chi router.
		out := make([]types.EndpointMeta, 0, len(r.byPtr))
		for _, m := range r.byPtr {
			out = append(out, *m)
		}
		return out, nil
	}

	// Walk the chi router to resolve path + method for each registered handler.
	matched := make(map[uintptr]bool)
	var walkErr error

	walkErr = r.walker(func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		ptr := handlerPtr(handler)
		if meta, ok := r.byPtr[ptr]; ok {
			// Clone and fill in path + method.
			resolved := *meta
			resolved.Path = route
			resolved.Method = method
			r.resolved = append(r.resolved, resolved)
			matched[ptr] = true
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("swagger: chi.Walk failed: %w", walkErr)
	}

	// Include any registered endpoints that weren't found during Walk
	// (e.g. registered but not yet mounted, or Walk not reflecting them).
	for ptr, meta := range r.byPtr {
		if !matched[ptr] {
			r.resolved = append(r.resolved, *meta)
		}
	}

	return r.resolved, nil
}

// Reset clears all registered metadata. Useful in tests.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPtr = make(map[uintptr]*types.EndpointMeta)
	r.resolved = nil
	r.walker = nil
}

// funcPtr returns the function pointer for an http.HandlerFunc.
func funcPtr(fn http.HandlerFunc) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// handlerPtr extracts the function pointer from an http.Handler.
// chi wraps HandlerFunc in its own type; reflect sees through it.
func handlerPtr(h http.Handler) uintptr {
	if fn, ok := h.(http.HandlerFunc); ok {
		return reflect.ValueOf(fn).Pointer()
	}
	return reflect.ValueOf(h).Pointer()
}
