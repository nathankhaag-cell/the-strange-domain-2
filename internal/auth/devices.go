package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

const (
	linkCodeTTL   = 10 * time.Minute
	linkCodeLen   = 10 // Crockford base32: 50 bits, single use, ten minutes
	linkAlphabet  = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	recoveryCtx   = "strange-domain-recovery-v1:"
	maxUserDevice = 20
)

var ErrTooManyDevices = errors.New("this account already has the maximum number of devices")

// Device is one registered device of an account.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey []byte `json:"public_key"`
	CreatedAt int64  `json:"created_at"`
}

// NewLinkCode issues a single-use code that lets a new device join userID's
// account. The code is shown as XXXXX-XXXXX (and in a QR code by the client).
func (s *Service) NewLinkCode(ctx context.Context, userID string) (string, error) {
	b := make([]byte, linkCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := make([]byte, linkCodeLen)
	for i := range b {
		code[i] = linkAlphabet[int(b[i])%len(linkAlphabet)] // 256 is a multiple of 32: no bias
	}
	now := s.now()
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM device_link_codes WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO device_link_codes (code_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(string(code)), userID, now.Add(linkCodeTTL).Unix()); err != nil {
		return "", err
	}
	return string(code[:5]) + "-" + string(code[5:]), nil
}

// normalizeLinkCode accepts what people type: any case, with or without the
// dash or spaces, and the usual Crockford look-alikes.
func normalizeLinkCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1").Replace(code)
	return code
}

// LinkDevice registers a new device under the account that issued code.
func (s *Service) LinkDevice(ctx context.Context, code, deviceName string, pub ed25519.PublicKey) (Account, error) {
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" || len(deviceName) > 64 {
		return Account{}, fmt.Errorf("%w: device name must be 1-64 characters", domains.ErrInvalidInput)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Account{}, fmt.Errorf("%w: public key must be a 32-byte Ed25519 key", domains.ErrInvalidInput)
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	var acct Account
	err = tx.QueryRowContext(ctx,
		`SELECT u.id, u.callsign, u.is_node_admin FROM device_link_codes c JOIN users u ON u.id = c.user_id
		  WHERE c.code_hash = ? AND c.expires_at > ?`, hashToken(normalizeLinkCode(code)), s.now().Unix()).
		Scan(&acct.UserID, &acct.Callsign, &acct.IsNodeAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrUnauthorized
	}
	if err != nil {
		return Account{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_link_codes WHERE code_hash = ?`, hashToken(normalizeLinkCode(code))); err != nil {
		return Account{}, err
	}
	if err := s.addDevice(ctx, tx, acct.UserID, deviceName, pub, &acct); err != nil {
		return Account{}, err
	}
	return acct, tx.Commit()
}

func (s *Service) addDevice(ctx context.Context, tx *sql.Tx, userID, name string, pub ed25519.PublicKey, acct *Account) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE identity_pk = ?`, []byte(pub)).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDeviceKnown
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return err
	}
	if n >= maxUserDevice {
		return ErrTooManyDevices
	}
	acct.DeviceID = store.NewID()
	_, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, name, identity_pk, created_at) VALUES (?, ?, ?, ?, ?)`,
		acct.DeviceID, userID, name, []byte(pub), s.now().Unix())
	return err
}

// Devices lists userID's devices, oldest first.
func (s *Service) Devices(ctx context.Context, userID string) ([]Device, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, name, identity_pk, created_at FROM devices WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.PublicKey, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice removes one of userID's devices and ends its sessions. Other
// members' clients then remove it from their MLS groups.
func (s *Service) RevokeDevice(ctx context.Context, userID, deviceID string) error {
	res, err := s.st.DB.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domains.ErrNotFound
	}
	return nil
}

// SetRecoveryKey stores the public half of the key the client derives from
// the person's recovery code. Calling it again replaces the old one.
func (s *Service) SetRecoveryKey(ctx context.Context, userID string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: recovery key must be a 32-byte Ed25519 key", domains.ErrInvalidInput)
	}
	_, err := s.st.DB.ExecContext(ctx, `UPDATE users SET recovery_pk = ? WHERE id = ?`, []byte(pub), userID)
	return err
}

// RecoveryChallenge issues a nonce for callsign's recovery key to sign.
func (s *Service) RecoveryChallenge(ctx context.Context, callsign string) ([]byte, error) {
	var userID string
	var pk []byte
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, recovery_pk FROM users WHERE callsign = ?`,
		strings.TrimSpace(callsign)).Scan(&userID, &pk)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(pk) == 0) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	now := s.now()
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM recovery_challenges WHERE expires_at <= ?`, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO recovery_challenges (nonce, user_id, expires_at) VALUES (?, ?, ?)`,
		nonce, userID, now.Add(challengeTTL).Unix()); err != nil {
		return nil, err
	}
	return nonce, nil
}

// SignRecovery is what a client signs with its recovery key.
func SignRecovery(priv ed25519.PrivateKey, nonce []byte) []byte {
	return ed25519.Sign(priv, append([]byte(recoveryCtx), nonce...))
}

// Recover proves ownership of callsign with the recovery key, removes every
// existing device (they're assumed lost) and registers a new one.
func (s *Service) Recover(ctx context.Context, callsign string, nonce, sig []byte, deviceName string, pub ed25519.PublicKey) (Account, error) {
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" || len(deviceName) > 64 || len(pub) != ed25519.PublicKeySize {
		return Account{}, fmt.Errorf("%w: need a device name and a 32-byte Ed25519 key", domains.ErrInvalidInput)
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()

	var acct Account
	var recoveryPK []byte
	err = tx.QueryRowContext(ctx,
		`SELECT id, callsign, is_node_admin, recovery_pk FROM users WHERE callsign = ?`, strings.TrimSpace(callsign)).
		Scan(&acct.UserID, &acct.Callsign, &acct.IsNodeAdmin, &recoveryPK)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrUnauthorized
	}
	if err != nil {
		return Account{}, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM recovery_challenges WHERE nonce = ? AND user_id = ? AND expires_at > ?`, nonce, acct.UserID, s.now().Unix())
	if err != nil {
		return Account{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrUnauthorized
	}
	if len(recoveryPK) != ed25519.PublicKeySize || !ed25519.Verify(recoveryPK, append([]byte(recoveryCtx), nonce...), sig) {
		tx.Commit() // keep the challenge consumed
		return Account{}, ErrUnauthorized
	}
	// Lost devices lose their sessions, KeyPackages and pending transfers too (cascade).
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE user_id = ?`, acct.UserID); err != nil {
		return Account{}, err
	}
	if err := s.addDevice(ctx, tx, acct.UserID, deviceName, pub, &acct); err != nil {
		return Account{}, err
	}
	return acct, tx.Commit()
}
