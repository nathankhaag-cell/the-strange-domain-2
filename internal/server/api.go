package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/blobs"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
)

// API vocabulary follows the approved theme where users see it:
// invites are "summons" and the default roles carry the approved names.

const maxBody = 64 << 10

type ctxKey struct{}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/register", s.register)
	s.mux.HandleFunc("POST /api/v1/auth/challenge", s.challenge)
	s.mux.HandleFunc("POST /api/v1/auth/verify", s.verify)
	s.mux.Handle("POST /api/v1/auth/signout", s.authed(s.signOut))

	s.mux.Handle("GET /api/v1/me", s.authed(s.me))
	s.mux.Handle("GET /api/v1/domains", s.authed(s.listDomains))
	s.mux.Handle("POST /api/v1/domains", s.authed(s.createDomain))
	s.mux.Handle("GET /api/v1/domains/{id}", s.authed(s.getDomain))
	s.mux.Handle("POST /api/v1/domains/{id}/channels", s.authed(s.createChannel))
	s.mux.Handle("POST /api/v1/domains/{id}/summons", s.authed(s.createSummons))
	s.mux.Handle("POST /api/v1/summons/{token}/accept", s.authed(s.acceptSummons))
	s.mux.Handle("POST /api/v1/domains/{id}/members/{uid}/role", s.authed(s.assignRole))
	s.mux.Handle("POST /api/v1/domains/{id}/members/{uid}/mute", s.authed(s.mute(true)))
	s.mux.Handle("POST /api/v1/domains/{id}/members/{uid}/unmute", s.authed(s.mute(false)))
	s.mux.Handle("POST /api/v1/domains/{id}/members/{uid}/kick", s.authed(s.kick))
	s.mux.Handle("POST /api/v1/domains/{id}/members/{uid}/ban", s.authed(s.ban))
	s.mux.Handle("GET /api/v1/domains/{id}/bans", s.authed(s.listBans))
	s.mux.Handle("DELETE /api/v1/domains/{id}/bans/{uid}", s.authed(s.unban))
}

// authed requires a valid "Authorization: Bearer <token>" header.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, auth.Account)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, auth.ErrUnauthorized)
			return
		}
		acct, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			writeErr(w, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, token)), acct)
	})
}

