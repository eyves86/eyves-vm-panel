package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipMiddlewareCompressesJSON(t *testing.T) {
	handler := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"hello":"world"}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip content-encoding, got %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Vary") == "" {
		t.Fatal("expected Vary: Accept-Encoding")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("failed to read gzip body: %v", err)
	}
	decoded, _ := io.ReadAll(zr)
	if !strings.Contains(string(decoded), `"hello":"world"`) {
		t.Fatalf("unexpected decoded body: %s", decoded)
	}
}

func TestGzipMiddlewareSkipsWithoutHeader(t *testing.T) {
	handler := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("should not compress when client does not advertise gzip")
	}
	if rec.Body.String() != "plain" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestGzipMiddlewareSkipsWebsocketPath(t *testing.T) {
	upgraded := false
	handler := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgraded = true
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/ssh", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !upgraded {
		t.Fatal("websocket path should reach handler")
	}
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("websocket path must not be gzipped")
	}
}

func TestIsHashedAsset(t *testing.T) {
	cases := map[string]bool{
		"/assets/index-abc123.js":  true,
		"/assets/index-def456.css": true,
		"/index.html":              false,
		"/assets-fakepath/x.js":    false, // no hash in basename => not hashed asset
		"/assets/vendors-xyz.js":   true,
		"/api/v1/containers":       false,
	}
	for path, want := range cases {
		if got := isHashedAsset(path); got != want {
			t.Errorf("isHashedAsset(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestShouldCompressPath(t *testing.T) {
	cases := map[string]bool{
		"/api/v1/containers": true,
		"/":                  true,
		"/assets/index-x.js": true,
		"/favicon.svg":       true,
		"/photo.png":         false,
		"/file.zip":          false,
	}
	for path, want := range cases {
		if got := shouldCompressPath(path); got != want {
			t.Errorf("shouldCompressPath(%q) = %v, want %v", path, got, want)
		}
	}
}
