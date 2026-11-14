package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Los archivos estáticos se guardan en el navegador y se revalidan: sin
// ETag no hay 304 y cada página bajaba todo de nuevo (la fuente de íconos
// llegaba tarde y se leían sus nombres).
func TestStaticRevalida(t *testing.T) {
	h := http.StripPrefix("/static/", staticHandler())
	const path = "/static/beercss-5.0.3/material-symbols-outlined.woff2"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	tag := rec.Header().Get("ETag")
	if rec.Code != http.StatusOK || tag == "" {
		t.Fatalf("primer pedido: %d, ETag %q", rec.Code, tag)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, quería no-cache", cc)
	}

	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("If-None-Match", tag)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("con If-None-Match: %d, quería 304", rec.Code)
	}

	req = httptest.NewRequest("GET", path, nil)
	req.Header.Set("If-None-Match", `"otro"`)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("con otro ETag: %d, quería 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/static/no-existe.js", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("ETag") != "" {
		t.Errorf("archivo que no existe: %d, ETag %q", rec.Code, rec.Header().Get("ETag"))
	}
}
