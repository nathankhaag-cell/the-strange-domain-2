package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dom := domains.NewService(st)
	hub := relay.NewHub()
	ts := httptest.NewServer(New(st, dom, auth.NewService(st, dom), relay.NewService(st, hub), hub))
	t.Cleanup(ts.Close)
	return ts
}

// call sends a JSON request and decodes the JSON response into out (if non-nil).
func call(t *testing.T, ts *httptest.Server, method, path, token string, body, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

type client struct {
	priv     ed25519.PrivateKey
	userID   string
	deviceID string
	token    string
}

// signUp registers a new device and signs it in.
func signUp(t *testing.T, ts *httptest.Server, callsign, summons string) (*client, int) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c := &client{priv: priv}
	var reg struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	code := call(t, ts, "POST", "/api/v1/register", "", map[string]string{
		"callsign": callsign, "device_name": "laptop",
		"public_key": base64.StdEncoding.EncodeToString(pub), "summons": summons,
	}, &reg)
	if code != http.StatusCreated {
		return nil, code
	}
	c.userID, c.deviceID = reg.UserID, reg.DeviceID
	c.token = signIn(t, ts, c)
	return c, code
}

func signIn(t *testing.T, ts *httptest.Server, c *client) string {
	t.Helper()
	var ch struct{ Nonce string }
	if code := call(t, ts, "POST", "/api/v1/auth/challenge", "", map[string]string{"device_id": c.deviceID}, &ch); code != 200 {
		t.Fatalf("challenge: %d", code)
	}
	nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
	var v struct{ Token string }
	code := call(t, ts, "POST", "/api/v1/auth/verify", "", map[string]string{
		"device_id": c.deviceID, "nonce": ch.Nonce,
		"signature": base64.StdEncoding.EncodeToString(auth.SignChallenge(c.priv, nonce)),
	}, &v)
	if code != 200 || v.Token == "" {
		t.Fatalf("verify: %d", code)
	}
	return v.Token
}