// ---- accounts ----

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Callsign   string `json:"callsign"`
		DeviceName string `json:"device_name"`
		PublicKey  string `json:"public_key"` // base64 Ed25519
		Summons    string `json:"summons"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	acct, err := s.auth.Register(r.Context(), req.Callsign, req.DeviceName, ed25519.PublicKey(pub), req.Summons)
	if err != nil {
		writeErr(w, err)
		return
	}
	if req.Summons != "" {
		// A new account belongs to exactly the one domain its Summons joined.
		if ds, err := s.dom.DomainsForUser(r.Context(), acct.UserID); err == nil {
			for _, d := range ds {
				s.notifyDomain(r.Context(), d.ID)
			}
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"user_id": acct.UserID, "device_id": acct.DeviceID, "is_node_admin": acct.IsNodeAdmin})
}

func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	nonce, err := s.auth.Challenge(r.Context(), req.DeviceID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"nonce": base64.StdEncoding.EncodeToString(nonce)})
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID  string `json:"device_id"`
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	nonce, err1 := base64.StdEncoding.DecodeString(req.Nonce)
	sig, err2 := base64.StdEncoding.DecodeString(req.Signature)
	if err1 != nil || err2 != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	token, err := s.auth.Verify(r.Context(), req.DeviceID, nonce, sig)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"token": token})
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	if err := s.auth.SignOut(r.Context(), r.Context().Value(ctxKey{}).(string)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, a auth.Account) {
	av, err := s.blobs.AvatarVersions(r.Context(), []string{a.UserID})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"user_id": a.UserID, "device_id": a.DeviceID, "callsign": a.Callsign,
		"is_node_admin": a.IsNodeAdmin, "avatar": av[a.UserID]})
}

// ---- domains ----

type domainJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnerID string `json:"owner_id"`
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request, a auth.Account) {
	ds, err := s.dom.DomainsForUser(r.Context(), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]domainJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, domainJSON{d.ID, d.Name, d.OwnerID})
	}
	writeJSON(w, out)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	d, err := s.dom.CreateDomain(r.Context(), a.UserID, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, domainJSON{d.ID, d.Name, d.OwnerID})
}

func (s *Server) getDomain(w http.ResponseWriter, r *http.Request, a auth.Account) {
	ctx, id := r.Context(), r.PathValue("id")
	if err := s.dom.RequireMember(ctx, id, a.UserID); err != nil {
		writeErr(w, err)
		return
	}
	roles, err := s.dom.Roles(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	members, err := s.dom.Members(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	channels, err := s.dom.Channels(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	type roleJSON struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Rank        int    `json:"rank"`
		Permissions uint64 `json:"permissions"`
	}
	type memberJSON struct {
		UserID   string `json:"user_id"`
		Callsign string `json:"callsign"`
		RoleID   string `json:"role_id"`
		Muted    bool   `json:"muted"`
		Avatar   int64  `json:"avatar,omitempty"`
	}
	type channelJSON struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	resp := struct {
		Roles    []roleJSON    `json:"roles"`
		Members  []memberJSON  `json:"members"`
		Channels []channelJSON `json:"channels"`
	}{[]roleJSON{}, []memberJSON{}, []channelJSON{}}
	for _, x := range roles {
		resp.Roles = append(resp.Roles, roleJSON{x.ID, x.Name, x.Rank, uint64(x.Permissions)})
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	avatars, err := s.blobs.AvatarVersions(ctx, ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, m := range members {
		resp.Members = append(resp.Members, memberJSON{m.UserID, m.Callsign, m.Role.ID, m.Muted, avatars[m.UserID]})
	}
	for _, c := range channels {
		resp.Channels = append(resp.Channels, channelJSON{c.ID, c.Name, c.Kind})
	}
	writeJSON(w, resp)
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		Name string `json:"name"`
		Kind string `json:"kind"` // "text" (Chapel) or "voice" (Voice Relay)
	}
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.dom.CreateChannel(r.Context(), r.PathValue("id"), a.UserID, req.Name, req.Kind)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.notifyDomain(r.Context(), r.PathValue("id"))
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"id": c.ID, "name": c.Name, "kind": c.Kind})
}

func (s *Server) createSummons(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		MaxUses    int `json:"max_uses"`
		TTLSeconds int `json:"ttl_seconds"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.MaxUses < 0 || req.TTLSeconds < 0 {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	token, err := s.dom.CreateInvite(r.Context(), r.PathValue("id"), a.UserID, req.MaxUses,
		time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"summons": token})
}

