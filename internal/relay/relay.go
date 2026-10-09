// Package relay is the node's MLS delivery service.
//
// Clients do all encryption with MLS (RFC 9420). The node only:
//   - hands out prepublished KeyPackages so members can add each other's devices,
//   - stores opaque MLS messages per group and gives them a global order,
//   - accepts one commit per epoch so every member agrees on group state,
//   - forwards Welcome messages to the device they're for,
//   - decides who may read or send, from domain membership or Conclave membership.
//
// A group is either a domain channel (Chapel or Voice Relay; the group id is the
// channel id) or a Conclave (a DM outside any domain; a Confession when it has
// two members).
package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/perm"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

var (
	ErrStaleEpoch = errors.New("message is for a different epoch; fetch new commits and retry")
	ErrNoKeys     = errors.New("device has no KeyPackages left")
	ErrTooLarge   = errors.New("message too large")
)

const (
	MaxMessageSize     = 256 << 10
	MaxKeyPackages     = 100
	MaxConclaveMembers = 50
	defaultFetchLimit  = 200
	KindApplication    = "application"
	KindProposal       = "proposal"
	KindCommit         = "commit"
)

type Message struct {
	Seq          int64  `json:"seq"`
	GroupID      string `json:"group_id"`
	SenderUser   string `json:"sender_user"`
	SenderDevice string `json:"sender_device"`
	Kind         string `json:"kind"`
	Epoch        int64  `json:"epoch"`
	Data         []byte `json:"data,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type Welcome struct {
	ID      int64  `json:"id"`
	GroupID string `json:"group_id"`
	Data    []byte `json:"data"`
}

// Event is pushed to connected clients; they fetch the content themselves.
type Event struct {
	Type    string `json:"type"` // "message", "deleted", "welcome", "domain", "conclave", "keys", "devices", "device_linked", "device_revoked", "transfer"
	GroupID string `json:"group_id"`
	Seq     int64  `json:"seq,omitempty"`
}

// Notifier delivers events to connected users.
type Notifier interface {
	Notify(userIDs []string, ev Event)
}

type Service struct {
	st     *store.Store
	notify Notifier
	now    func() time.Time
}

func NewService(st *store.Store, n Notifier) *Service {
	return &Service{st: st, notify: n, now: time.Now}
}

// ---- KeyPackages ----

// PublishKeyPackages stores KeyPackages for a device, up to MaxKeyPackages in total.
func (s *Service) PublishKeyPackages(ctx context.Context, deviceID string, kps [][]byte) error {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var have int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM key_packages WHERE device_id = ?`, deviceID).Scan(&have); err != nil {
		return err
	}
	if have+len(kps) > MaxKeyPackages {
		return fmt.Errorf("%w: at most %d KeyPackages per device", domains.ErrInvalidInput, MaxKeyPackages)
	}
	for _, kp := range kps {
		if len(kp) == 0 || len(kp) > 16<<10 {
			return fmt.Errorf("%w: KeyPackage must be 1 byte to 16 KB", domains.ErrInvalidInput)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO key_packages (device_id, data, created_at) VALUES (?, ?, ?)`, deviceID, kp, s.now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeviceKeyPackage is one claimed KeyPackage for one device.
type DeviceKeyPackage struct {
	DeviceID   string `json:"device_id"`
	KeyPackage []byte `json:"key_package"`
}

// ClaimKeyPackages removes and returns one KeyPackage for each of targetUser's
// devices. The requester must share a domain or Conclave with the target.
// Devices with none left are skipped.
func (s *Service) ClaimKeyPackages(ctx context.Context, requesterID, targetUser string) ([]DeviceKeyPackage, error) {
	if requesterID != targetUser {
		ok, err := s.shareSpace(ctx, requesterID, targetUser)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, domains.ErrForbidden
		}
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT k.id, k.device_id, k.data FROM key_packages k
		  WHERE k.id IN (SELECT MIN(k2.id) FROM key_packages k2 JOIN devices d ON d.id = k2.device_id
		                  WHERE d.user_id = ? GROUP BY k2.device_id)`, targetUser)
	if err != nil {
		return nil, err
	}
	var ids []int64
	out := []DeviceKeyPackage{}
	for rows.Next() {
		var id int64
		var kp DeviceKeyPackage
		if err := rows.Scan(&id, &kp.DeviceID, &kp.KeyPackage); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		out = append(out, kp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM key_packages WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, ErrNoKeys
	}
	return out, tx.Commit()
}

// ---- Conclaves ----

// CreateConclave starts a DM between the creator and memberIDs. Every member
// must share a domain with the creator.
func (s *Service) CreateConclave(ctx context.Context, creatorID string, memberIDs []string) (string, error) {
	members := map[string]bool{creatorID: true}
	for _, m := range memberIDs {
		members[m] = true
	}
	if len(members) < 2 || len(members) > MaxConclaveMembers {
		return "", fmt.Errorf("%w: a Conclave needs 2 to %d members", domains.ErrInvalidInput, MaxConclaveMembers)
	}
	for m := range members {
		if m == creatorID {
			continue
		}
		ok, err := s.shareDomain(ctx, creatorID, m)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%w: you can only start a Conclave with people who share a domain with you", domains.ErrForbidden)
		}
	}
	id := store.NewID()
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO conclaves (id, created_by, created_at) VALUES (?, ?, ?)`,
		id, creatorID, s.now().Unix()); err != nil {
		return "", err
	}
	for m := range members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO conclave_members (conclave_id, user_id) VALUES (?, ?)`, id, m); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

