package binder_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avran02/swagger/binder"
)

func TestBindRequest_StrictBody(t *testing.T) {
	type child struct {
		Name string `json:"name"`
	}
	type request struct {
		Name   string           `json:"name"`
		Tenant int              `header:"X-Tenant-ID" required:"false"`
		Child  *child           `json:"child"`
		Items  []child          `json:"items,omitempty"`
		Map    map[string]child `json:"map,omitempty"`
	}
	tests := []struct{ name, body, contentType, loc string }{
		{"duplicate nested object", `{"name":"ok","child":{"name":"first"},"child":{"name":"last"}}`, "application/json", "body"},
		{"second value", `{} {}`, "application/json", "body"},
		{"trailing garbage", `{} garbage`, "application/json", "body"},
		{"wrong media type", `{}`, "text/plain", "body"},
		{"missing media type", `{}`, "", "body"},
		{"substring media type", `{}`, "x-application/json", "body"},
		{"multipart bypass", "--x--\r\n", "multipart/form-data; boundary=x", "body"},
		{"missing field", `{}`, "application/json", "body.name"},
		{"null scalar", `{"name":null}`, "application/json", "body.name"},
		{"null root", `null`, "application/json", "body"},
		{"wrong root type", `[]`, "application/json", "body"},
		{"header injection", `{"name":"ok","Tenant":123}`, "application/json", "body.Tenant"},
		{"header lowercase injection", `{"name":"ok","tenant":123}`, "application/json", "body.tenant"},
		{"unknown field", `{"name":"ok","extra":123}`, "application/json", "body.extra"},
		{"missing in array", `{"name":"ok","items":[{}]}`, "application/json", "body.items.0.name"},
		{"missing in map", `{"name":"ok","map":{"first":{}}}`, "application/json", "body.map.first.name"},
		{"nullable child", `{"name":"ok","child":null}`, "application/json", ""},
		{"valid nested", `{"name":"ok","items":[{"name":"kid"}]}`, "application/json; charset=utf-8", ""},
		{"trailing whitespace", "{\"name\":\"ok\"} \n", "application/json", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			var dst request
			err := binder.BindRequest(r, &dst)
			if tt.loc == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var verr *binder.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected validation error, got %v", err)
			}
			found := false
			for _, d := range verr.Detail {
				if strings.Join(d.Loc, ".") == tt.loc {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s in %+v", tt.loc, verr.Detail)
			}
			if dst.Tenant != 0 {
				t.Fatalf("body populated header: %+v", dst)
			}
		})
	}
}

func TestBindRequest_NumericBounds(t *testing.T) {
	tests := []struct {
		query string
		valid bool
	}{
		{"u=255&i=-128&f=3.4e38", true},
		{"u=256&i=0&f=1", false},
		{"u=1&i=128&f=1", false},
		{"u=1&i=-129&f=1", false},
		{"u=1&i=0&f=3.5e38", false},
		{"u=1&i=0&f=NaN", false},
		{"u=1&i=0&f=Inf", false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var dst struct {
				U uint8   `query:"u"`
				I int8    `query:"i"`
				F float32 `query:"f"`
			}
			err := binder.BindRequest(httptest.NewRequest("GET", "/?"+tt.query, nil), &dst)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v got %+v, err=%v", tt.valid, dst, err)
			}
		})
	}
}

func TestBindRequest_IgnoredTags(t *testing.T) {
	var dst struct {
		X    int    `query:"-"`
		File string `form:"-"`
		Body string `json:"-"`
	}
	if err := binder.BindRequest(httptest.NewRequest("POST", "/", nil), &dst); err != nil {
		t.Fatal(err)
	}
}

func TestBindRequest_RequiredNullablePointer(t *testing.T) {
	for _, body := range []string{`{}`, `{"name":null}`, `{"name":"ok"}`} {
		t.Run(body, func(t *testing.T) {
			var dst struct {
				Name *string `json:"name" required:"true"`
			}
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			err := binder.BindRequest(r, &dst)
			if (err != nil) != (body == `{}`) {
				t.Fatalf("body=%s err=%v", body, err)
			}
		})
	}
}

func TestBindRequest_MixedSourcesKeepTheirValues(t *testing.T) {
	var dst struct {
		Name   string `json:"name"`
		Tenant int    `header:"X-Tenant-ID"`
		Page   int    `query:"page"`
	}
	r := httptest.NewRequest("POST", "/?page=2", strings.NewReader(`{"name":"ok"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant-ID", "42")
	if err := binder.BindRequest(r, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "ok" || dst.Tenant != 42 || dst.Page != 2 {
		t.Fatalf("got %+v", dst)
	}
}

func TestBindRequest_RejectsAmbiguousDTO(t *testing.T) {
	tests := []any{
		&struct {
			Name string `json:"name"`
			File string `form:"file"`
		}{},
		&struct {
			Tenant int `json:"tenant" header:"X-Tenant-ID"`
		}{},
		&struct {
			Page int `query:"page" header:"X-Page"`
		}{},
		&struct {
			Page int `query:""`
		}{},
	}
	for _, dst := range tests {
		if err := binder.BindRequest(httptest.NewRequest("POST", "/", nil), dst); err == nil {
			t.Fatalf("accepted ambiguous DTO %T", dst)
		}
	}
}
