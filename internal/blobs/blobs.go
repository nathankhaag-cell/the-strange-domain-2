// Package blobs stores attachments and profile pictures.
//
// Attachments are encrypted on the sender's device (AES-GCM with a fresh key
// per file); the key, a hash and the file's name and type travel inside the
// MLS message, so the node only ever holds opaque ciphertext. Ciphertext is
// kept on disk under <data>/blobs, one file per blob, with a row in the
// database recording who uploaded it, for which group, and which message it
// belongs to.
//
// Who may upload to or read a group is decided by the caller (the relay's
// access rules); this package enforces sizes, quotas and cleanup.
package blobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

var (
	ErrTooLarge   = errors.New("file too large")
	ErrQuota      = errors.New("upload quota used up; delete some files first")
	ErrTooPending = errors.New("too many uploads waiting to be sent")
)

const (
	// DefaultMaxSize is the largest attachment accepted unless configured.
	DefaultMaxSize = 25 << 20
	// DefaultQuota is how much attachment data one person may keep on the node.
	DefaultQuota = 1 << 30
	// MaxPending is how many uploaded-but-unsent blobs one person may have.
	MaxPending = 20
	// PendingTTL is how long an uploaded blob may wait for its message.
	PendingTTL = time.Hour
	// MaxPerMessage is how many blobs one message may carry.
	MaxPerMessage = 10
	sweepEvery    = 10 * time.Minute
)

// Blob is one stored attachment.
type Blob struct {
	ID      string
	OwnerID string
	GroupID string
	Size    int64
	Seq     int64 // 0 until attached to a message
}

