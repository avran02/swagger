package binder_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/avran02/swagger/binder"
)

// buildGetType constructs a struct type with nQuery query fields, nPath path
// fields, nHeader header fields and nCookie cookie fields — simulating a huge
// GET endpoint DTO with no JSON body at all.
func buildGetType(nQuery, nPath, nHeader, nCookie int) reflect.Type {
	fields := make([]reflect.StructField, 0, nQuery+nPath+nHeader+nCookie)

	for i := 0; i < nQuery; i++ {
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("Q%d", i),
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(fmt.Sprintf(`query:"q_%d"`, i)),
		})
	}
	for i := 0; i < nPath; i++ {
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("P%d", i),
			Type: reflect.TypeOf(int64(0)),
			Tag:  reflect.StructTag(fmt.Sprintf(`path:"p_%d"`, i)),
		})
	}
	for i := 0; i < nHeader; i++ {
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("H%d", i),
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(fmt.Sprintf(`header:"X-Header-%d"`, i)),
		})
	}
	for i := 0; i < nCookie; i++ {
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("C%d", i),
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(fmt.Sprintf(`cookie:"c_%d"`, i)),
		})
	}

	return reflect.StructOf(fields)
}

// buildGetRequest constructs a GET request whose query string, headers and
// cookies populate every field produced by buildGetType with the same counts.
func buildGetRequest(nQuery, nPath, nHeader, nCookie int) *http.Request {
	q := url.Values{}
	for i := 0; i < nQuery; i++ {
		q.Set(fmt.Sprintf("q_%d", i), fmt.Sprintf("value-%d", i))
	}

	r := httptest.NewRequest(http.MethodGet, "/items?"+q.Encode(), nil)

	for i := 0; i < nHeader; i++ {
		r.Header.Set(fmt.Sprintf("X-Header-%d", i), fmt.Sprintf("hval-%d", i))
	}
	for i := 0; i < nCookie; i++ {
		r.AddCookie(&http.Cookie{Name: fmt.Sprintf("c_%d", i), Value: fmt.Sprintf("cval-%d", i)})
	}
	return r
}

func BenchmarkBindRequest_HugeGET(b *testing.B) {
	const nQuery, nPath, nHeader, nCookie = 50, 20, 30, 30

	binder.SetURLParamFunc(func(r *http.Request, key string) string {
		return "42"
	})
	b.Cleanup(func() { binder.SetURLParamFunc(nil) })

	rootType := buildGetType(nQuery, nPath, nHeader, nCookie)
	r := buildGetRequest(nQuery, nPath, nHeader, nCookie)
	b.Logf("total fields: %d (query=%d path=%d header=%d cookie=%d)",
		rootType.NumField(), nQuery, nPath, nHeader, nCookie)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := reflect.New(rootType).Interface()
		if err := binder.BindRequest(r, dst); err != nil {
			b.Fatal(err)
		}
	}
}
