// Package auth handles accounts, devices and sign-in.
//
// Nobody signs in with a password. Each device generates an Ed25519 identity
// key and registers its public half; to sign in it signs a single-use
// challenge from the node and receives a bearer session token. The node
// stores only a hash of that token.
//
// Registration is invite-only: the very first account on a node needs no
// Summons and becomes the node's administrator; everyone after that must
// present a valid Summons, which also joins them to that domain.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

var (
	ErrUnauthorized    = errors.New("not signed in")
	ErrSummonsRequired = errors.New("a Summons is required to join this node")
	ErrCallsignTaken   = errors.New("callsign is taken")
	ErrDeviceKnown     = errors.New("this device key is already registered")
)

const (
	challengeTTL = 2 * time.Minute
	sessionTTL   = 30 * 24 * time.Hour
	// signContext binds signatures to this purpose so a device key can't be
	// tricked into signing a challenge that means something else.
	signContext = "strange-domain-auth-v1:"
)

// Account is a signed-in device's identity.
type Account struct {
	UserID      string
	DeviceID    string
	Callsign    string
	IsNodeAdmin bool
}

type Service struct {
	st  *store.Store
	dom *domains.Service
	now func() time.Time
}

func NewService(st *store.Store, dom *domains.Service) *Service {
	return &Service{st: st, dom: dom, now: time.Now}
}

// Register creates an account and its first device. summons may be empty
// only for the node's first account.
func (s *Service) Register(ctx context.Context, callsign, deviceName string, pub ed25519.PublicKey, summons string) (Account, error) {
	callsign = strings.TrimSpace(callsign)
	deviceName = strings.TrimSpace(deviceName)
	if callsign == "" || len(callsign) > 32 {
		return Account{}, fmt.Errorf("%w: callsign must be 1-32 characters", domains.ErrInvalidInput)
	}
	if deviceName == "" || len(deviceName) > 64 {
		return Account{}, fmt.Errorf("%w: device name must be 1-64 characters", domains.ErrInvalidInput)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Account{}, fmt.Errorf("%w: public key must be a 32-byte Ed25519 key", domains.ErrInvalidInput)
	}
	if summons != "" {
		if err := s.dom.CheckInvite(ctx, summons); err != nil {
			return Account{}, err
		}
	}

	acct := Account{UserID: store.NewID(), DeviceID: store.NewID(), Callsign: callsign}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	var users int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return Account{}, err
	}
	if users > 0 && summons == "" {
		return Account{}, ErrSummonsRequired
	}
	acct.IsNodeAdmin = users == 0
	if err := s.uniqueCheck(ctx, tx, callsign, pub); err != nil {
		return Account{}, err
	}
	now := s.now().Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, callsign, created_at, is_node_admin) VALUES (?, ?, ?, ?)`,
		acct.UserID, callsign, now, acct.IsNodeAdmin); err != nil {
		return Account{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, name, identity_pk, created_at) VALUES (?, ?, ?, ?, ?)`,
		acct.DeviceID, acct.UserID, deviceName, []byte(pub), now); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}

	if summons != "" {
		if _, err := s.dom.JoinByInvite(ctx, summons, acct.UserID); err != nil {
			// The Summons was used up between the check and the join; undo the
			// account so the person can retry with a fresh one.
			s.st.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, acct.UserID)
			return Account{}, err
		}
	}
	return acct, nil
}

func (s *Service) uniqueCheck(ctx context.Context, tx *sql.Tx, callsign string, pub ed25519.PublicKey) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE callsign = ?`, callsign).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrCallsignTaken
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE identity_pk = ?`, []byte(pub)).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDeviceKnown
	}
	return nil
}

// Challenge issues a single-use nonce for deviceID to sign.
func (s *Service) Challenge(ctx context.Context, deviceID string) ([]byte, error) {
	var n int
	if err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id = ?`, deviceID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrUnauthorized
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	now := s.now()
	// Drop expired challenges so the table can't grow without bound.
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM auth_challenges WHERE expires_at <= ?`, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO auth_challenges (nonce, device_id, expires_at) VALUES (?, ?, ?)`,
		nonce, deviceID, now.Add(challengeTTL).Unix()); err != nil {
		return nil, err
	}
	return nonce, nil
}

// SignChallenge is what a client signs; exported so clients and tests agree.
func SignChallenge(priv ed25519.PrivateKey, nonce []byte) []byte {
	return ed25519.Sign(priv, append([]byte(signContext), nonce...))
}

// Verify checks a signed challenge and returns a new session token.
func (s *Service) Verify(ctx context.Context, deviceID string, nonce, sig []byte) (string, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// Consume the challenge first so it can never be replayed, valid or not.
	res, err := tx.ExecContext(ctx,
		`DELETE FROM auth_challenges WHERE nonce = ? AND device_id = ? AND expires_at > ?`,
		nonce, deviceID, s.now().Unix())
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrUnauthorized
	}
	var pub []byte
	var userID string
	if err := tx.QueryRowContext(ctx, `SELECT identity_pk, user_id FROM devices WHERE id = ?`, deviceID).
		Scan(&pub, &userID); err != nil {
		return "", ErrUnauthorized
	}
	if !ed25519.Verify(pub, append([]byte(signContext), nonce...), sig) {
		tx.Commit() // keep the challenge consumed
		return "", ErrUnauthorized
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, device_id, user_id, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), deviceID, userID, s.now().Add(sessionTTL).Unix()); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

// Authenticate resolves a bearer token to its account.
func (s *Service) Authenticate(ctx context.Context, token string) (Account, error) {
	if token == "" {
		return Account{}, ErrUnauthorized
	}
	var a Account
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT s.user_id, s.device_id, u.callsign, u.is_node_admin
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ? AND s.expires_at > ?`, hashToken(token), s.now().Unix()).
		Scan(&a.UserID, &a.DeviceID, &a.Callsign, &a.IsNodeAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrUnauthorized
	}
	return a, err
}

// SignOut ends the session for token.
func (s *Service) SignOut(ctx context.Context, token string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
