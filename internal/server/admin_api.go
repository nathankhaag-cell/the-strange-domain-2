package server

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/rtc"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/update"
)

// The node admin (the first account on the node, users.is_node_admin) can
// see how the node is doing. Nothing here reveals message contents, keys,
// tokens, invite codes or anyone's network address.

func (s *Server) adminRoutes() {
	s.mux.Handle("GET /api/v1/admin/stats", s.authed(s.adminStats))
}

// Counts are totals read from the database.
type Counts struct {
	Users   int `json:"users"`
	Devices int `json:"devices"`
	Domains int `json:"domains"`
}

// Counts returns how many accounts, devices and domains the node holds.
func (s *Server) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM devices), (SELECT COUNT(*) FROM domains)`).
		Scan(&c.Users, &c.Devices, &c.Domains)
	return c, err
}

type domainStats struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Members  int    `json:"members"`
	Online   int    `json:"online"`
	Channels int    `json:"channels"`
	Messages int    `json:"messages"` // stored encrypted messages in its channels
}

type adminStatsJSON struct {
	Version      string         `json:"version"`
	Platform     string         `json:"platform"`
	UptimeS      int64          `json:"uptime_s"`
	DBBytes      int64          `json:"db_bytes"`
	Counts       Counts         `json:"counts"`
	OnlineUsers  int            `json:"online_users"`
	Traffic      Snapshot       `json:"traffic"`
	Domains      []domainStats  `json:"domains"`
	RecentErrors []ErrorEntry   `json:"recent_errors"`
	Update       *update.Status `json:"update"`
	Calls        *rtc.Stats     `json:"calls,omitempty"` // nil: calls are off
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request, a auth.Account) {
	if !a.IsNodeAdmin {
		writeErr(w, domains.ErrForbidden)
		return
	}
	ctx := r.Context()
	counts, err := s.Counts(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	ds, err := s.domainStats(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	var calls *rtc.Stats
	if s.rtc != nil {
		st := s.rtc.Stats()
		calls = &st
	}
	writeJSON(w, adminStatsJSON{
		Version:      Version,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		UptimeS:      int64(time.Since(s.started).Seconds()),
		DBBytes:      s.st.Size(),
		Counts:       counts,
		OnlineUsers:  len(s.hub.OnlineUsers()),
		Traffic:      s.metrics.Snapshot(),
		Domains:      ds,
		RecentErrors: s.metrics.RecentErrors(),
		Update:       s.updates.Status(),
		Calls:        calls,
	})
}

func (s *Server) domainStats(ctx context.Context) ([]domainStats, error) {
	rows, err := s.st.DB.QueryContext(ctx, `
		SELECT d.id, d.name,
		       (SELECT COUNT(*) FROM members m WHERE m.domain_id = d.id),
		       (SELECT COUNT(*) FROM channels c WHERE c.domain_id = d.id),
		       (SELECT COUNT(*) FROM channels c JOIN mls_messages mm ON mm.group_id = c.id
		         WHERE c.domain_id = d.id AND mm.kind = 'application' AND mm.deleted = 0)
		  FROM domains d ORDER BY d.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domainStats{}
	for rows.Next() {
		var d domainStats
		if err := rows.Scan(&d.ID, &d.Name, &d.Members, &d.Channels, &d.Messages); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Who is online, per domain.
	online := s.hub.OnlineUsers()
	if len(online) == 0 {
		return out, nil
	}
	mrows, err := s.st.DB.QueryContext(ctx, `SELECT domain_id, user_id FROM members`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	perDomain := map[string]int{}
	for mrows.Next() {
		var did, uid string
		if err := mrows.Scan(&did, &uid); err != nil {
			return nil, err
		}
		if online[uid] {
			perDomain[did]++
		}
	}
	for i := range out {
		out[i].Online = perDomain[out[i].ID]
	}
	return out, mrows.Err()
}
