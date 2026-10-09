package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestLinkSecondDeviceAndTransferHistory(t *testing.T) {
	ts := newTestServer(t)
	laptop, _ := signUp(t, ts, "nathan", "")

	var lc struct{ Code string }
	if code := call(t, ts, "POST", "/api/v1/devices/link-code", laptop.token, nil, &lc); code != 200 || len(lc.Code) != 11 {
		t.Fatalf("link code: %d %q", code, lc.Code)
	}

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var linked struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	// People type codes loosely: lower case, no dash.
	typed := strings.ToLower(strings.ReplaceAll(lc.Code, "-", ""))
	if code := call(t, ts, "POST", "/api/v1/devices/link", "", map[string]string{
		"code": typed, "device_name": "phone", "public_key": b64(pub)}, &linked); code != http.StatusCreated {
		t.Fatalf("link: %d", code)
	}
	if linked.UserID != laptop.userID {
		t.Fatal("linked device should belong to the same account")
	}
	// The code is single use.
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	if code := call(t, ts, "POST", "/api/v1/devices/link", "", map[string]string{
		"code": lc.Code, "device_name": "other", "public_key": b64(pub2)}, nil); code != http.StatusUnauthorized {
		t.Fatalf("reused code: %d", code)
	}

	phone := &client{priv: priv, userID: linked.UserID, deviceID: linked.DeviceID}
	phone.token = signIn(t, ts, phone)
	var me struct{ Callsign string }
	call(t, ts, "GET", "/api/v1/me", phone.token, nil, &me)
	if me.Callsign != "nathan" {
		t.Fatalf("phone signed in as %q", me.Callsign)
	}
	var devs []auth.Device
	call(t, ts, "GET", "/api/v1/devices", phone.token, nil, &devs)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2", len(devs))
	}

	// The laptop sends encrypted history to the phone in two chunks.
	for i, part := range []string{"history-part-1", "history-part-2"} {
		if code := call(t, ts, "POST", "/api/v1/devices/"+phone.deviceID+"/transfers", laptop.token, map[string]any{
			"transfer_id": "t1", "chunk": i, "total": 2, "data": []byte(part)}, nil); code != http.StatusNoContent {
			t.Fatalf("send chunk %d: %d", i, code)
		}
	}
	// Someone else's device can't be targeted.
	other, _ := signUp(t, ts, "stranger", mustSummons(t, ts, laptop))
	if code := call(t, ts, "POST", "/api/v1/devices/"+phone.deviceID+"/transfers", other.token, map[string]any{
		"transfer_id": "x", "chunk": 0, "total": 1, "data": []byte("x")}, nil); code != http.StatusNotFound {
		t.Fatalf("cross-account transfer: %d", code)
	}

	var pending []relay.TransferChunk
	call(t, ts, "GET", "/api/v1/transfers", phone.token, nil, &pending)
	if len(pending) != 2 || pending[0].Data != nil {
		t.Fatalf("pending: %+v", pending)
	}
	var got relay.TransferChunk
	call(t, ts, "POST", "/api/v1/transfers/"+itoa(pending[1].ID)+"/take", phone.token, nil, &got)
	if string(got.Data) != "history-part-2" || got.Chunk != 1 {
		t.Fatalf("take: %+v", got)
	}
	if code := call(t, ts, "POST", "/api/v1/transfers/"+itoa(pending[1].ID)+"/take", phone.token, nil, nil); code != http.StatusNotFound {
		t.Fatalf("second take: %d", code)
	}

	// Revoking the laptop ends its session.
	if code := call(t, ts, "DELETE", "/api/v1/devices/"+laptop.deviceID, phone.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code := call(t, ts, "GET", "/api/v1/me", laptop.token, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked device still signed in: %d", code)
	}
}

func TestRecoveryReplacesLostDevices(t *testing.T) {
	ts := newTestServer(t)
	old, _ := signUp(t, ts, "nathan", "")

	// The client derives this key pair from the recovery code shown at signup.
	rpub, rpriv, _ := ed25519.GenerateKey(rand.Reader)
	if code := call(t, ts, "PUT", "/api/v1/account/recovery-key", old.token, map[string]string{"public_key": b64(rpub)}, nil); code != http.StatusNoContent {
		t.Fatalf("set recovery key: %d", code)
	}

	var ch struct{ Nonce string }
	call(t, ts, "POST", "/api/v1/recover/challenge", "", map[string]string{"callsign": "nathan"}, &ch)
	nonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
	npub, npriv, _ := ed25519.GenerateKey(rand.Reader)

	// A wrong recovery key fails and burns the challenge.
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	body := map[string]string{"callsign": "nathan", "nonce": ch.Nonce, "signature": b64(auth.SignRecovery(wrong, nonce)),
		"device_name": "new laptop", "public_key": b64(npub)}
	if code := call(t, ts, "POST", "/api/v1/recover", "", body, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", code)
	}
	call(t, ts, "POST", "/api/v1/recover/challenge", "", map[string]string{"callsign": "nathan"}, &ch)
	nonce, _ = base64.StdEncoding.DecodeString(ch.Nonce)
	body["nonce"], body["signature"] = ch.Nonce, b64(auth.SignRecovery(rpriv, nonce))
	var rec struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	if code := call(t, ts, "POST", "/api/v1/recover", "", body, &rec); code != http.StatusCreated || rec.UserID != old.userID {
		t.Fatalf("recover: %d %+v", code, rec)
	}
	if code := call(t, ts, "GET", "/api/v1/me", old.token, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("lost device still signed in: %d", code)
	}
	fresh := &client{priv: npriv, userID: rec.UserID, deviceID: rec.DeviceID}
	fresh.token = signIn(t, ts, fresh)
	var devs []auth.Device
	call(t, ts, "GET", "/api/v1/devices", fresh.token, nil, &devs)
	if len(devs) != 1 || devs[0].ID != rec.DeviceID {
		t.Fatalf("devices after recovery: %+v", devs)
	}

	// No recovery key set: recovery is refused.
	if code := call(t, ts, "POST", "/api/v1/recover/challenge", "", map[string]string{"callsign": "nobody"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("unknown callsign: %d", code)
	}
}

func mustSummons(t *testing.T, srv *httptest.Server, owner *client) string {
	t.Helper()
	var d domainJSON
	call(t, srv, "POST", "/api/v1/domains", owner.token, map[string]string{"name": "Kin"}, &d)
	var sm struct{ Summons string }
	call(t, srv, "POST", "/api/v1/domains/"+d.ID+"/summons", owner.token, map[string]int{"max_uses": 1}, &sm)
	return sm.Summons
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
