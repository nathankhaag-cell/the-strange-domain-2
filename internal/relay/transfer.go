package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
)

// History transfer between a person's own devices. The sending device
// encrypts its message history for the new device's key; the node only
// passes the chunks along and deletes each once it is taken.

const (
	MaxTransferChunk   = 4 << 20
	MaxTransferChunks  = 64
	maxPendingTransfer = 256 << 20
)

// TransferChunk describes one stored chunk (Data only when taken).
type TransferChunk struct {
	ID         int64  `json:"id"`
	TransferID string `json:"transfer_id"`
	FromDevice string `json:"from_device"`
	Chunk      int    `json:"chunk"`
	Total      int    `json:"total"`
	Size       int    `json:"size"`
	Data       []byte `json:"data,omitempty"`
}

// SendTransferChunk stores one chunk for toDevice, which must be another
// device of the same person.
func (s *Service) SendTransferChunk(ctx context.Context, userID, fromDevice, toDevice, transferID string, chunk, total int, data []byte) error {
	if transferID == "" || len(transferID) > 64 || total < 1 || total > MaxTransferChunks || chunk < 0 || chunk >= total {
		return fmt.Errorf("%w: need a transfer id and 0 <= chunk < total <= %d", domains.ErrInvalidInput, MaxTransferChunks)
	}
	if len(data) == 0 || len(data) > MaxTransferChunk {
		return fmt.Errorf("%w: a chunk must be 1 byte to %d bytes", domains.ErrInvalidInput, MaxTransferChunk)
	}
	if toDevice == fromDevice {
		return fmt.Errorf("%w: a device can't send history to itself", domains.ErrInvalidInput)
	}
	var owner string
	err := s.st.DB.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id = ?`, toDevice).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != userID) {
		return domains.ErrNotFound
	}
	if err != nil {
		return err
	}
	var pending int64
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(LENGTH(data)), 0) FROM device_transfers WHERE to_device = ?`, toDevice).Scan(&pending); err != nil {
		return err
	}
	if pending+int64(len(data)) > maxPendingTransfer {
		return ErrTooLarge
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO device_transfers (to_device, from_device, transfer_id, chunk, total, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`, toDevice, fromDevice, transferID, chunk, total, data, s.now().Unix()); err != nil {
		return err
	}
	s.notify.Notify([]string{userID}, Event{Type: "transfer", GroupID: transferID})
	return nil
}

// PendingTransfers lists chunks waiting for deviceID, without their data.
func (s *Service) PendingTransfers(ctx context.Context, deviceID string) ([]TransferChunk, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, transfer_id, from_device, chunk, total, LENGTH(data) FROM device_transfers
		  WHERE to_device = ? ORDER BY transfer_id, chunk`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TransferChunk{}
	for rows.Next() {
		var c TransferChunk
		if err := rows.Scan(&c.ID, &c.TransferID, &c.FromDevice, &c.Chunk, &c.Total, &c.Size); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TakeTransferChunk returns one chunk addressed to deviceID and deletes it.
func (s *Service) TakeTransferChunk(ctx context.Context, deviceID string, id int64) (TransferChunk, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return TransferChunk{}, err
	}
	defer tx.Rollback()
	var c TransferChunk
	err = tx.QueryRowContext(ctx,
		`SELECT id, transfer_id, from_device, chunk, total, data FROM device_transfers WHERE id = ? AND to_device = ?`,
		id, deviceID).Scan(&c.ID, &c.TransferID, &c.FromDevice, &c.Chunk, &c.Total, &c.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return TransferChunk{}, domains.ErrNotFound
	}
	if err != nil {
		return TransferChunk{}, err
	}
	c.Size = len(c.Data)
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_transfers WHERE id = ?`, id); err != nil {
		return TransferChunk{}, err
	}
	return c, tx.Commit()
}
