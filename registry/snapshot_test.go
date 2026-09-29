package registry_test

import (
	"errors"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
	"net/http"
	"testing"
)

type valueHandler struct{}

func (valueHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestRegistry_FailedWalkDoesNotPublishSnapshot(t *testing.T) {
	reg := registry.NewRegistry()
	h := reg.RegisterMeta(func(http.ResponseWriter, *http.Request) {}, types.EndpointMeta{})
	calls := 0
	failure := errors.New("walk failed")
	reg.SetWalker(func(fn registry.WalkFunc) error {
		calls++
		if err := fn("GET", "/first", h); err != nil {
			return err
		}
		if calls == 1 {
			return failure
		}
		return fn("GET", "/second", h)
	})
	if _, err := reg.Endpoints(); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
	eps, err := reg.Endpoints()
	if err != nil || len(eps) != 2 || calls != 2 {
		t.Fatalf("calls=%d eps=%+v err=%v", calls, eps, err)
	}
}

func TestRegistry_SnapshotsAreIndependent(t *testing.T) {
	reg := registry.NewRegistry()
	meta := types.EndpointMeta{Tags: []string{"original"}, ResponseHeaders: map[string]types.ResponseHeaderSpec{"X-Test": {Description: "original"}}}
	h := reg.RegisterMeta(func(http.ResponseWriter, *http.Request) {}, meta)
	meta.Tags[0] = "changed before walk"
	meta.ResponseHeaders["X-Test"] = types.ResponseHeaderSpec{Description: "changed"}
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("GET", "/original", h) })
	eps, _ := reg.Endpoints()
	if eps[0].Tags[0] != "original" || eps[0].ResponseHeaders["X-Test"].Description != "original" {
		t.Fatal("registration aliases caller metadata")
	}
	eps[0].Path = "/changed"
	eps[0].Tags[0] = "changed"
	delete(eps[0].ResponseHeaders, "X-Test")
	again, _ := reg.Endpoints()
	if again[0].Path != "/original" || again[0].Tags[0] != "original" || len(again[0].ResponseHeaders) != 1 {
		t.Fatalf("snapshot leaked: %+v", again)
	}
}

func TestRegistry_SkipsUnregisteredValueHandler(t *testing.T) {
	reg := registry.NewRegistry()
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("GET", "/unregistered", valueHandler{}) })
	eps, err := reg.Endpoints()
	if err != nil || len(eps) != 0 {
		t.Fatalf("eps=%+v err=%v", eps, err)
	}
}
