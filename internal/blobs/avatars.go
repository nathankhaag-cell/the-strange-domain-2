package blobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image/png"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
)

// Profile pictures are not end-to-end encrypted: the node stores and serves
// them, like callsigns. The client crops and re-encodes them to a small
// square (which also drops any metadata in the original file) before upload.

const (
	MaxAvatarSize = 512 << 10
	maxAvatarSide = 1024
)

// Avatar is a stored profile picture.
type Avatar struct {
	Mime    string
	Data    []byte
	Version int64
}

// avatarMime checks data is a PNG or WebP image and returns its type.
func avatarMime(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("%w: not a valid PNG", domains.ErrInvalidInput)
		}
		if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxAvatarSide || cfg.Height > maxAvatarSide {
			return "", fmt.Errorf("%w: picture must be at most %d pixels square", domains.ErrInvalidInput, maxAvatarSide)
		}
		return "image/png", nil
	case len(data) > 16 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", nil
	}
	return "", fmt.Errorf("%w: profile picture must be PNG or WebP", domains.ErrInvalidInput)
}

// SetAvatar stores userID's profile picture and returns its new version.
func (s *Service) SetAvatar(ctx context.Context, userID string, data []byte) (int64, error) {
	if len(data) > MaxAvatarSize {
		return 0, ErrTooLarge
	}
	mime, err := avatarMime(data)
	if err != nil {
		return 0, err
	}
	version := s.now().UnixMilli()
	var prev int64
	s.st.DB.QueryRowContext(ctx, `SELECT version FROM avatars WHERE user_id = ?`, userID).Scan(&prev)
	if version <= prev {
		version = prev + 1
	}
	_, err = s.st.DB.ExecContext(ctx,
		`INSERT INTO avatars (user_id, mime, data, version) VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET mime = excluded.mime, data = excluded.data, version = excluded.version`,
		userID, mime, data, version)
	return version, err
}

// DeleteAvatar removes userID's profile picture.
func (s *Service) DeleteAvatar(ctx context.Context, userID string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM avatars WHERE user_id = ?`, userID)
	return err
}

// GetAvatar returns userID's profile picture, or domains.ErrNotFound.
func (s *Service) GetAvatar(ctx context.Context, userID string) (Avatar, error) {
	var a Avatar
	err := s.st.DB.QueryRowContext(ctx, `SELECT mime, data, version FROM avatars WHERE user_id = ?`, userID).
		Scan(&a.Mime, &a.Data, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domains.ErrNotFound
	}
	return a, err
}

// AvatarVersions returns the profile picture version of each user that has one.
func (s *Service) AvatarVersions(ctx context.Context, userIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, id := range userIDs {
		var v int64
		err := s.st.DB.QueryRowContext(ctx, `SELECT version FROM avatars WHERE user_id = ?`, id).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}
