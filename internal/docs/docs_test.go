package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	for path, want := range map[string]string{
		"/openapi.yaml": "openapi: 3.0.3",
		"/swagger":      `url: "/openapi.yaml"`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: статус %d, в теле нет %q", path, rec.Code, want)
		}
	}
}