func (s *Server) acceptSummons(w http.ResponseWriter, r *http.Request, a auth.Account) {
	d, err := s.dom.JoinByInvite(r.Context(), r.PathValue("token"), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.notifyDomain(r.Context(), d.ID)
	writeJSON(w, domainJSON{d.ID, d.Name, d.OwnerID})
}

// ---- moderation ----

func (s *Server) assignRole(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		RoleID string `json:"role_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.domainDone(w, r, s.dom.AssignRole(r.Context(), r.PathValue("id"), a.UserID, r.PathValue("uid"), req.RoleID))
}

func (s *Server) mute(muted bool) func(http.ResponseWriter, *http.Request, auth.Account) {
	return func(w http.ResponseWriter, r *http.Request, a auth.Account) {
		s.domainDone(w, r, s.dom.SetMuted(r.Context(), r.PathValue("id"), a.UserID, r.PathValue("uid"), muted))
	}
}

func (s *Server) kick(w http.ResponseWriter, r *http.Request, a auth.Account) {
	s.domainDone(w, r, s.dom.Kick(r.Context(), r.PathValue("id"), a.UserID, r.PathValue("uid")), r.PathValue("uid"))
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		Reason string `json:"reason"`
		// DurationSeconds 0 (or absent) bans permanently.
		DurationSeconds int64 `json:"duration_seconds"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &req) {
		return
	}
	if req.DurationSeconds < 0 || req.DurationSeconds > maxBanSeconds {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	err := s.dom.Ban(r.Context(), r.PathValue("id"), a.UserID, r.PathValue("uid"), req.Reason,
		time.Duration(req.DurationSeconds)*time.Second)
	s.domainDone(w, r, err, r.PathValue("uid"))
}

// maxBanSeconds caps a temporary ban at ten years; use 0 for permanent.
const maxBanSeconds = 10 * 365 * 24 * 3600

func (s *Server) listBans(w http.ResponseWriter, r *http.Request, a auth.Account) {
	bans, err := s.dom.Bans(r.Context(), r.PathValue("id"), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	type banJSON struct {
		UserID     string `json:"user_id"`
		Callsign   string `json:"callsign"`
		BannedBy   string `json:"banned_by"`
		Reason     string `json:"reason"`
		BannedAt   int64  `json:"banned_at"`
		ExpiresAt  int64  `json:"expires_at"` // 0: permanent
		FormerRank int    `json:"former_rank"`
	}
	out := make([]banJSON, 0, len(bans))
	for _, b := range bans {
		out = append(out, banJSON{b.UserID, b.Callsign, b.BannedBy, b.Reason, b.BannedAt, b.ExpiresAt, b.FormerRank})
	}
	writeJSON(w, out)
}

// SweepBans lifts bans whose time is up and tells those domains' members.
// Joining checks expiry too, so this only keeps ban lists and the audit log current.
func (s *Server) SweepBans(ctx context.Context) error {
	ids, err := s.dom.SweepExpiredBans(ctx)
	for _, id := range ids {
		s.notifyDomain(ctx, id)
	}
	return err
}

// RunBanSweep calls SweepBans every interval until ctx is done.
func (s *Server) RunBanSweep(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.SweepBans(ctx)
		}
	}
}

func (s *Server) unban(w http.ResponseWriter, r *http.Request, a auth.Account) {
	s.domainDone(w, r, s.dom.Unban(r.Context(), r.PathValue("id"), a.UserID, r.PathValue("uid")))
}

// ---- live updates ----

// notifyDomain tells a domain's members (and any extra users, such as someone
// just kicked) that its members, roles or channels changed. Clients refetch the
// domain and, for membership changes, add or remove devices in their MLS groups.
func (s *Server) notifyDomain(ctx context.Context, domainID string, extra ...string) {
	members, err := s.dom.Members(ctx, domainID)
	if err != nil {
		return
	}
	ids := append([]string{}, extra...)
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	s.hub.Notify(ids, relay.Event{Type: "domain", GroupID: domainID})
}

// domainDone finishes a moderation request and, on success, notifies the domain.
func (s *Server) domainDone(w http.ResponseWriter, r *http.Request, err error, extra ...string) {
	if err == nil {
		s.notifyDomain(r.Context(), r.PathValue("id"), extra...)
		s.recheckCalls() // kicks, bans, mutes and role changes apply to calls too
	}
	done(w, err)
}

// ---- helpers ----

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONLimit(w, r, v, maxBody)
}

func readJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func done(w http.ResponseWriter, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeErr maps known errors to status codes and hides internal ones.
func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, domains.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, domains.ErrForbidden), errors.Is(err, domains.ErrNotMember),
		errors.Is(err, domains.ErrBanned), errors.Is(err, auth.ErrSummonsRequired):
		status = http.StatusForbidden
	case errors.Is(err, domains.ErrNotFound), errors.Is(err, domains.ErrInvalidInvite):
		status = http.StatusNotFound
	case errors.Is(err, domains.ErrAlreadyMember), errors.Is(err, auth.ErrCallsignTaken),
		errors.Is(err, auth.ErrDeviceKnown), errors.Is(err, relay.ErrStaleEpoch):
		status = http.StatusConflict
	case errors.Is(err, auth.ErrTooManyDevices):
		status = http.StatusForbidden
	case errors.Is(err, relay.ErrNoKeys):
		status = http.StatusNotFound
	case errors.Is(err, relay.ErrTooLarge), errors.Is(err, blobs.ErrTooLarge), errors.Is(err, blobs.ErrQuota):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, blobs.ErrTooPending):
		status = http.StatusTooManyRequests
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "internal error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
