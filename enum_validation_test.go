package swagger_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/avran02/swagger"
)

type validatedItem struct {
	Status publicStatus `json:"status"`
}
type validatedDTO struct {
	Status     publicStatus             `json:"status"`
	Optional   *publicStatus            `json:"optional,omitempty"`
	Omitted    string                   `json:"omitted,omitempty" enum:"a,b"`
	Rating     int                      `json:"rating" enum:"1,2,3"`
	Flag       bool                     `json:"flag" enum:"true"`
	Items      []*validatedItem         `json:"items"`
	Labels     []string                 `json:"labels" enum:"a,b"`
	Lookup     map[string]*publicStatus `json:"lookup"`
	Restricted publicStatus             `json:"restricted" enum:"published"`
	Query      publicStatus             `query:"status" required:"false"`
}

func jsonRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/enum", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestBindOr422_ValidatesNestedEnums(t *testing.T) {
	valid := `{"status":"draft","rating":2,"flag":true,"items":[{"status":"published"}],"labels":["a"],"lookup":{"first":"draft","nil":null},"restricted":"published"}`
	cases := []struct {
		name, body, query string
		want              [][]string
	}{
		{"valid", valid, "", nil},
		{"nullable", strings.Replace(valid, `"status":"draft"`, `"status":"draft","optional":null`, 1), "", nil},
		{"omitted optional", valid, "", nil},
		{"enum aggregates", `{"status":"bad","rating":4,"flag":false,"items":[{"status":"bad"}],"labels":["bad"],"lookup":{"first":"bad"},"restricted":"draft","optional":"bad"}`, "?status=bad", [][]string{
			{"body", "flag"}, {"body", "items", "0", "status"}, {"body", "labels", "0"}, {"body", "lookup", "first"}, {"body", "optional"}, {"body", "rating"}, {"body", "restricted"}, {"body", "status"}, {"query", "status"},
		}},
		{"null item", strings.Replace(valid, `[{"status":"published"}]`, `[null]`, 1), "", nil},
		{"supplied optional", strings.Replace(valid, `"status":"draft"`, `"status":"draft","omitted":"bad"`, 1), "", [][]string{{"body", "omitted"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := jsonRequest(tc.body)
			r.URL.RawQuery = strings.TrimPrefix(tc.query, "?")
			w := httptest.NewRecorder()
			var dst validatedDTO
			success := swagger.BindOr422(w, r, &dst)
			if len(tc.want) == 0 {
				if !success || w.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", w.Code, w.Body)
				}
				return
			}
			if success || w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			var result swagger.ValidationError
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			var locations [][]string
			for _, field := range result.Detail {
				if field.Type != "enum" {
					t.Fatalf("unexpected error: %+v", field)
				}
				locations = append(locations, field.Loc)
			}
			if !reflect.DeepEqual(locations, tc.want) {
				t.Fatalf("locations=%v want=%v", locations, tc.want)
			}
		})
	}
}

type parameterEnums struct {
	Query   string       `query:"q" required:"false" enum:"a,b"`
	Default string       `query:"default" required:"false" default:"a" enum:"a,b"`
	Header  publicStatus `header:"X-Status" required:"false"`
	Cookie  publicStatus `cookie:"status" required:"false"`
	Path    publicStatus `path:"status"`
	Number  int          `query:"n" required:"false" enum:"1,2"`
}

func TestBindOr422_ValidatesAllParameterSources(t *testing.T) {
	swagger.SetURLParamFunc(func(r *http.Request, key string) string { return r.PathValue(key) })
	defer swagger.SetURLParamFunc(nil)
	for _, valid := range []bool{true, false} {
		r := httptest.NewRequest(http.MethodGet, "/?q=a&n=1", nil)
		r.SetPathValue("status", "draft")
		r.Header.Set("X-Status", "published")
		r.AddCookie(&http.Cookie{Name: "status", Value: "draft"})
		if !valid {
			r.URL.RawQuery = "q=&n=3"
			r.SetPathValue("status", "bad")
			r.Header.Set("X-Status", "bad")
			r.Header.Set("Cookie", "status=bad")
		}
		var dst parameterEnums
		w := httptest.NewRecorder()
		got := swagger.BindOr422(w, r, &dst)
		if got != valid {
			t.Fatalf("valid=%v status=%d body=%s", valid, w.Code, w.Body)
		}
		if valid && dst.Default != "a" {
			t.Fatalf("default=%q", dst.Default)
		}
		if !valid {
			var result swagger.ValidationError
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			want := [][]string{{"query", "q"}, {"header", "X-Status"}, {"cookie", "status"}, {"path", "status"}, {"query", "n"}}
			var locations [][]string
			for _, field := range result.Detail {
				locations = append(locations, field.Loc)
			}
			if !reflect.DeepEqual(locations, want) {
				t.Fatalf("locations=%v", locations)
			}
		}
	}
	// Missing optional fields are ignored, but defaults are still validated.
	type defaultEnum struct {
		Value string `query:"value" required:"false" default:"bad" enum:"a,b"`
	}
	var dst defaultEnum
	w := httptest.NewRecorder()
	if swagger.BindOr422(w, httptest.NewRequest(http.MethodGet, "/", nil), &dst) {
		t.Fatal("invalid default accepted")
	}
}