// LeaveConclave removes userID from a Conclave. Other members' clients then
// commit the removal of that user's devices from the MLS group.
func (s *Service) LeaveConclave(ctx context.Context, conclaveID, userID string) error {
	res, err := s.st.DB.ExecContext(ctx,
		`DELETE FROM conclave_members WHERE conclave_id = ? AND user_id = ?`, conclaveID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domains.ErrNotMember
	}
	return nil
}

// ---- messages ----

// Send stores an MLS message. Commits must target the group's current epoch
// and advance it; the first commit for an epoch wins.
func (s *Service) Send(ctx context.Context, groupID, userID, deviceID, kind string, epoch int64, data []byte) (Message, error) {
	if kind != KindApplication && kind != KindProposal && kind != KindCommit {
		return Message{}, fmt.Errorf("%w: kind must be application, proposal or commit", domains.ErrInvalidInput)
	}
	if len(data) == 0 {
		return Message{}, fmt.Errorf("%w: empty message", domains.ErrInvalidInput)
	}
	if len(data) > MaxMessageSize {
		return Message{}, ErrTooLarge
	}
	// Muted members can't post content but can still send the commits and
	// proposals that keep their MLS state in sync.
	recipients, err := s.access(ctx, groupID, userID, kind == KindApplication)
	if err != nil {
		return Message{}, err
	}
	m := Message{GroupID: groupID, SenderUser: userID, SenderDevice: deviceID, Kind: kind, Epoch: epoch,
		Data: data, CreatedAt: s.now().Unix()}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mls_groups (id) VALUES (?)`, groupID); err != nil {
		return Message{}, err
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM mls_groups WHERE id = ?`, groupID).Scan(&current); err != nil {
		return Message{}, err
	}
	if epoch != current {
		return Message{}, ErrStaleEpoch
	}
	if kind == KindCommit {
		if _, err := tx.ExecContext(ctx, `UPDATE mls_groups SET epoch = epoch + 1 WHERE id = ?`, groupID); err != nil {
			return Message{}, err
		}
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO mls_messages (group_id, sender_user, sender_device, kind, epoch, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`, groupID, userID, deviceID, kind, epoch, data, m.CreatedAt)
	if err != nil {
		return Message{}, err
	}
	if m.Seq, err = res.LastInsertId(); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	s.notify.Notify(recipients, Event{Type: "message", GroupID: groupID, Seq: m.Seq})
	return m, nil
}

// Epoch returns the group's current epoch (0 for a group with no commits yet).
func (s *Service) Epoch(ctx context.Context, groupID, userID string) (int64, error) {
	if _, err := s.access(ctx, groupID, userID, false); err != nil {
		return 0, err
	}
	var epoch int64
	err := s.st.DB.QueryRowContext(ctx, `SELECT epoch FROM mls_groups WHERE id = ?`, groupID).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return epoch, err
}

// Fetch returns messages in a group after seq, oldest first.
func (s *Service) Fetch(ctx context.Context, groupID, userID string, afterSeq int64, limit int) ([]Message, error) {
	if _, err := s.access(ctx, groupID, userID, false); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > defaultFetchLimit {
		limit = defaultFetchLimit
	}
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT seq, group_id, sender_user, sender_device, kind, epoch, data, deleted, created_at
		   FROM mls_messages WHERE group_id = ? AND seq > ? ORDER BY seq LIMIT ?`, groupID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Seq, &m.GroupID, &m.SenderUser, &m.SenderDevice, &m.Kind, &m.Epoch, &m.Data,
			&m.Deleted, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete erases an application message's ciphertext, leaving a tombstone so
// clients can hide it. Senders may delete their own; in a domain channel,
// members with the DeleteMessages permission may delete anyone's.
func (s *Service) Delete(ctx context.Context, groupID string, seq int64, userID string) error {
	recipients, err := s.access(ctx, groupID, userID, false)
	if err != nil {
		return err
	}
	var sender, kind string
	err = s.st.DB.QueryRowContext(ctx,
		`SELECT sender_user, kind FROM mls_messages WHERE group_id = ? AND seq = ? AND deleted = 0`, groupID, seq).
		Scan(&sender, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return domains.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != KindApplication {
		return fmt.Errorf("%w: only application messages can be deleted", domains.ErrInvalidInput)
	}
	if sender != userID {
		ok, err := s.canModerate(ctx, groupID, userID, sender)
		if err != nil {
			return err
		}
		if !ok {
			return domains.ErrForbidden
		}
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`UPDATE mls_messages SET data = NULL, deleted = 1 WHERE group_id = ? AND seq = ?`, groupID, seq); err != nil {
		return err
	}
	s.notify.Notify(recipients, Event{Type: "deleted", GroupID: groupID, Seq: seq})
	return nil
}

// ---- Welcomes ----

// SendWelcome forwards an MLS Welcome to a device that was just added to a group.
// The sender must be able to send to the group and the target device's owner
// must be allowed to read it.
func (s *Service) SendWelcome(ctx context.Context, groupID, senderID, targetDevice string, data []byte) error {
	if len(data) == 0 || len(data) > MaxMessageSize {
		return fmt.Errorf("%w: Welcome must be 1 byte to %d bytes", domains.ErrInvalidInput, MaxMessageSize)
	}
	if _, err := s.access(ctx, groupID, senderID, false); err != nil {
		return err
	}
	var owner string
	err := s.st.DB.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id = ?`, targetDevice).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return domains.ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.access(ctx, groupID, owner, false); err != nil {
		return fmt.Errorf("%w: that device's owner can't join this group", domains.ErrForbidden)
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO mls_groups (id) VALUES (?)`, groupID); err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO mls_welcomes (device_id, group_id, data, created_at) VALUES (?, ?, ?, ?)`,
		targetDevice, groupID, data, s.now().Unix()); err != nil {
		return err
	}
	s.notify.Notify([]string{owner}, Event{Type: "welcome", GroupID: groupID})
	return nil
}

// TakeWelcomes returns and deletes every pending Welcome for a device.
func (s *Service) TakeWelcomes(ctx context.Context, deviceID string) ([]Welcome, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, group_id, data FROM mls_welcomes WHERE device_id = ? ORDER BY id`, deviceID)
	if err != nil {
		return nil, err
	}
	out := []Welcome{}
	for rows.Next() {
		var w Welcome
		if err := rows.Scan(&w.ID, &w.GroupID, &w.Data); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, w)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mls_welcomes WHERE device_id = ?`, deviceID); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ---- access rules ----

// access checks that userID may read (or, with send, post to) groupID and
// returns the user IDs who should be told about new messages.
func (s *Service) access(ctx context.Context, groupID, userID string, send bool) ([]string, error) {
	// Domain channel?
	var domainID string
	err := s.st.DB.QueryRowContext(ctx, `SELECT domain_id FROM channels WHERE id = ?`, groupID).Scan(&domainID)
	if err == nil {
		var p int64
		var muted bool
		var ownerID string
		err := s.st.DB.QueryRowContext(ctx,
			`SELECT r.permissions, m.muted, d.owner_id FROM members m
			   JOIN roles r ON r.id = m.role_id JOIN domains d ON d.id = m.domain_id
			  WHERE m.domain_id = ? AND m.user_id = ?`, domainID, userID).Scan(&p, &muted, &ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domains.ErrNotMember
		}
		if err != nil {
			return nil, err
		}
		if send && ownerID != userID && (muted || !perm.Permission(p).Has(perm.SendMessages)) {
			return nil, domains.ErrForbidden
		}
		return s.userList(ctx, `SELECT user_id FROM members WHERE domain_id = ?`, domainID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Conclave?
	var n int
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM conclave_members WHERE conclave_id = ? AND user_id = ?`, groupID, userID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		var exists int
		s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM conclaves WHERE id = ?`, groupID).Scan(&exists)
		if exists == 0 {
			return nil, domains.ErrNotFound
		}
		return nil, domains.ErrNotMember
	}
	return s.userList(ctx, `SELECT user_id FROM conclave_members WHERE conclave_id = ?`, groupID)
}

// canModerate reports whether actorID may delete targetID's messages in a domain channel.
func (s *Service) canModerate(ctx context.Context, groupID, actorID, targetID string) (bool, error) {
	var domainID string
	err := s.st.DB.QueryRowContext(ctx, `SELECT domain_id FROM channels WHERE id = ?`, groupID).Scan(&domainID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // Conclaves have no moderators
	}
	if err != nil {
		return false, err
	}
	actor, ok, err := s.memberActor(ctx, domainID, actorID)
	if err != nil || !ok {
		return false, err
	}
	target, ok, err := s.memberActor(ctx, domainID, targetID)
	if err != nil {
		return false, err
	}
	if !ok {
		// The sender has left or been removed; any member with the permission may clean up.
		return actor.IsOwner || actor.Perms.Has(perm.DeleteMessages), nil
	}
	return perm.CanActOn(actor, perm.DeleteMessages, target.Rank, target.IsOwner), nil
}

func (s *Service) memberActor(ctx context.Context, domainID, userID string) (perm.Actor, bool, error) {
	var a perm.Actor
	var p int64
	var owner string
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT r.rank, r.permissions, d.owner_id FROM members m
		   JOIN roles r ON r.id = m.role_id JOIN domains d ON d.id = m.domain_id
		  WHERE m.domain_id = ? AND m.user_id = ?`, domainID, userID).Scan(&a.Rank, &p, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	a.Perms, a.IsOwner = perm.Permission(p), owner == userID
	return a, true, nil
}

func (s *Service) shareDomain(ctx context.Context, a, b string) (bool, error) {
	var n int
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members m1 JOIN members m2 ON m1.domain_id = m2.domain_id
		  WHERE m1.user_id = ? AND m2.user_id = ?`, a, b).Scan(&n)
	return n > 0, err
}

// shareSpace reports whether a and b share a domain or a Conclave.
func (s *Service) shareSpace(ctx context.Context, a, b string) (bool, error) {
	if ok, err := s.shareDomain(ctx, a, b); ok || err != nil {
		return ok, err
	}
	var n int
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM conclave_members c1 JOIN conclave_members c2 ON c1.conclave_id = c2.conclave_id
		  WHERE c1.user_id = ? AND c2.user_id = ?`, a, b).Scan(&n)
	return n > 0, err
}

