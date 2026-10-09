package server

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

func newMediaServer(t *testing.T, maxSize, quota int64) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dom := domains.NewService(st)
	hub := relay.NewHub()
	ts := httptest.NewServer(New(st, dom, auth.NewService(st, dom), relay.NewService(st, hub), hub, WithUploadLimits(maxSize, quota)))
	t.Cleanup(ts.Close)
	return ts, dir
}

// raw sends a non-JSON body and returns the status and response body.
func raw(t *testing.T, ts *httptest.Server, method, path, token string, body []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

type mediaFixture struct {
	ts                       *httptest.Server
	dir                      string
	abbot, brother, outsider *client
	chapel                   string
}

// mediaSetup: abbot and brother share "Sector 7"; outsider shares only a
// second domain with abbot.
func mediaSetup(t *testing.T, maxSize, quota int64) *mediaFixture {
	f := &mediaFixture{}
	f.ts, f.dir = newMediaServer(t, maxSize, quota)
	f.abbot, _ = signUp(t, f.ts, "abbot", "")
	var d, d2 domainJSON
	call(t, f.ts, "POST", "/api/v1/domains", f.abbot.token, map[string]string{"name": "Sector 7"}, &d)
	call(t, f.ts, "POST", "/api/v1/domains", f.abbot.token, map[string]string{"name": "Elsewhere"}, &d2)
	var sm, sm2 struct{ Summons string }
	call(t, f.ts, "POST", "/api/v1/domains/"+d.ID+"/summons", f.abbot.token, map[string]int{"max_uses": 5}, &sm)
	call(t, f.ts, "POST", "/api/v1/domains/"+d2.ID+"/summons", f.abbot.token, map[string]int{"max_uses": 5}, &sm2)
	f.brother, _ = signUp(t, f.ts, "brother", sm.Summons)
	f.outsider, _ = signUp(t, f.ts, "outsider", sm2.Summons)
	var detail struct {
		Channels []struct{ ID, Kind string } `json:"channels"`
	}
	call(t, f.ts, "GET", "/api/v1/domains/"+d.ID, f.abbot.token, nil, &detail)
	f.chapel = detail.Channels[0].ID
	return f
}

func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	root := filepath.Join(dir, "blobs")
	filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err == nil && e.IsDir() && p == filepath.Join(root, "tmp") {
			return filepath.SkipDir // partial uploads
		}
		if err == nil && !e.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestBlobLifecycle(t *testing.T) {
	f := mediaSetup(t, 1<<20, 10<<20)
	ciphertext := bytes.Repeat([]byte{0xA5, 0x17}, 5000)

	// Only members who may post can upload; outsiders can't.
	if code, _ := raw(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/blobs", f.outsider.token, ciphertext); code != http.StatusForbidden {
		t.Fatalf("outsider upload: %d", code)
	}
	if code, _ := raw(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/blobs", "", ciphertext); code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload: %d", code)
	}
	code, body := raw(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/blobs", f.abbot.token, ciphertext)
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %s", code, body)
	}
	id := between(string(body), `"id":"`, `"`)

	// The file on disk is exactly what was uploaded, under the data dir.
	files := blobFiles(t, f.dir)
	if len(files) != 1 {
		t.Fatalf("blob files: %v", files)
	}
	if onDisk, _ := os.ReadFile(files[0]); !bytes.Equal(onDisk, ciphertext) {
		t.Fatal("stored blob differs from upload")
	}

	// Before its message is sent, only the uploader can fetch it.
	if code, _ := raw(t, f.ts, "GET", "/api/v1/blobs/"+id, f.brother.token, nil); code != http.StatusNotFound {
		t.Fatalf("brother fetch before send: %d", code)
	}
	if code, got := raw(t, f.ts, "GET", "/api/v1/blobs/"+id, f.abbot.token, nil); code != 200 || !bytes.Equal(got, ciphertext) {
		t.Fatalf("uploader fetch: %d", code)
	}

	// Someone else can't attach it to their message.
	if code := call(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/messages", f.brother.token,
		map[string]any{"kind": "application", "epoch": 0, "data": []byte("x"), "blobs": []string{id}}, nil); code != http.StatusBadRequest {
		t.Fatalf("attach someone else's blob: %d", code)
	}
	var sent struct{ Seq int64 }
	if code := call(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/messages", f.abbot.token,
		map[string]any{"kind": "application", "epoch": 0, "data": []byte("x"), "blobs": []string{id}}, &sent); code != http.StatusCreated {
		t.Fatalf("send with blob: %d", code)
	}
	// Attached once only.
	if code := call(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/messages", f.abbot.token,
		map[string]any{"kind": "application", "epoch": 0, "data": []byte("x"), "blobs": []string{id}}, nil); code != http.StatusBadRequest {
		t.Fatalf("reattach: %d", code)
	}

	// Now members can fetch it; outsiders and anonymous callers can't.
	res, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/blobs/"+id, nil)
	res.Header.Set("Authorization", "Bearer "+f.brother.token)
	resp, err := http.DefaultClient.Do(res)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, ciphertext) {
		t.Fatalf("member fetch: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("blob content type: %q", ct)
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/blobs/"+id, f.outsider.token, nil); code != http.StatusNotFound {
		t.Fatalf("outsider fetch: %d", code)
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/blobs/"+id, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous fetch: %d", code)
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/blobs/../../node.db", f.abbot.token, nil); code == 200 {
		t.Fatal("path traversal served something")
	}

	// Deleting the message deletes the blob and its file.
	if code := call(t, f.ts, "DELETE", "/api/v1/groups/"+f.chapel+"/messages/"+strconv.FormatInt(sent.Seq, 10), f.abbot.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete message: %d", code)
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/blobs/"+id, f.abbot.token, nil); code != http.StatusNotFound {
		t.Fatalf("fetch after delete: %d", code)
	}
	if files := blobFiles(t, f.dir); len(files) != 0 {
		t.Fatalf("files left after delete: %v", files)
	}
}

