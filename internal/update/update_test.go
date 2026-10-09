package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.2.0", "v1.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.0", "v1.0.0-beta.1", 1},
		{"v1.0.0-beta.2", "v1.0.0-beta.10", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-1", "v1.0.0-alpha", -1},
		{"1.0.0+build.5", "v1.0.0", 0},
	}
	for _, c := range cases {
		a, ok1 := Parse(c.a)
		b, ok2 := Parse(c.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse %q %q", c.a, c.b)
		}
		if got := Compare(a, b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"dev", "abc1234", "", "v1.2", "v1.2.x", "v01.2.3"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func fakeGitHub(t *testing.T, body string, status int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestCheck(t *testing.T) {
	url := fakeGitHub(t, `{"tag_name":"v0.2.0","html_url":"https://github.com/x/y/releases/tag/v0.2.0"}`, 200)
	c := &Checker{Current: "v0.1.0", URL: url}
	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "v0.2.0" || st.URL != "https://github.com/x/y/releases/tag/v0.2.0" {
		t.Fatalf("status = %+v", st)
	}
	if c.Status() != st {
		t.Fatal("status not stored")
	}

	c = &Checker{Current: "v0.2.0", URL: url}
	if st, _ := c.Check(context.Background()); st == nil || st.Available {
		t.Fatalf("same version reported as update: %+v", st)
	}

	c = &Checker{Current: "dev", URL: url}
	if c.Enabled() {
		t.Fatal("dev build should not check")
	}
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("dev build checked")
	}
}

func TestCheckFailuresAreQuiet(t *testing.T) {
	for _, url := range []string{
		fakeGitHub(t, `rate limited`, 403),
		fakeGitHub(t, `not json`, 200),
		"http://127.0.0.1:1", // nothing listening
	} {
		c := &Checker{Current: "v0.1.0", URL: url}
		if _, err := c.Check(context.Background()); err == nil {
			t.Errorf("%s: expected an error", url)
		}
		c.CheckAndLog(context.Background()) // must not panic or block
		if c.Status() != nil {
			t.Errorf("%s: failed check stored a status", url)
		}
	}
}
