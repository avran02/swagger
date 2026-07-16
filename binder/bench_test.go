package binder_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/avran02/swagger/binder"
)

func BenchmarkBindRequest(b *testing.B) {
	r := httptest.NewRequest(http.MethodGet, "/items?depth=3&force=true&rating=4.5", nil)
	r.Header.Set("X-Token", "tok-123")
	r.AddCookie(&http.Cookie{Name: "session", Value: "sess-1"})

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var dst bindTarget
		if err := binder.BindRequest(r, &dst); err != nil {
			b.Fatal(err)
		}
	}
}
