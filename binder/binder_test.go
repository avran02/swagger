package binder_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avran02/swagger/binder"
)

type bindTarget struct {
	Name   string   `json:"name"`
	Page   int      `query:"page"  default:"1"`
	Depth  int      `query:"depth"`
	Force  bool     `query:"force"`
	Rating *float64 `query:"rating"`
	Token  string   `header:"X-Token"`
	Sess   string   `cookie:"session"`
	ID     int64    `path:"id"`
}

func TestBindRequest_QueryHeaderCookie(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/items?depth=3&force=true&rating=4.5", nil)
	r.Header.Set("X-Token", "tok-123")
	r.AddCookie(&http.Cookie{Name: "session", Value: "sess-1"})

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatalf("BindRequest: %v", err)
	}

	if dst.Depth != 3 {
		t.Errorf("Depth = %d, want 3", dst.Depth)
	}
	if !dst.Force {
		t.Error("Force = false, want true")
	}
	if dst.Rating == nil || *dst.Rating != 4.5 {
		t.Errorf("Rating = %v, want 4.5", dst.Rating)
	}
	if dst.Token != "tok-123" {
		t.Errorf("Token = %q, want tok-123", dst.Token)
	}
	if dst.Sess != "sess-1" {
		t.Errorf("Sess = %q, want sess-1", dst.Sess)
	}
}

func TestBindRequest_DefaultApplied(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/items", nil)

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatalf("BindRequest: %v", err)
	}
	if dst.Page != 1 {
		t.Errorf("Page = %d, want default 1", dst.Page)
	}
	// No default and no value — field stays zero, pointer stays nil.
	if dst.Depth != 0 {
		t.Errorf("Depth = %d, want 0", dst.Depth)
	}
	if dst.Rating != nil {
		t.Errorf("Rating = %v, want nil", dst.Rating)
	}
}

func TestBindRequest_ValueOverridesDefault(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/items?page=7", nil)

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatalf("BindRequest: %v", err)
	}
	if dst.Page != 7 {
		t.Errorf("Page = %d, want 7", dst.Page)
	}
}

func TestBindRequest_PathParam(t *testing.T) {
	// Not parallel: SetURLParamFunc mutates package-level state.
	binder.SetURLParamFunc(func(r *http.Request, key string) string {
		if key == "id" {
			return "42"
		}
		return ""
	})
	defer binder.SetURLParamFunc(nil)

	r := httptest.NewRequest(http.MethodGet, "/items/42", nil)

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatalf("BindRequest: %v", err)
	}
	if dst.ID != 42 {
		t.Errorf("ID = %d, want 42", dst.ID)
	}
}

func TestBindRequest_JSONBody(t *testing.T) {
	t.Parallel()

	body := strings.NewReader(`{"name":"Alice"}`)
	r := httptest.NewRequest(http.MethodPost, "/items", body)
	r.Header.Set("Content-Type", "application/json")

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatalf("BindRequest: %v", err)
	}
	if dst.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", dst.Name)
	}
}

func TestBindRequest_InvalidInt(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/items?depth=abc", nil)

	var dst bindTarget
	if err := binder.BindRequest(r, &dst); err == nil {
		t.Fatal("expected error for non-numeric depth, got nil")
	}
}

func TestBindRequest_NilAndNonPointer(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/items", nil)

	if err := binder.BindRequest(r, nil); err == nil {
		t.Error("expected error for nil dst")
	}
	var dst bindTarget
	if err := binder.BindRequest(r, dst); err == nil {
		t.Error("expected error for non-pointer dst")
	}
}
