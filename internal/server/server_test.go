package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(New(st))
	t.Cleanup(ts.Close)
	return ts
}

func TestEndpoints(t *testing.T) {
	ts := newTestServer(t)

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", res.StatusCode, err)
	}

	res, err = http.Get(ts.URL + "/api/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil || info["version"] == nil {
		t.Fatalf("info: %v %v", info, err)
	}

	res, err = http.Get(ts.URL + "/")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("index: %v %v", res.StatusCode, err)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("missing CSP header")
	}
}
