package binder_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/avran02/swagger/binder"
)

// buildNestedType constructs a struct type nested `depth` levels deep, with
// `fieldsPerLevel` json-tagged string fields at every level (plus one field
// pointing to the next level's struct), simulating a huge multi-nested JSON
// body DTO.
func buildNestedType(depth, fieldsPerLevel int) reflect.Type {
	var inner reflect.Type
	for d := depth; d >= 0; d-- {
		fields := make([]reflect.StructField, 0, fieldsPerLevel+1)
		for i := 0; i < fieldsPerLevel; i++ {
			fields = append(fields, reflect.StructField{
				Name: fmt.Sprintf("Field%d", i),
				Type: reflect.TypeOf(""),
				Tag:  reflect.StructTag(fmt.Sprintf(`json:"field_%d"`, i)),
			})
		}
		if inner != nil {
			fields = append(fields, reflect.StructField{
				Name: "Child",
				Type: inner,
				Tag:  `json:"child"`,
			})
		}
		inner = reflect.StructOf(fields)
	}

	// Top-level wrapper adds query/path/header/cookie fields alongside the
	// deeply nested JSON body, mirroring a realistic request DTO.
	top := []reflect.StructField{
		{Name: "Root", Type: inner, Tag: `json:"root"`},
		{Name: "Page", Type: reflect.TypeOf(0), Tag: `query:"page" default:"1"`},
		{Name: "Depth", Type: reflect.TypeOf(0), Tag: `query:"depth"`},
		{Name: "Force", Type: reflect.TypeOf(false), Tag: `query:"force"`},
		{Name: "Token", Type: reflect.TypeOf(""), Tag: `header:"X-Token"`},
		{Name: "Sess", Type: reflect.TypeOf(""), Tag: `cookie:"session"`},
		{Name: "ID", Type: reflect.TypeOf(int64(0)), Tag: `path:"id"`},
	}
	return reflect.StructOf(top)
}

// fillJSON produces a JSON payload matching t's shape (zero-value scalars are
// fine — we only care about decode cost, not content).
func fillJSON(t reflect.Type) []byte {
	v := reflect.New(t)
	data, err := json.Marshal(v.Interface())
	if err != nil {
		panic(err)
	}
	return data
}

func BenchmarkBindRequest_LargeNestedJSON(b *testing.B) {
	// 6 levels deep, 30 fields per level => ~180 struct fields total,
	// plus 7 top-level query/path/header/cookie fields.
	rootType := buildNestedType(6, 30)
	body := fillJSON(rootType)
	b.Logf("payload size: %d bytes, type fields at top level: %d", len(body), rootType.NumField())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "/items?depth=3&force=true", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Token", "tok-123")
		r.AddCookie(&http.Cookie{Name: "session", Value: "sess-1"})

		dst := reflect.New(rootType).Interface()
		if err := binder.BindRequest(r, dst); err != nil {
			b.Fatal(err)
		}
	}
}
