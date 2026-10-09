package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
)

func (s *Server) deviceRoutes() {
	s.mux.Handle("GET /api/v1/devices", s.authed(s.listDevices))
	s.mux.Handle("POST /api/v1/devices/link-code", s.authed(s.newLinkCode))
	s.mux.HandleFunc("POST /api/v1/devices/link", s.linkDevice)
	s.mux.Handle("DELETE /api/v1/devices/{id}", s.authed(s.revokeDevice))

	s.mux.Handle("PUT /api/v1/account/recovery-key", s.authed(s.setRecoveryKey))
	s.mux.HandleFunc("POST /api/v1/recover/challenge", s.recoveryChallenge)
	s.mux.HandleFunc("POST /api/v1/recover", s.recover)

	s.mux.Handle("POST /api/v1/devices/{id}/transfers", s.authed(s.sendTransfer))
	s.mux.Handle("GET /api/v1/transfers", s.authed(s.listTransfers))
	s.mux.Handle("POST /api/v1/transfers/{id}/take", s.authed(s.takeTransfer))
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, a auth.Account) {
	ds, err := s.auth.Devices(r.Context(), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, ds)
}

func (s *Server) newLinkCode(w http.ResponseWriter, r *http.Request, a auth.Account) {
	code, err := s.auth.NewLinkCode(r.Context(), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"code": code, "expires_in_seconds": 600})
}

func (s *Server) linkDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
		PublicKey  string `json:"public_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	acct, err := s.auth.LinkDevice(r.Context(), req.Code, req.DeviceName, ed25519.PublicKey(pub))
	if err != nil {
		writeErr(w, err)
		return
	}
	// The person's other devices add the new one to their MLS groups and may
	// offer it their history.
	s.hub.Notify([]string{acct.UserID}, relay.Event{Type: "device_linked", GroupID: acct.DeviceID})
	s.notifyPeers(r.Context(), acct.UserID, "devices", acct.UserID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"user_id": acct.UserID, "device_id": acct.DeviceID, "callsign": acct.Callsign})
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request, a auth.Account) {
	id := r.PathValue("id")
	if err := s.auth.RevokeDevice(r.Context(), a.UserID, id); err != nil {
		writeErr(w, err)
		return
	}
	s.hub.Notify([]string{a.UserID}, relay.Event{Type: "device_revoked", GroupID: id})
	s.notifyPeers(r.Context(), a.UserID, "devices", a.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setRecoveryKey(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		PublicKey string `json:"public_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	done(w, s.auth.SetRecoveryKey(r.Context(), a.UserID, ed25519.PublicKey(pub)))
}

func (s *Server) recoveryChallenge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Callsign string `json:"callsign"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	nonce, err := s.auth.RecoveryChallenge(r.Context(), req.Callsign)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"nonce": base64.StdEncoding.EncodeToString(nonce)})
}

func (s *Server) recover(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Callsign   string `json:"callsign"`
		Nonce      string `json:"nonce"`
		Signature  string `json:"signature"`
		DeviceName string `json:"device_name"`
		PublicKey  string `json:"public_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	nonce, e1 := base64.StdEncoding.DecodeString(req.Nonce)
	sig, e2 := base64.StdEncoding.DecodeString(req.Signature)
	pub, e3 := base64.StdEncoding.DecodeString(req.PublicKey)
	if e1 != nil || e2 != nil || e3 != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	acct, err := s.auth.Recover(r.Context(), req.Callsign, nonce, sig, req.DeviceName, ed25519.PublicKey(pub))
	if err != nil {
		writeErr(w, err)
		return
	}
	// The lost devices are gone; peers' clients remove them from MLS groups.
	s.notifyPeers(r.Context(), acct.UserID, "devices", acct.UserID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"user_id": acct.UserID, "device_id": acct.DeviceID, "callsign": acct.Callsign})
}

func (s *Server) sendTransfer(w http.ResponseWriter, r *http.Request, a auth.Account) {
	var req struct {
		TransferID string `json:"transfer_id"`
		Chunk      int    `json:"chunk"`
		Total      int    `json:"total"`
		Data       []byte `json:"data"`
	}
	if !readJSONLimit(w, r, &req, relay.MaxTransferChunk*2) {
		return
	}
	done(w, s.relay.SendTransferChunk(r.Context(), a.UserID, a.DeviceID, r.PathValue("id"),
		req.TransferID, req.Chunk, req.Total, req.Data))
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request, a auth.Account) {
	cs, err := s.relay.PendingTransfers(r.Context(), a.DeviceID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, cs)
}

func (s *Server) takeTransfer(w http.ResponseWriter, r *http.Request, a auth.Account) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, domains.ErrInvalidInput)
		return
	}
	c, err := s.relay.TakeTransferChunk(r.Context(), a.DeviceID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}
