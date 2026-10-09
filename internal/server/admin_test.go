package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type adminStatsResp struct {
	Version string `json:"version"`
	DBBytes int64  `json:"db_bytes"`
	Counts  Counts `json:"counts"`
	Online  int    `json:"online_users"`
	Traffic Snapshot
	Domains []struct {
		Name     string `json:"name"`
		Members  int    `json:"members"`
		Online   int    `json:"online"`
		Channels int    `json:"channels"`
		Messages int    `json:"messages"`
	} `json:"domains"`
	RecentErrors []ErrorEntry `json:"recent_errors"`
	Update       any          `json:"update"`
}

func TestAdminStats(t *testing.T) {
	ts := newTestServer(t)
	admin, _ := signUp(t, ts, "abbot", "")
	var d domainJSON
	call(t, ts, "POST", "/api/v1/domains", admin.token, map[string]string{"name": "Sector 7"}, &d)
	var sm struct{ Summons string }
	call(t, ts, "POST", "/api/v1/domains/"+d.ID+"/summons", admin.token, map[string]int{"max_uses": 5}, &sm)
	member, code := signUp(t, ts, "brother", sm.Summons)
	if code != http.StatusCreated {
		t.Fatalf("register: %d", code)
	}

	// Anyone else gets 403; no token gets 401.
	if code := call(t, ts, "GET", "/api/v1/admin/stats", member.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("non-admin: got %d, want 403", code)
	}
	if code := call(t, ts, "GET", "/api/v1/admin/stats", "", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", code)
	}

	var before adminStatsResp
	if code := call(t, ts, "GET", "/api/v1/admin/stats", admin.token, nil, &before); code != 200 {
		t.Fatalf("admin: %d", code)
	}

	// The member opens a stream and sends two messages.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	wsjson.Write(ctx, conn, map[string]string{"token": member.token})
	var ready map[string]string
	if err := wsjson.Read(ctx, conn, &ready); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Channels []struct{ ID string } `json:"channels"`
	}
	call(t, ts, "GET", "/api/v1/domains/"+d.ID, member.token, nil, &detail)
	for i := 0; i < 2; i++ {
		if code := call(t, ts, "POST", "/api/v1/groups/"+detail.Channels[0].ID+"/messages", member.token,
			map[string]any{"kind": "application", "epoch": 0, "data": []byte("secret ciphertext")}, nil); code != 201 {
			t.Fatalf("send: %d", code)
		}
	}

	res, err := http.NewRequest("GET", ts.URL+"/api/v1/admin/stats", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Header.Set("Authorization", "Bearer "+admin.token)
	resp, err := http.DefaultClient.Do(res)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var after adminStatsResp
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after.Traffic.Messages != before.Traffic.Messages+2 {
		t.Errorf("messages: %d -> %d", before.Traffic.Messages, after.Traffic.Messages)
	}
	if after.Traffic.Streams != 1 || after.Traffic.Devices != 1 || after.Online != 1 {
		t.Errorf("live: streams %d devices %d online %d", after.Traffic.Streams, after.Traffic.Devices, after.Online)
	}
	if after.Traffic.Requests <= before.Traffic.Requests || after.Traffic.RequestsPerMin == 0 {
		t.Errorf("requests: %+v", after.Traffic)
	}
	if after.Traffic.BytesIn == 0 || after.Traffic.BytesOut == 0 {
		t.Errorf("bytes: %+v", after.Traffic)
	}
	if after.Traffic.Rejected < 1 { // the 403 and 401 above
		t.Errorf("rejected: %d", after.Traffic.Rejected)
	}
	if after.Counts.Users != 2 || after.Counts.Domains != 1 || after.Counts.Devices != 2 {
		t.Errorf("counts: %+v", after.Counts)
	}
	if len(after.Domains) != 1 || after.Domains[0].Members != 2 || after.Domains[0].Messages != 2 ||
		after.Domains[0].Online != 1 || after.Domains[0].Channels != 2 {
		t.Errorf("domains: %+v", after.Domains)
	}
	if after.DBBytes == 0 || after.Version == "" {
		t.Errorf("node: %+v", after)
	}
	// Nothing secret leaks into the stats.
	for _, secret := range []string{admin.token, member.token, sm.Summons, "c2VjcmV0IGNpcGhlcnRleHQ=", "secret ciphertext", "127.0.0.1"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("stats contain %q", secret)
		}
	}

	// Closing the stream is counted.
	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		var s adminStatsResp
		call(t, ts, "GET", "/api/v1/admin/stats", admin.token, nil, &s)
		if s.Traffic.Streams == 0 && s.Traffic.Devices == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream still counted: %+v", s.Traffic)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMetricsCounters(t *testing.T) {
	m := newMetrics()
	now := time.Unix(1_000_000, 0)
	m.record("GET /a", 200, 10, 20, now)
	m.record("POST /b/{id}", 500, 1, 2, now.Add(10*time.Second))
	m.record("GET /c", 404, 0, 5, now.Add(20*time.Second))
	m.streamOpened("d1")
	m.streamOpened("d1")
	m.streamOpened("d2")
	m.streamClosed("d1")

	s := m.Snapshot()
	if s.Requests != 3 || s.Errors != 1 || s.Rejected != 1 || s.BytesIn != 11 || s.BytesOut != 27 {
		t.Fatalf("snapshot: %+v", s)
	}
	if s.Streams != 2 || s.Devices != 2 {
		t.Fatalf("streams %d devices %d", s.Streams, s.Devices)
	}
	m.streamClosed("d1")
	if s := m.Snapshot(); s.Devices != 1 {
		t.Fatalf("devices after close: %d", s.Devices)
	}
	if e := m.RecentErrors(); len(e) != 1 || e[0].Route != "POST /b/{id}" || e[0].Status != 500 {
		t.Fatalf("recent errors: %+v", e)
	}
	// The window counts only the last minute.
	if n := m.rate.lastMinute(now.Add(30 * time.Second)); n != 3 {
		t.Fatalf("rate at +30s: %d", n)
	}
	if n := m.rate.lastMinute(now.Add(75 * time.Second)); n != 1 {
		t.Fatalf("rate at +75s: %d", n)
	}
	// Recent errors keep only the newest.
	for i := 0; i < maxRecentErrors+5; i++ {
		m.record("GET /x", 503, 0, 0, now)
	}
	if e := m.RecentErrors(); len(e) != maxRecentErrors || e[0].Route != "GET /x" {
		t.Fatalf("recent errors cap: %d", len(e))
	}
}

func TestMetricsConcurrent(t *testing.T) {
	m := newMetrics()
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 1000; i++ {
				m.record("GET /", 200, 1, 1, time.Now())
			}
			done <- struct{}{}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	if s := m.Snapshot(); s.Requests != 8000 || s.BytesIn != 8000 {
		t.Fatalf("%+v", s)
	}
}
