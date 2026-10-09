package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
)

// Binary MLS payloads travel as base64 strings in JSON ([]byte fields).

func (s *Server) relayRoutes() {
	s.mux.Handle("POST /api/v1/keypackages", s.authed(s.publishKeyPackages))
	s.mux.Handle("POST /api/v1/users/{uid}/keypackages/claim", s.authed(s.claimKeyPackages))

	s.mux.Handle("GET /api/v1/conclaves", s.authed(s.listConclaves))
	s.mux.Handle("POST /api/v1/conclaves", s.authed(s.createConclave))
	s.mux.Handle("DELETE /api/v1/conclaves/{id}/members/me", s.authed(s.leaveConclave))

	s.mux.Handle("GET /api/v1/groups/{gid}/epoch", s.authed(s.groupEpoch))
	s.mux.Handle("GET /api/v1/groups/{gid}/devices", s.authed(s.groupDevices))
	s.mux.Handle("GET /api/v1/groups/{gid}/messages", s.authed(s.fetchMessages))
	s.mux.Handle("POST /api/v1/groups/{gid}/messages", s.authed(s.sendMessage))
	s.mux.Handle("DELETE /api/v1/groups/{gid}/messages/{seq}", s.authed(s.deleteMessage))
	s.mux.Handle("POST /api/v1/groups/{gid}/welcomes", s.authed(s.sendWelcome))
	s.mux.Handle("POST /api/v1/welcomes/take", s.authed(s.takeWelcomes))

	s.mux.HandleFunc("GET /api/v1/stream", s.stream)
}

func (s *Server) publishKeyPackages(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		KeyPackages [][]byte `json:"key_packages"`
	}
	if !readJSONLimit(w, r, &req, 2<<20) {
		return
	}
	if err := s.relay.PublishKeyPackages(r.Context(), a.DeviceID, req.KeyPackages); err != nil {
		writeErr(w, err)
		return
	}
	// Peers whose clients tried to add this person's devices to an MLS group
	// while they had no KeyPackages can now retry.
	s.notifyPeers(r.Context(), a.UserID, "keys", a.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) claimKeyPackages(w http.ResponseWriter, r *http.Request, a auth.Account) {
	kps, err := s.relay.ClaimKeyPackages(r.Context(), a.UserID, r.PathValue("uid"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, kps)
}

func (s *Server) createConclave(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		MemberIDs []string `json:"member_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	id, err := s.relay.CreateConclave(r.Context(), a.UserID, req.MemberIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.notifyConclave(r.Context(), id)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": id})
}

func (s *Server) leaveConclave(w http.ResponseWriter, r *http.Request, a auth.Account) {
	err := s.relay.LeaveConclave(r.Context(), r.PathValue("id"), a.UserID)
	if err == nil {
		s.notifyConclave(r.Context(), r.PathValue("id"), a.UserID)
	}
	done(w, err)
}

func (s *Server) listConclaves(w http.ResponseWriter, r *http.Request, a auth.Account) {
	cs, err := s.relay.Conclaves(r.Context(), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	var ids []string
	for _, c := range cs {
		for _, m := range c.Members {
			ids = append(ids, m.UserID)
		}
	}
	avatars, err := s.blobs.AvatarVersions(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	for i := range cs {
		for j := range cs[i].Members {
			cs[i].Members[j].Avatar = avatars[cs[i].Members[j].UserID]
		}
	}
	writeJSON(w, cs)
}

// notifyConclave tells a Conclave's members (plus extra users) that its membership changed.
func (s *Server) notifyConclave(ctx context.Context, id string, extra ...string) {
	ids, err := s.relay.ConclaveMemberIDs(ctx, id)
	if err != nil {
		return
	}
	s.hub.Notify(append(ids, extra...), relay.Event{Type: "conclave", GroupID: id})
}

func (s *Server) groupEpoch(w http.ResponseWriter, r *http.Request, a auth.Account) {
	epoch, err := s.relay.Epoch(r.Context(), r.PathValue("gid"), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]int64{"epoch": epoch})
}

func (s *Server) groupDevices(w http.ResponseWriter, r *http.Request, a auth.Account) {
	ds, err := s.relay.GroupDevices(r.Context(), r.PathValue("gid"), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, ds)
}

// notifyPeers tells userID and everyone sharing a space with them that
// userID's devices changed, so their clients update MLS groups.
func (s *Server) notifyPeers(ctx context.Context, userID, typ, detail string) {
	peers, err := s.relay.Peers(ctx, userID)
	if err != nil {
		return
	}
	s.hub.Notify(peers, relay.Event{Type: typ, GroupID: detail})
}

func (s *Server) fetchMessages(w http.ResponseWriter, r *http.Request, a auth.Account) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := s.relay.Fetch(r.Context(), r.PathValue("gid"), a.UserID, after, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, msgs)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		Kind  string `json:"kind"`
		Epoch int64  `json:"epoch"`
		Data  []byte `json:"data"`
		// Blobs lists attachments uploaded for this message; they become
		// readable by the group and are deleted with the message.
		Blobs []string `json:"blobs,omitempty"`
	}
	if !readJSONLimit(w, r, &req, relay.MaxMessageSize*2) {
		return
	}
	gid := r.PathValue("gid")
	if len(req.Blobs) > 0 {
		if req.Kind != relay.KindApplication {
			writeErr(w, domains.ErrInvalidInput)
			return
		}
		if err := s.blobs.CheckPending(r.Context(), a.UserID, gid, req.Blobs); err != nil {
			writeErr(w, err)
			return
		}
	}
	m, err := s.relay.Send(r.Context(), gid, a.UserID, a.DeviceID, req.Kind, req.Epoch, req.Data)
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(req.Blobs) > 0 {
		if err := s.blobs.Attach(r.Context(), a.UserID, gid, m.Seq, req.Blobs); err != nil {
			writeErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]int64{"seq": m.Seq})
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request, a auth.Account) {
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	if err := s.relay.Delete(r.Context(), r.PathValue("gid"), seq, a.UserID); err != nil {
		writeErr(w, err)
		return
	}
	// The message is gone; so are its attachments. A failure here leaves
	// them for the sweep, which removes blobs of deleted messages.
	_ = s.blobs.DeleteForMessage(r.Context(), r.PathValue("gid"), seq)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sendWelcome(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		DeviceID string `json:"device_id"`
		Data     []byte `json:"data"`
	}
	if !readJSONLimit(w, r, &req, relay.MaxMessageSize*2) {
		return
	}
	done(w, s.relay.SendWelcome(r.Context(), r.PathValue("gid"), a.UserID, req.DeviceID, req.Data))
}

func (s *Server) takeWelcomes(w http.ResponseWriter, r *http.Request, a auth.Account) {
	ws, err := s.relay.TakeWelcomes(r.Context(), a.DeviceID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, ws)
}

// stream is a WebSocket that pushes Events. The client's first frame must be
// {"token": "<session token>"}; browsers can't set headers on WebSockets and
// tokens don't belong in URLs.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Auth is by an in-band token, not cookies, so cross-origin requests
		// can't ride on a user's session. Desktop and phone apps connect from
		// non-web origins, so the origin check is off.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 10)

	authCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	var hello struct {
		Token string `json:"token"`
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

	events, unsubscribe := s.hub.Subscribe(acct.UserID)
	defer unsubscribe()
	ctx := conn.CloseRead(r.Context()) // we only write from here on
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "ready"}); err != nil {
		return
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				conn.Close(websocket.StatusTryAgainLater, "too slow; resync")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(wctx, conn, ev)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}
