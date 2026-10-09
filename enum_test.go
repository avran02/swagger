package swagger_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/avran02/swagger"
	"github.com/avran02/swagger/builder"
	"github.com/avran02/swagger/registry"
	"github.com/avran02/swagger/types"
)

type publicStatus string

func (publicStatus) EnumValues() []any {
	return []any{publicStatus("draft"), publicStatus("published")}
}

var _ swagger.EnumValues = publicStatus("")

type publicEnumDTO struct {
	Status publicStatus `json:"status"`
}

func TestEnum_PublicAPIAndExplicitValidation(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	reg := registry.NewRegistry()
	reg.RegisterRequestDTO(h, publicEnumDTO{}, publicEnumDTO{}, types.Public, nil)
	reg.SetWalker(func(fn registry.WalkFunc) error { return fn("POST", "/enum", h) })
	cfg := swagger.Config{SchemaOptions: []swagger.SchemaOption{swagger.Enum[publicStatus]("custom")}}
	spec, err := builder.Build(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := spec.Components.Schemas["swagger_test.publicEnumDTO"].Properties["status"].Enum
	if !reflect.DeepEqual(got, []any{"custom"}) {
		t.Fatalf("enum = %v", got)
	}
	// Metadata must not silently introduce a new 422 boundary in existing APIs.
	req := httptest.NewRequest(http.MethodPost, "/enum", strings.NewReader(`{"status":"other"}`))
	req.Header.Set("Content-Type", "application/json")
	var dto publicEnumDTO
	if err := swagger.BindRequest(req, &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Status != "other" {
		t.Fatalf("status = %s", dto.Status)
	}
}
