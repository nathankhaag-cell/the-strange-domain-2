package server

import (
	"net/http"
	"testing"
)

func TestCORSOnlyForAppOrigins(t *testing.T) {
	ts := newTestServer(t)

	preflight := func(origin string) *http.Response {
		req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/v1/auth/challenge", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		req.Header.Set("Access-Control-Request-Private-Network", "true")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	for _, origin := range []string{"http://localhost", "https://localhost", "capacitor://localhost"} {
		res := preflight(origin)
		if res.StatusCode != http.StatusNoContent {
			t.Errorf("%s preflight: %d", origin, res.StatusCode)
		}
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("%s preflight allow-origin %q", origin, got)
		}
		if res.Header.Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" ||
			res.Header.Get("Access-Control-Allow-Private-Network") != "true" {
			t.Errorf("%s preflight headers: %v", origin, res.Header)
		}
		if res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: credentials must not be allowed", origin)
		}
	}

	// Anything else gets no CORS headers, so browsers keep it same-origin.
	for _, origin := range []string{"https://evil.example", "http://localhost:3000", "https://localhost:8443", "http://localhost.evil.example", "null"} {
		res := preflight(origin)
		if res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Methods") != "" {
			t.Errorf("%s got CORS headers: %v", origin, res.Header)
		}
	}

	// Actual requests from the app origin are readable; the web client's
	// files are not part of it.
	get := func(path, origin string) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := get("/api/v1/info", "http://localhost"); res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "http://localhost" {
		t.Errorf("info from app: %d %v", res.StatusCode, res.Header)
	}
	if res := get("/api/v1/info", "https://evil.example"); res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("info from other origin got CORS")
	}
	if res := get("/", "http://localhost"); res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("web client files got CORS")
	}
}
