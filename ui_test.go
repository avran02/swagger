package swagger

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeUI_EscapesScriptValues(t *testing.T) {
	value := "</script><script>alert(1)</script>\n\"\\"
	encoded := jsonString(value)
	var decoded string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || decoded != value {
		t.Fatalf("invalid string encoding: %q, %v", encoded, err)
	}
	w := httptest.NewRecorder()
	ServeUI(UIConfig{SpecURL: value})(w, httptest.NewRequest("GET", "/docs/", nil))
	if strings.Contains(w.Body.String(), "</script><script>alert(1)") {
		t.Fatal("script terminator was not escaped")
	}
	if !strings.Contains(w.Body.String(), encoded) {
		t.Fatal("encoded value missing")
	}
}

func TestServeUI_EmbedsEnumHintsPlugin(t *testing.T) {
	w := httptest.NewRecorder()
	ServeUI(UIConfig{})(w, httptest.NewRequest("GET", "/docs/", nil))
	html := w.Body.String()
	for _, want := range []string{"function SwaggerEnumHintsPlugin(system)", "SwaggerUIBundle.plugins.DownloadUrl,\n      SwaggerEnumHintsPlugin", "!props.isExecute"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing enum hints integration: %q", want)
		}
	}
}