func TestBlobLimits(t *testing.T) {
	f := mediaSetup(t, 1000, 2500)
	up := func(n int) int {
		code, _ := raw(t, f.ts, "POST", "/api/v1/groups/"+f.chapel+"/blobs", f.brother.token, bytes.Repeat([]byte{1}, n))
		return code
	}
	if code := up(1001); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over size cap: %d", code)
	}
	if code := up(0); code != http.StatusBadRequest {
		t.Fatalf("empty: %d", code)
	}
	if up(1000) != 201 || up(1000) != 201 {
		t.Fatal("uploads within quota failed")
	}
	if code := up(600); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over quota: %d", code)
	}
	var lim map[string]int64
	call(t, f.ts, "GET", "/api/v1/limits", f.brother.token, nil, &lim)
	if lim["max_upload_bytes"] != 1000 || lim["quota_bytes"] != 2500 || lim["used_bytes"] != 2000 {
		t.Fatalf("limits: %v", lim)
	}
	// Nothing partial is left behind by refused uploads.
	if files := blobFiles(t, f.dir); len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
}

func TestAvatars(t *testing.T) {
	f := mediaSetup(t, 0, 0)
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	pic := buf.Bytes()

	if code, _ := raw(t, f.ts, "PUT", "/api/v1/account/avatar", f.brother.token, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); code != http.StatusBadRequest {
		t.Fatalf("svg avatar: %d", code)
	}
	if code, _ := raw(t, f.ts, "PUT", "/api/v1/account/avatar", f.brother.token, bytes.Repeat([]byte{0}, 600<<10)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge avatar: %d", code)
	}
	if code, body := raw(t, f.ts, "PUT", "/api/v1/account/avatar", f.brother.token, pic); code != 200 {
		t.Fatalf("set avatar: %d %s", code, body)
	}

	// Domain members and /me carry the version.
	var detail struct {
		Members []struct {
			Callsign string `json:"callsign"`
			Avatar   int64  `json:"avatar"`
		} `json:"members"`
	}
	var doms []domainJSON
	call(t, f.ts, "GET", "/api/v1/domains", f.abbot.token, nil, &doms)
	for _, d := range doms {
		if d.Name == "Sector 7" {
			call(t, f.ts, "GET", "/api/v1/domains/"+d.ID, f.abbot.token, nil, &detail)
		}
	}
	found := false
	for _, m := range detail.Members {
		if m.Callsign == "brother" {
			found = m.Avatar > 0
		} else if m.Avatar != 0 {
			t.Fatalf("unexpected avatar for %s", m.Callsign)
		}
	}
	if !found {
		t.Fatalf("brother's avatar version missing: %+v", detail)
	}
	var me struct{ Avatar int64 }
	call(t, f.ts, "GET", "/api/v1/me", f.brother.token, nil, &me)
	if me.Avatar == 0 {
		t.Fatal("me has no avatar version")
	}

	// People who share a space can fetch it; others can't.
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/users/"+f.brother.userID+"/avatar?v=1", nil)
	req.Header.Set("Authorization", "Bearer "+f.abbot.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(got, pic) || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("peer fetch: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/users/"+f.brother.userID+"/avatar", f.outsider.token, nil); code != http.StatusNotFound {
		t.Fatalf("stranger fetch: %d", code)
	}
	if code, _ := raw(t, f.ts, "DELETE", "/api/v1/account/avatar", f.brother.token, nil); code != http.StatusNoContent {
		t.Fatalf("delete avatar: %d", code)
	}
	if code, _ := raw(t, f.ts, "GET", "/api/v1/users/"+f.brother.userID+"/avatar", f.abbot.token, nil); code != http.StatusNotFound {
		t.Fatalf("fetch deleted avatar: %d", code)
	}
}

func TestCSPAllowsBlobImagesOnly(t *testing.T) {
	ts := newTestServer(t)
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	got := res.Header.Get("Content-Security-Policy")
	want := "default-src 'self'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'"
	if got != want {
		t.Fatalf("CSP = %q, want %q", got, want)
	}
}

func between(s, start, end string) string {
	_, after, ok := strings.Cut(s, start)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(after, end)
	return v
}
