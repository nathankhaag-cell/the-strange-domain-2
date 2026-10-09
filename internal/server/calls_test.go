package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/rtc"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

func newCallServer(t *testing.T, video bool) *httptest.Server {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dom := domains.NewService(st)
	hub := relay.NewHub()
	rl := relay.NewService(st, hub)
	sfu, err := NewCallRelay(rtc.Config{Video: video}, rl, hub)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(st, dom, auth.NewService(st, dom), rl, hub, WithCalls(sfu)))
	t.Cleanup(func() {
		ts.Close()
		sfu.Close()
	})
	return ts
}

type callMsg struct {
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Error    string `json:"error"`
	CanSpeak bool   `json:"can_speak"`
	Video    bool   `json:"video"`
	Reason   string `json:"reason"`
	SDP      string `json:"sdp"`
}

// dialCall opens the call socket and sends the hello.
func dialCall(t *testing.T, ts *httptest.Server, hello map[string]any) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/rtc", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatal(err)
	}
	return conn, ctx
}

// readUntil reads call messages until one of type typ.
func readUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, typ string) callMsg {
	t.Helper()
	for {
		var m callMsg
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		if m.Type == typ {
			return m
		}
		if m.Type == "error" && typ != "error" {
			t.Fatalf("waiting for %q: error %d %s", typ, m.Status, m.Error)
		}
	}
}

func TestCallSignallingPermissions(t *testing.T) {
	ts := newCallServer(t, true)
	abbot, _ := signUp(t, ts, "abbot", "")
	var d domainJSON
	call(t, ts, "POST", "/api/v1/domains", abbot.token, map[string]string{"name": "Sector 7"}, &d)
	var sm struct{ Summons string }
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/summons", abbot.token, map[string]int{"max_uses": 5}, &sm)
	brother, _ := signUp(t, ts, "brother", sm.Summons)
	novice, _ := signUp(t, ts, "novice", sm.Summons)
	outsider, _ := signUp(t, ts, "outsider", mustSummons(t, ts, abbot))

	var detail struct {
		Channels []struct{ ID, Kind string } `json:"channels"`
		Roles    []struct{ ID, Name string } `json:"roles"`
	}
	call(t, ts, "GET", "/api/v1/domains/"+d.ID, abbot.token, nil, &detail)
	var chapel, voice, postulant string
	for _, c := range detail.Channels {
		if c.Kind == "voice" {
			voice = c.ID
		} else {
			chapel = c.ID
		}
	}
	for _, r := range detail.Roles {
		if r.Name == "Postulant" {
			postulant = r.ID
		}
	}

	var info struct{ Features []string }
	call(t, ts, "GET", "/api/v1/info", "", nil, &info)
	if !slices.Contains(info.Features, "calls") || !slices.Contains(info.Features, "video") {
		t.Fatalf("features: %v", info.Features)
	}

	// No token, a bad token: the socket is closed.
	conn, ctx := dialCall(t, ts, map[string]any{"token": "nope", "room": voice})
	var v any
	if err := wsjson.Read(ctx, conn, &v); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("bad token: %v", err)
	}

	refused := func(who *client, room string, status int) {
		t.Helper()
		conn, ctx := dialCall(t, ts, map[string]any{"token": who.token, "room": room})
		if m := readUntil(t, ctx, conn, "error"); m.Status != status {
			t.Fatalf("join %s: got %d (%s), want %d", room, m.Status, m.Error, status)
		}
	}
	refused(outsider, voice, http.StatusForbidden)  // not in the domain
	refused(brother, chapel, http.StatusBadRequest) // Chapels have no calls
	refused(brother, "no-such-room", http.StatusNotFound)

	// A Postulant's role has no JoinVoice.
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+novice.userID+"/role", abbot.token,
		map[string]string{"role_id": postulant}, nil)
	refused(novice, voice, http.StatusForbidden)

	// A member joins and gets an offer; the room shows up for domain members only.
	bconn, bctx := dialCall(t, ts, map[string]any{"token": brother.token, "room": voice})
	w := readUntil(t, bctx, bconn, "welcome")
	if !w.CanSpeak || !w.Video {
		t.Fatalf("welcome: %+v", w)
	}
	if o := readUntil(t, bctx, bconn, "offer"); !strings.Contains(o.SDP, "m=audio") || !strings.Contains(o.SDP, "m=video") {
		t.Fatalf("offer:\n%s", o.SDP)
	}
	var rooms []rtc.RoomInfo
	call(t, ts, "GET", "/api/v1/calls", abbot.token, nil, &rooms)
	if len(rooms) != 1 || rooms[0].ID != voice || !rooms[0].Relay || len(rooms[0].Participants) != 1 ||
		rooms[0].Participants[0].UserID != brother.userID {
		t.Fatalf("rooms: %+v", rooms)
	}
	call(t, ts, "GET", "/api/v1/calls", outsider.token, nil, &rooms)
	if len(rooms) != 0 {
		t.Fatalf("an outsider sees the room: %+v", rooms)
	}

	// Muting them in the domain makes them listen-only at once.
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+brother.userID+"/mute", abbot.token, nil, nil)
	if m := readUntil(t, bctx, bconn, "speak"); m.CanSpeak {
		t.Fatal("still allowed to speak after being muted")
	}
	// A muted member can still join (to listen).
	mconn, mctx := dialCall(t, ts, map[string]any{"token": brother.token, "room": voice})
	if m := readUntil(t, mctx, mconn, "welcome"); m.CanSpeak {
		t.Fatal("a muted member joined able to speak")
	}
	mconn.Close(websocket.StatusNormalClosure, "")

	// Kicked from the domain: dropped from the call.
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/members/"+brother.userID+"/kick", abbot.token, nil, nil)
	if m := readUntil(t, bctx, bconn, "bye"); m.Reason == "" {
		t.Fatal("no reason given")
	}
}