type Service struct {
	st      *store.Store
	dir     string
	MaxSize int64
	Quota   int64
	now     func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func NewService(st *store.Store) *Service {
	return &Service{st: st, dir: filepath.Join(st.Dir, "blobs"), MaxSize: DefaultMaxSize, Quota: DefaultQuota, now: time.Now}
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) path(id string) string {
	return filepath.Join(s.dir, id[:2], id)
}

// Used returns how many bytes of attachments userID keeps on the node.
func (s *Service) Used(ctx context.Context, userID string) (int64, error) {
	var used int64
	err := s.st.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM blobs WHERE owner_id = ?`, userID).Scan(&used)
	return used, err
}

// Put stores up to MaxSize bytes of ciphertext from r as a new blob for
// groupID. The caller must already have checked userID may send there.
func (s *Service) Put(ctx context.Context, userID, groupID string, r io.Reader) (Blob, error) {
	s.maybeSweep(ctx)
	var used int64
	var pending int
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size), 0), COUNT(CASE WHEN seq IS NULL THEN 1 END) FROM blobs WHERE owner_id = ?`, userID).
		Scan(&used, &pending); err != nil {
		return Blob{}, err
	}
	if pending >= MaxPending {
		return Blob{}, ErrTooPending
	}
	limit := s.MaxSize
	if left := s.Quota - used; left < limit {
		limit = left
	}
	if limit <= 0 {
		return Blob{}, ErrQuota
	}

	tmpDir := filepath.Join(s.dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return Blob{}, err
	}
	f, err := os.CreateTemp(tmpDir, "up-*")
	if err != nil {
		return Blob{}, err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Blob{}, err
	}
	if n > limit {
		if limit < s.MaxSize {
			return Blob{}, ErrQuota
		}
		return Blob{}, ErrTooLarge
	}
	if n == 0 {
		return Blob{}, fmt.Errorf("%w: empty file", domains.ErrInvalidInput)
	}

	b := Blob{ID: store.NewID(), OwnerID: userID, GroupID: groupID, Size: n}
	if err := os.MkdirAll(filepath.Dir(s.path(b.ID)), 0o700); err != nil {
		return Blob{}, err
	}
	if err := os.Rename(tmp, s.path(b.ID)); err != nil {
		return Blob{}, err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO blobs (id, owner_id, group_id, size, created_at) VALUES (?, ?, ?, ?, ?)`,
		b.ID, userID, groupID, n, s.now().Unix()); err != nil {
		os.Remove(s.path(b.ID))
		return Blob{}, err
	}
	ok = true
	return b, nil
}

// Get returns a blob's record, or domains.ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Blob, error) {
	if !validID(id) {
		return Blob{}, domains.ErrNotFound
	}
	b := Blob{ID: id}
	var seq sql.NullInt64
	err := s.st.DB.QueryRowContext(ctx, `SELECT owner_id, group_id, size, seq FROM blobs WHERE id = ?`, id).
		Scan(&b.OwnerID, &b.GroupID, &b.Size, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return Blob{}, domains.ErrNotFound
	}
	b.Seq = seq.Int64
	return b, err
}

// Open opens a blob's ciphertext for reading.
func (s *Service) Open(b Blob) (*os.File, error) {
	f, err := os.Open(s.path(b.ID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, domains.ErrNotFound
	}
	return f, err
}

// CheckPending verifies that every id is a blob userID uploaded for groupID
// and has not yet attached to a message.
func (s *Service) CheckPending(ctx context.Context, userID, groupID string, ids []string) error {
	if len(ids) > MaxPerMessage {
		return fmt.Errorf("%w: at most %d files per message", domains.ErrInvalidInput, MaxPerMessage)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("%w: file listed twice", domains.ErrInvalidInput)
		}
		seen[id] = true
		b, err := s.Get(ctx, id)
		if errors.Is(err, domains.ErrNotFound) {
			return fmt.Errorf("%w: unknown file", domains.ErrInvalidInput)
		}
		if err != nil {
			return err
		}
		if b.OwnerID != userID || b.GroupID != groupID || b.Seq != 0 {
			return fmt.Errorf("%w: file was not uploaded for this message", domains.ErrInvalidInput)
		}
	}
	return nil
}

// Attach records that the blobs belong to message seq, so other group
// members may fetch them and they are removed with the message.
func (s *Service) Attach(ctx context.Context, userID, groupID string, seq int64, ids []string) error {
	for _, id := range ids {
		if _, err := s.st.DB.ExecContext(ctx,
			`UPDATE blobs SET seq = ? WHERE id = ? AND owner_id = ? AND group_id = ? AND seq IS NULL`,
			seq, id, userID, groupID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteForMessage removes the blobs attached to a deleted message.
func (s *Service) DeleteForMessage(ctx context.Context, groupID string, seq int64) error {
	return s.deleteWhere(ctx, `group_id = ? AND seq = ?`, groupID, seq)
}

func (s *Service) deleteWhere(ctx context.Context, where string, args ...any) error {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id FROM blobs WHERE `+where, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM blobs WHERE id = ?`, id); err != nil {
			return err
		}
		if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Sweep removes orphans: uploads never attached to a message within
// PendingTTL, blobs whose message was deleted or is gone, blobs of groups
// that no longer exist, and stray partial uploads.
func (s *Service) Sweep(ctx context.Context) error {
	cutoff := s.now().Add(-PendingTTL)
	err := s.deleteWhere(ctx, `(seq IS NULL AND created_at < ?)
		OR (seq IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM mls_messages m WHERE m.group_id = blobs.group_id AND m.seq = blobs.seq AND m.deleted = 0))
		OR (NOT EXISTS (SELECT 1 FROM channels c WHERE c.id = blobs.group_id)
			AND NOT EXISTS (SELECT 1 FROM conclaves c WHERE c.id = blobs.group_id))`, cutoff.Unix())
	if err != nil {
		return err
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, "tmp"))
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(s.dir, "tmp", e.Name()))
		}
	}
	return nil
}

// maybeSweep runs Sweep at most every sweepEvery, piggybacking on uploads so
// the node needs no background goroutine.
func (s *Service) maybeSweep(ctx context.Context) {
	s.mu.Lock()
	due := s.now().Sub(s.lastSweep) >= sweepEvery
	if due {
		s.lastSweep = s.now()
	}
	s.mu.Unlock()
	if due {
		_ = s.Sweep(ctx)
	}
}
