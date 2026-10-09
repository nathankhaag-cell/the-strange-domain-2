package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/blobs"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
)

// Attachments and profile pictures. Attachment bodies are ciphertext the
// node cannot read (see internal/blobs); profile pictures are plain images.

func (s *Server) mediaRoutes() {
	s.mux.Handle("GET /api/v1/limits", s.authed(s.limits))
	s.mux.Handle("POST /api/v1/groups/{gid}/blobs", s.authed(s.uploadBlob))
	s.mux.Handle("GET /api/v1/blobs/{id}", s.authed(s.fetchBlob))

	s.mux.Handle("PUT /api/v1/account/avatar", s.authed(s.setAvatar))
	s.mux.Handle("DELETE /api/v1/account/avatar", s.authed(s.deleteAvatar))
	s.mux.Handle("GET /api/v1/users/{uid}/avatar", s.authed(s.getAvatar))
}

func (s *Server) limits(w http.ResponseWriter, r *http.Request, a auth.Account) {
	used, err := s.blobs.Used(r.Context(), a.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]int64{
		"max_upload_bytes":   s.blobs.MaxSize,
		"quota_bytes":        s.blobs.Quota,
		"used_bytes":         used,
		"max_files":          blobs.MaxPerMessage,
		"max_avatar_bytes":   blobs.MaxAvatarSize,
		"pending_ttl_second": int64(blobs.PendingTTL.Seconds()),
	})
}

// uploadBlob stores an encrypted attachment for a group. The body is the raw
// ciphertext. Only people who may post to the group can upload.
func (s *Server) uploadBlob(w http.ResponseWriter, r *http.Request, a auth.Account) {
	gid := r.PathValue("gid")
	if err := s.relay.CanSend(r.Context(), gid, a.UserID); err != nil {
		writeErr(w, err)
		return
	}
	if r.ContentLength > s.blobs.MaxSize {
		writeErr(w, blobs.ErrTooLarge)
		return
	}
	b, err := s.blobs.Put(r.Context(), a.UserID, gid, r.Body)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"id": b.ID, "size": b.Size})
}

// fetchBlob returns an attachment's ciphertext to a member of its group.
// Until the message carrying it is sent, only its uploader can fetch it.
func (s *Server) fetchBlob(w http.ResponseWriter, r *http.Request, a auth.Account) {
	b, err := s.blobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if b.Seq == 0 && b.OwnerID != a.UserID {
		writeErr(w, domains.ErrNotFound)
		return
	}
	if err := s.relay.CanRead(r.Context(), b.GroupID, a.UserID); err != nil {
		if errors.Is(err, domains.ErrNotMember) || errors.Is(err, domains.ErrForbidden) {
			err = domains.ErrNotFound // don't confirm the blob exists
		}
		writeErr(w, err)
		return
	}
	f, err := s.blobs.Open(b)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", "attachment")
	h.Set("Content-Length", strconv.FormatInt(b.Size, 10))
	// A blob never changes; the client may cache it (it is ciphertext).
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	io.Copy(w, f)
}

func (s *Server) setAvatar(w http.ResponseWriter, r *http.Request, a auth.Account) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, blobs.MaxAvatarSize+1))
	if err != nil || len(data) > blobs.MaxAvatarSize {
		writeErr(w, blobs.ErrTooLarge)
		return
	}
	v, err := s.blobs.SetAvatar(r.Context(), a.UserID, data)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.notifyPeers(r.Context(), a.UserID, "avatar", a.UserID)
	writeJSON(w, map[string]int64{"avatar": v})
}

func (s *Server) deleteAvatar(w http.ResponseWriter, r *http.Request, a auth.Account) {
	if err := s.blobs.DeleteAvatar(r.Context(), a.UserID); err != nil {
		writeErr(w, err)
		return
	}
	s.notifyPeers(r.Context(), a.UserID, "avatar", a.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// getAvatar serves a profile picture to its owner and to anyone who shares a
// domain or Conclave with them.
func (s *Server) getAvatar(w http.ResponseWriter, r *http.Request, a auth.Account) {
	uid := r.PathValue("uid")
	if uid != a.UserID {
		ok, err := s.relay.SharesSpace(r.Context(), a.UserID, uid)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !ok {
			writeErr(w, domains.ErrNotFound)
			return
		}
	}
	av, err := s.blobs.GetAvatar(r.Context(), uid)
	if err != nil {
		writeErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", av.Mime)
	h.Set("Content-Length", strconv.Itoa(len(av.Data)))
	h.Set("ETag", `"`+strconv.FormatInt(av.Version, 10)+`"`)
	// Clients ask for ?v=<version>, so a new picture is a new URL.
	h.Set("Cache-Control", "private, max-age=86400")
	w.Write(av.Data)
}