func TestBindOr422_ValidatesMultipartEnums(t *testing.T) {
	type upload struct {
		Mode     publicStatus          `form:"mode"`
		Rating   *int                  `multipart:"rating" enum:"1,2"`
		Optional string                `form:"optional" required:"false" enum:"a,b"`
		File     *multipart.FileHeader `form:"file"`
	}
	for _, mode := range []string{"draft", "bad"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("mode", mode)
		_ = writer.WriteField("rating", "2")
		file, err := writer.CreateFormFile("file", "sample.txt")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("content"))
		_ = writer.Close()
		r := httptest.NewRequest(http.MethodPost, "/", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		var dst upload
		success := swagger.BindOr422(w, r, &dst)
		if success != (mode == "draft") {
			t.Fatalf("mode=%s status=%d body=%s", mode, w.Code, w.Body)
		}
		if dst.File == nil {
			t.Fatal("file binding lost")
		}
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		if mode == "bad" {
			var result swagger.ValidationError
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if len(result.Detail) != 1 || !reflect.DeepEqual(result.Detail[0].Loc, []string{"body", "mode"}) {
				t.Fatalf("errors=%+v", result)
			}
		}
	}
}

func TestBindAndValidate_OptionsAndBindingCompatibility(t *testing.T) {
	options := []swagger.SchemaOption{swagger.Enum[publicStatus]("custom")}
	for _, test := range []struct {
		value   string
		options []swagger.SchemaOption
		valid   bool
	}{
		{"custom", options, true}, {"draft", options, false}, {"draft", nil, true}, {"custom", nil, false},
	} {
		var dto publicEnumDTO
		err := swagger.BindAndValidate(jsonRequest(`{"status":"`+test.value+`"}`), &dto, test.options...)
		if (err == nil) != test.valid {
			t.Fatalf("value=%s options=%d error=%v", test.value, len(test.options), err)
		}
	}
	var dto publicEnumDTO
	if err := swagger.BindRequest(jsonRequest(`{"status":"unknown"}`), &dto); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if swagger.BindOr422(w, jsonRequest(`{"status":"custom"}`), &dto, options...) == false {
		t.Fatalf("configured enum rejected: %s", w.Body)
	}
}

type invalidRuntimeEnum string

func (invalidRuntimeEnum) EnumValues() []any { return []any{42} }

func TestBindOr422_InvalidEnumDefinitionIsServerError(t *testing.T) {
	type request struct {
		Value invalidRuntimeEnum `json:"value"`
	}
	var dst request
	w := httptest.NewRecorder()
	if swagger.BindOr422(w, jsonRequest(`{"value":"a"}`), &dst) {
		t.Fatal("invalid metadata accepted")
	}
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "Internal Server Error" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

type validatedMapKey string

func (k *validatedMapKey) UnmarshalText(text []byte) error {
	*k = validatedMapKey(strings.ToUpper(string(text)))
	return nil
}

func TestBindAndValidate_MapKeys(t *testing.T) {
	type request struct {
		Signed   map[int]publicStatus             `json:"signed"`
		Unsigned map[uint]publicStatus            `json:"unsigned"`
		Text     map[validatedMapKey]publicStatus `json:"text"`
	}
	for _, valid := range []bool{true, false} {
		status := "bad"
		if valid {
			status = "draft"
		}
		body := `{"signed":{"-2":"` + status + `"},"unsigned":{"3":"` + status + `"},"text":{"key":"` + status + `"}}`
		var dst request
		err := swagger.BindAndValidate(jsonRequest(body), &dst)
		if valid {
			if err != nil || dst.Text["KEY"] != publicStatus("draft") {
				t.Fatalf("err=%v dst=%+v", err, dst)
			}
			continue
		}
		var ve *swagger.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("want validation error, got %v", err)
		}
		var got [][]string
		for _, field := range ve.Detail {
			got = append(got, field.Loc)
		}
		want := [][]string{{"body", "signed", "-2"}, {"body", "text", "key"}, {"body", "unsigned", "3"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("locations=%v want=%v", got, want)
		}
	}
}