func (s *Service) userList(ctx context.Context, query string, arg string) ([]string, error) {
	rows, err := s.st.DB.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ConclaveMember is one member of a Conclave.
type ConclaveMember struct {
	UserID   string `json:"user_id"`
	Callsign string `json:"callsign"`
}

// Conclave is a DM group the caller belongs to.
type Conclave struct {
	ID        string           `json:"id"`
	CreatedBy string           `json:"created_by"`
	CreatedAt int64            `json:"created_at"`
	Members   []ConclaveMember `json:"members"`
}

// Conclaves lists the Conclaves userID belongs to, newest first, with their members.
func (s *Service) Conclaves(ctx context.Context, userID string) ([]Conclave, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT c.id, c.created_by, c.created_at, u.id, u.callsign
		   FROM conclaves c
		   JOIN conclave_members mine ON mine.conclave_id = c.id AND mine.user_id = ?
		   JOIN conclave_members cm ON cm.conclave_id = c.id
		   JOIN users u ON u.id = cm.user_id
		  ORDER BY c.created_at DESC, c.id, u.callsign`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conclave{}
	index := map[string]int{}
	for rows.Next() {
		var c Conclave
		var m ConclaveMember
		if err := rows.Scan(&c.ID, &c.CreatedBy, &c.CreatedAt, &m.UserID, &m.Callsign); err != nil {
			return nil, err
		}
		i, ok := index[c.ID]
		if !ok {
			i = len(out)
			index[c.ID] = i
			out = append(out, c)
		}
		out[i].Members = append(out[i].Members, m)
	}
	return out, rows.Err()
}

// ConclaveMemberIDs returns the user IDs in a Conclave.
func (s *Service) ConclaveMemberIDs(ctx context.Context, conclaveID string) ([]string, error) {
	return s.userList(ctx, `SELECT user_id FROM conclave_members WHERE conclave_id = ?`, conclaveID)
}

// Peers returns userID and everyone who shares a domain or Conclave with them.
func (s *Service) Peers(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT m2.user_id FROM members m1 JOIN members m2 ON m1.domain_id = m2.domain_id WHERE m1.user_id = ?
		 UNION
		 SELECT c2.user_id FROM conclave_members c1 JOIN conclave_members c2 ON c1.conclave_id = c2.conclave_id WHERE c1.user_id = ?
		 UNION SELECT ?`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GroupDevice is one device that belongs in a group's MLS state.
type GroupDevice struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

// GroupDevices lists every device of every user who may read groupID, so
// members' clients can add missing devices and remove revoked ones. The
// caller must be able to read the group.
func (s *Service) GroupDevices(ctx context.Context, groupID, userID string) ([]GroupDevice, error) {
	users, err := s.access(ctx, groupID, userID, false)
	if err != nil {
		return nil, err
	}
	out := []GroupDevice{}
	for _, u := range users {
		rows, err := s.st.DB.QueryContext(ctx, `SELECT id FROM devices WHERE user_id = ? ORDER BY created_at, id`, u)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			d := GroupDevice{UserID: u}
			if err := rows.Scan(&d.DeviceID); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
