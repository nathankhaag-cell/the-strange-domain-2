package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/rtc"
)

// Voice and video calls (see internal/rtc). The node relays media it cannot
// decrypt: clients encrypt every frame end to end with keys from the call's
// MLS group.

// WithCalls turns on calls, relayed by sfu.
func WithCalls(sfu *rtc.SFU) Option {
	return func(s *Server) { s.rtc = sfu }
}

// NewCallRelay makes the media relay with the node's access rules: Voice
// Relays for domain members whose role may join voice (members muted in the
// domain listen only), and Conclave calls for the Conclave's members.
func NewCallRelay(cfg rtc.Config, rl *relay.Service, hub *relay.Hub) (*rtc.SFU, error) {
	access := func(ctx context.Context, room, userID, deviceID string) (rtc.Access, error) {
		a, err := rl.CallAccess(ctx, room, userID, deviceID)
		return rtc.Access{Relay: a.Relay, CanSpeak: a.CanSpeak, Audience: a.Audience}, err
	}
	notify := func(users []string, room string) {
		hub.Notify(users, relay.Event{Type: "call", GroupID: room})
	}
	return rtc.New(cfg, access, notify)
}

func (s *Server) callRoutes() {
	if s.rtc == nil {
		return
	}
	s.mux.Handle("GET /api/v1/calls", s.authed(s.listCalls))
	s.mux.Handle("POST /api/v1/calls/{gid}/decline", s.authed(s.declineCall))
	s.mux.HandleFunc("GET /api/v1/rtc", s.callSocket)
}

// features lists what this node supports, including calls when they are on.
func (s *Server) features() []string {
	out := append([]string{}, Features...)
	if s.rtc != nil {
		out = append(out, "calls")
		if s.rtc.VideoEnabled() {
			out = append(out, "video")
		}
	}
	return out
}

// CallStats returns live call counters; ok is false when calls are off.
func (s *Server) CallStats() (st rtc.Stats, ok bool) {
	if s.rtc == nil {
		return st, false
	}
	return s.rtc.Stats(), true
}

// recheckCalls applies membership, role and mute changes to running calls.
func (s *Server) recheckCalls() {
	if s.rtc == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.rtc.Recheck(ctx)
	}()
}

// listCalls returns the running calls the caller may join: Voice Relays in
// their domains and calls in their Conclaves.
func (s *Server) listCalls(w http.ResponseWriter, r *http.Request, a auth.Account) {
	out := []rtc.RoomInfo{}
	for _, room := range s.rtc.Rooms() {
		if _, err := s.relay.CallAccess(r.Context(), room.ID, a.UserID, a.DeviceID); err == nil {
			out = append(out, room)
		}
	}
	writeJSON(w, out)
}

func (s *Server) declineCall(w http.ResponseWriter, r *http.Request, a auth.Account) {
	gid := r.PathValue("gid")
	if _, err := s.relay.CallAccess(r.Context(), gid, a.UserID, a.DeviceID); err != nil {
		writeErr(w, err)
		return
	}
	if !s.rtc.Decline(gid, a.UserID) {
		writeErr(w, domains.ErrNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// callSocket is one participant's signalling WebSocket. The first frame is
// {"token": "...", "room": "<group id>", "ring": bool, "video": bool}; then
// the node sends offers and room updates and the client answers (see
// internal/rtc).
func (s *Server) callSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same as /api/v1/stream: auth is the in-band token, never cookies.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 10)

	authCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	var hello struct {
		Token string `json:"token"`
		rtc.Hello
	}
	err = wsjson.Read(authCtx, conn, &hello)
	cancel()
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, "expected token")
		return
	}
	acct, err := s.auth.Authenticate(r.Context(), hello.Token)
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, "not signed in")
		return
	}
	wc := &wsConn{c: conn, ctx: r.Context()}
	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	go wc.keepAlive(ctx)
	err = s.rtc.Join(ctx, wc, acct.UserID, acct.DeviceID, hello.Hello)
	if err != nil {
		msg := err.Error()
		var status int
		switch {
		case errors.Is(err, domains.ErrForbidden), errors.Is(err, domains.ErrNotMember):
			status = http.StatusForbidden
		case errors.Is(err, domains.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, domains.ErrInvalidInput), errors.Is(err, rtc.ErrBadHello):
			status = http.StatusBadRequest
		case errors.Is(err, rtc.ErrRoomFull):
			status = http.StatusConflict
		default:
			status = http.StatusInternalServerError
			msg = "could not join the call"
		}
		_ = wc.Send(map[string]any{"type": "error", "status": status, "error": msg})
		conn.Close(websocket.StatusPolicyViolation, "refused")
		return
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

type wsConn struct {
	c   *websocket.Conn
	ctx context.Context
	mu  sync.Mutex
}

func (w *wsConn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	return w.c.Write(ctx, websocket.MessageText, b)
}

func (w *wsConn) Recv(ctx context.Context) ([]byte, error) {
	_, b, err := w.c.Read(ctx)
	return b, err
}

func (w *wsConn) keepAlive(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := w.c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