func TestConclaveCallRingsAndDeclines(t *testing.T) {
	ts := newCallServer(t, false)
	abbot, _ := signUp(t, ts, "abbot", "")
	brother, _ := signUp(t, ts, "brother", mustSummons(t, ts, abbot))
	var conclave struct{ ID string }
	if code := call(t, ts, "POST", "/api/v1/conclaves", abbot.token, map[string]any{"member_ids": []string{brother.userID}}, &conclave); code != 201 {
		t.Fatalf("conclave: %d", code)
	}
	var info struct{ Features []string }
	call(t, ts, "GET", "/api/v1/info", "", nil, &info)
	if !slices.Contains(info.Features, "calls") || slices.Contains(info.Features, "video") {
		t.Fatalf("features with video off: %v", info.Features)
	}

	// Brother listens for events.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.CloseNow()
	wsjson.Write(ctx, stream, map[string]string{"token": brother.token})
	var ready map[string]string
	wsjson.Read(ctx, stream, &ready)

	aconn, actx := dialCall(t, ts, map[string]any{"token": abbot.token, "room": conclave.ID, "ring": true, "video": true})
	readUntil(t, actx, aconn, "welcome")
	o := readUntil(t, actx, aconn, "offer")
	if strings.Contains(o.SDP, "m=video") {
		t.Fatal("video offered with -rtc-video=false")
	}
	for {
		var ev relay.Event
		if err := wsjson.Read(ctx, stream, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == "call" && ev.GroupID == conclave.ID {
			break
		}
	}
	var rooms []rtc.RoomInfo
	call(t, ts, "GET", "/api/v1/calls", brother.token, nil, &rooms)
	if len(rooms) != 1 || !rooms[0].Ringing || rooms[0].Relay || rooms[0].Video || rooms[0].StartedBy != abbot.userID {
		t.Fatalf("rooms: %+v", rooms)
	}
	if code := call(t, ts, "POST", "/api/v1/calls/"+conclave.ID+"/decline", brother.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("decline: %d", code)
	}
	readUntil(t, actx, aconn, "declined")
	// Someone outside the Conclave cannot decline (or see) it.
	other, _ := signUp(t, ts, "other", mustSummons(t, ts, abbot))
	if code := call(t, ts, "POST", "/api/v1/calls/"+conclave.ID+"/decline", other.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("outsider decline: %d", code)
	}
	// The call ends when its last participant leaves.
	aconn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		call(t, ts, "GET", "/api/v1/calls", brother.token, nil, &rooms)
		if len(rooms) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not end when its only participant left")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCallsOffByDefault(t *testing.T) {
	ts := newTestServer(t)
	abbot, _ := signUp(t, ts, "abbot", "")
	if code := call(t, ts, "GET", "/api/v1/calls", abbot.token, nil, nil); code != http.StatusNotFound {
		t.Fatalf("calls endpoint without calls: %d", code)
	}
}