func TestPublicEndpoints(t *testing.T) {
	ts := newTestServer(t)
	if code := call(t, ts, "GET", "/healthz", "", nil, nil); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
	var info map[string]any
	if code := call(t, ts, "GET", "/api/v1/info", "", nil, &info); code != 200 || info["version"] == nil {
		t.Fatalf("info: %d %v", code, info)
	}
	res, err := http.Get(ts.URL + "/")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("missing CSP header")
	}
	// Every file of the embedded client is served, including bundler chunks
	// whose names start with "_" (plain go:embed would skip them).
	err = fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		r, err := http.Get(ts.URL + strings.TrimPrefix(p, "web"))
		if err != nil {
			return err
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSignInAndDomainFlow(t *testing.T) {
	ts := newTestServer(t)

	// The first account needs no Summons and becomes node admin.
	abbot, code := signUp(t, ts, "abbot", "")
	if code != http.StatusCreated {
		t.Fatalf("first register: %d", code)
	}
	var me struct {
		IsNodeAdmin bool `json:"is_node_admin"`
	}
	call(t, ts, "GET", "/api/v1/me", abbot.token, nil, &me)
	if !me.IsNodeAdmin {
		t.Fatal("first account should be node admin")
	}

	// Everyone after that needs a Summons.
	if _, code := signUp(t, ts, "stranger", ""); code != http.StatusForbidden {
		t.Fatalf("register without summons: got %d, want 403", code)
	}

	var d domainJSON
	if code := call(t, ts, "POST", "/api/v1/domains", abbot.token, map[string]string{"name": "Sector 7"}, &d); code != 201 {
		t.Fatalf("create domain: %d", code)
	}
	var sm struct{ Summons string }
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/summons", abbot.token, map[string]int{"max_uses": 5}, &sm); code != 201 {
		t.Fatalf("summons: %d", code)
	}
	brother, code := signUp(t, ts, "brother", sm.Summons)
	if code != http.StatusCreated {
		t.Fatalf("register with summons: %d", code)
	}

	var detail struct {
		Roles []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"roles"`
		Members  []struct{ Callsign string } `json:"members"`
		Channels []struct{ Kind string }     `json:"channels"`
	}
	if code := call(t, ts, "GET", "/api/v1/domains/"+d.ID, brother.token, nil, &detail); code != 200 {
		t.Fatalf("get domain: %d", code)
	}
	if len(detail.Members) != 2 || len(detail.Channels) != 2 || detail.Roles[0].Name != "Abbot" {
		t.Fatalf("unexpected domain detail: %+v", detail)
	}

	// A Brother / Sister cannot create Chapels or ban the Abbot.
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/channels", brother.token,
		map[string]string{"name": "x", "kind": "text"}, nil); code != http.StatusForbidden {
		t.Fatalf("member create channel: %d", code)
	}
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+abbot.userID+"/ban", brother.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("member ban abbot: %d", code)
	}

	// The Abbot makes them a Warden, then bans them.
	var warden string
	for _, r := range detail.Roles {
		if r.Name == "Warden" {
			warden = r.ID
		}
	}
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+brother.userID+"/role", abbot.token,
		map[string]string{"role_id": warden}, nil); code != http.StatusNoContent {
		t.Fatalf("assign role: %d", code)
	}
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+brother.userID+"/ban", abbot.token,
		map[string]string{"reason": "test"}, nil); code != http.StatusNoContent {
		t.Fatalf("ban: %d", code)
	}
	if code := call(t, ts, "GET", "/api/v1/domains/"+d.ID, brother.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("banned member reads domain: %d", code)
	}

	// Signing out invalidates the token.
	call(t, ts, "POST", "/api/v1/auth/signout", brother.token, nil, nil)
	if code := call(t, ts, "GET", "/api/v1/me", brother.token, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("after signout: %d", code)
	}
}

func TestChallengeCannotBeReplayedOrForged(t *testing.T) {
	ts := newTestServer(t)
	c, _ := signUp(t, ts, "abbot", "")

	var ch struct{ Nonce string }
	call(t, ts, "POST", "/api/v1/auth/challenge", "", map[string]string{"device_id": c.deviceID}, &ch)
	nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)

	// A signature from a different key fails, and burns the challenge.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	body := map[string]string{"device_id": c.deviceID, "nonce": ch.Nonce,
		"signature": base64.StdEncoding.EncodeToString(auth.SignChallenge(other, nonce))}
	if code := call(t, ts, "POST", "/api/v1/auth/verify", "", body, nil); code != http.StatusUnauthorized {
		t.Fatalf("forged signature: %d", code)
	}
	body["signature"] = base64.StdEncoding.EncodeToString(auth.SignChallenge(c.priv, nonce))
	if code := call(t, ts, "POST", "/api/v1/auth/verify", "", body, nil); code != http.StatusUnauthorized {
		t.Fatalf("reused challenge: %d", code)
	}
	if code := call(t, ts, "GET", "/api/v1/me", "not-a-token", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", code)
	}
}

func TestStreamPushesMessages(t *testing.T) {
	ts := newTestServer(t)
	abbot, _ := signUp(t, ts, "abbot", "")
	var d domainJSON
	call(t, ts, "POST", "/api/v1/domains", abbot.token, map[string]string{"name": "Sector 7"}, &d)
	var detail struct {
		Channels []struct{ ID string } `json:"channels"`
	}
	call(t, ts, "GET", "/api/v1/domains/"+d.ID, abbot.token, nil, &detail)
	chapel := detail.Channels[0].ID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, map[string]string{"token": abbot.token}); err != nil {
		t.Fatal(err)
	}
	var ready map[string]string
	if err := wsjson.Read(ctx, conn, &ready); err != nil || ready["type"] != "ready" {
		t.Fatalf("ready: %v %v", ready, err)
	}

	var sent struct{ Seq int64 }
	if code := call(t, ts, "POST", "/api/v1/groups/"+chapel+"/messages", abbot.token,
		map[string]any{"kind": "application", "epoch": 0, "data": []byte("ciphertext")}, &sent); code != http.StatusCreated {
		t.Fatalf("send: %d", code)
	}
	var ev relay.Event
	if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "message" || ev.Seq != sent.Seq {
		t.Fatalf("event: %+v %v", ev, err)
	}
	var msgs []relay.Message
	call(t, ts, "GET", "/api/v1/groups/"+chapel+"/messages?after=0", abbot.token, nil, &msgs)
	if len(msgs) != 1 || string(msgs[0].Data) != "ciphertext" {
		t.Fatalf("fetch: %+v", msgs)
	}
	// A stale-epoch commit is refused with 409.
	call(t, ts, "POST", "/api/v1/groups/"+chapel+"/messages", abbot.token,
		map[string]any{"kind": "commit", "epoch": 0, "data": []byte("c")}, nil)
	if code := call(t, ts, "POST", "/api/v1/groups/"+chapel+"/messages", abbot.token,
		map[string]any{"kind": "commit", "epoch": 0, "data": []byte("c")}, nil); code != http.StatusConflict {
		t.Fatalf("stale commit: %d", code)
	}
}

func TestStreamRejectsBadToken(t *testing.T) {
	ts := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	wsjson.Write(ctx, conn, map[string]string{"token": "nope"})
	var v any
	if err := wsjson.Read(ctx, conn, &v); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("got %v, want policy violation close", err)
	}
}

func TestConclaveListAndDomainEvents(t *testing.T) {
	ts := newTestServer(t)
	abbot, _ := signUp(t, ts, "abbot", "")
	var d domainJSON
	call(t, ts, "POST", "/api/v1/domains", abbot.token, map[string]string{"name": "Sector 7"}, &d)
	var sm struct{ Summons string }
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/summons", abbot.token, map[string]int{"max_uses": 5}, &sm)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	wsjson.Write(ctx, conn, map[string]string{"token": abbot.token})
	var ready map[string]string
	wsjson.Read(ctx, conn, &ready)

	// A new member joining by Summons tells existing members to refresh.
	brother, _ := signUp(t, ts, "brother", sm.Summons)
	var ev relay.Event
	if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "domain" || ev.GroupID != d.ID {
		t.Fatalf("join event: %+v %v", ev, err)
	}

	// Publishing KeyPackages tells peers they can add this person's devices.
	if code := call(t, ts, "POST", "/api/v1/keypackages", brother.token, map[string]any{"key_packages": [][]byte{[]byte("kp")}}, nil); code != http.StatusNoContent {
		t.Fatalf("publish: %d", code)
	}
	if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "keys" || ev.GroupID != brother.userID {
		t.Fatalf("keys event: %+v %v", ev, err)
	}

	var created struct{ ID string }
	if code := call(t, ts, "POST", "/api/v1/conclaves", abbot.token, map[string]any{"member_ids": []string{brother.userID}}, &created); code != 201 {
		t.Fatalf("create conclave: %d", code)
	}
	if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "conclave" || ev.GroupID != created.ID {
		t.Fatalf("conclave event: %+v %v", ev, err)
	}
	var list []relay.Conclave
	if code := call(t, ts, "GET", "/api/v1/conclaves", brother.token, nil, &list); code != 200 {
		t.Fatalf("list conclaves: %d", code)
	}
	if len(list) != 1 || list[0].ID != created.ID || len(list[0].Members) != 2 {
		t.Fatalf("conclaves: %+v", list)
	}

	// Members can list every device that belongs in a group's MLS state.
	var detail struct {
		Channels []struct{ ID string } `json:"channels"`
	}
	call(t, ts, "GET", "/api/v1/domains/"+d.ID, abbot.token, nil, &detail)
	var devs []relay.GroupDevice
	if code := call(t, ts, "GET", "/api/v1/groups/"+detail.Channels[0].ID+"/devices", brother.token, nil, &devs); code != 200 || len(devs) != 2 {
		t.Fatalf("group devices: %d %+v", code, devs)
	}

	// Kicking notifies the domain, including the person removed.
	if code := call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+brother.userID+"/kick", abbot.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("kick: %d", code)
	}
	if err := wsjson.Read(ctx, conn, &ev); err != nil || ev.Type != "domain" {
		t.Fatalf("kick event: %+v %v", ev, err)
	}
	if code := call(t, ts, "GET", "/api/v1/groups/"+detail.Channels[0].ID+"/devices", brother.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("kicked member lists devices: %d", code)
	}
}
