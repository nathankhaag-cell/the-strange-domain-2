package server

import (
	"slices"
	"testing"
)

func TestInfoReportsCompat(t *testing.T) {
	ts := newTestServer(t)
	var info struct {
		Version      string   `json:"version"`
		API          int      `json:"api"`
		MinClientAPI *int     `json:"min_client_api"`
		Features     []string `json:"features"`
	}
	if code := call(t, ts, "GET", "/api/v1/info", "", nil, &info); code != 200 {
		t.Fatalf("info: %d", code)
	}
	if info.API != APILevel || info.API < 2 {
		t.Fatalf("api = %d, want %d (>= 2)", info.API, APILevel)
	}
	if info.MinClientAPI == nil || *info.MinClientAPI != MinClientAPI || *info.MinClientAPI > info.API {
		t.Fatalf("min_client_api = %v", info.MinClientAPI)
	}
	for _, f := range []string{"attachments", "avatars"} {
		if !slices.Contains(info.Features, f) {
			t.Fatalf("features %v missing %q", info.Features, f)
		}
	}
}
