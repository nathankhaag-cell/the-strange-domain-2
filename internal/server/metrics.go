package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics counts what the node does, in memory, for the node's host: the
// terminal status line and the admin view. Counters are atomics so the hot
// path costs a few adds; nothing is written to disk and nothing here holds
// message contents, tokens or addresses.
type Metrics struct {
	requests atomic.Int64 // HTTP requests answered
	errors   atomic.Int64 // 5xx responses
	rejected atomic.Int64 // 4xx responses
	bytesIn  atomic.Int64 // request bodies read
	bytesOut atomic.Int64 // response bodies and stream events written
	messages atomic.Int64 // messages relayed (accepted for delivery)
	streams  atomic.Int64 // live WebSocket streams

	rate rateWindow

	mu      sync.Mutex
	devices map[string]int // live streams per device
	recent  []ErrorEntry   // newest last, at most maxRecentErrors
}

const maxRecentErrors = 20

// ErrorEntry is one failed request. Route is the route pattern (such as
// "POST /api/v1/groups/{gid}/messages"), never the concrete path, so IDs and
// invite codes do not end up in it.
type ErrorEntry struct {
	Time   int64  `json:"time"`
	Route  string `json:"route"`
	Status int    `json:"status"`
}

func newMetrics() *Metrics {
	return &Metrics{devices: map[string]int{}}
}

// Snapshot is a point-in-time copy of the counters.
type Snapshot struct {
	Requests       int64 `json:"requests"`
	RequestsPerMin int64 `json:"requests_per_min"`
	Errors         int64 `json:"errors"`
	Rejected       int64 `json:"rejected"`
	BytesIn        int64 `json:"bytes_in"`
	BytesOut       int64 `json:"bytes_out"`
	Messages       int64 `json:"messages"`
	Streams        int64 `json:"streams"`
	Devices        int   `json:"devices"`
}

func (m *Metrics) Snapshot() Snapshot {
	m.mu.Lock()
	devices := len(m.devices)
	m.mu.Unlock()
	return Snapshot{
		Requests:       m.requests.Load(),
		RequestsPerMin: m.rate.lastMinute(time.Now()),
		Errors:         m.errors.Load(),
		Rejected:       m.rejected.Load(),
		BytesIn:        m.bytesIn.Load(),
		BytesOut:       m.bytesOut.Load(),
		Messages:       m.messages.Load(),
		Streams:        m.streams.Load(),
		Devices:        devices,
	}
}

// RecentErrors returns the latest failed requests, newest first.
func (m *Metrics) RecentErrors() []ErrorEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ErrorEntry, len(m.recent))
	for i, e := range m.recent {
		out[len(m.recent)-1-i] = e
	}
	return out
}

func (m *Metrics) streamOpened(deviceID string) {
	m.streams.Add(1)
	m.mu.Lock()
	m.devices[deviceID]++
	m.mu.Unlock()
}

func (m *Metrics) streamClosed(deviceID string) {
	m.streams.Add(-1)
	m.mu.Lock()
	if m.devices[deviceID]--; m.devices[deviceID] <= 0 {
		delete(m.devices, deviceID)
	}
	m.mu.Unlock()
}

func (m *Metrics) record(route string, status int, in, out int64, now time.Time) {
	m.requests.Add(1)
	m.rate.add(now)
	m.bytesIn.Add(in)
	m.bytesOut.Add(out)
	switch {
	case status >= 500:
		m.errors.Add(1)
		m.mu.Lock()
		if len(m.recent) == maxRecentErrors {
			copy(m.recent, m.recent[1:])
			m.recent = m.recent[:maxRecentErrors-1]
		}
		m.recent = append(m.recent, ErrorEntry{Time: now.Unix(), Route: route, Status: status})
		m.mu.Unlock()
	case status >= 400:
		m.rejected.Add(1)
	}
}

// rateWindow counts events in one-second buckets over the last minute.
// Concurrent updates around a bucket rollover may lose a count; that is
// fine for a status display.
type rateWindow struct {
	buckets [60]struct {
		sec atomic.Int64
		n   atomic.Int64
	}
}

func (w *rateWindow) add(now time.Time) {
	sec := now.Unix()
	b := &w.buckets[sec%60]
	if old := b.sec.Load(); old != sec && b.sec.CompareAndSwap(old, sec) {
		b.n.Store(0)
	}
	b.n.Add(1)
}

func (w *rateWindow) lastMinute(now time.Time) int64 {
	sec := now.Unix()
	var total int64
	for i := range w.buckets {
		b := &w.buckets[i]
		if s := b.sec.Load(); s > sec-60 && s <= sec {
			total += b.n.Load()
		}
	}
	return total
}

// instrument wraps the handler to count requests, bytes and errors, and logs
// each request at debug level.
func (s *Server) instrument(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body := &countingBody{ReadCloser: r.Body}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = body
		}
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		// The mux sets r.Pattern on this request; static files have no
		// IDs in their paths, so they are safe to show.
		route := r.Pattern
		if route == "" || route == "GET /" {
			route = r.Method + " " + r.URL.Path
		}
		s.metrics.record(route, status, body.n, sw.n, start)
		ctx := context.Background()
		if status >= 500 {
			slog.Warn("request failed", "route", route, "status", status)
		} else if slog.Default().Enabled(ctx, slog.LevelDebug) {
			slog.Debug("request", "route", route, "status", status, "ms", time.Since(start).Milliseconds(),
				"in", body.n, "out", sw.n, "remote", r.RemoteAddr)
		}
	})
}

type countingBody struct {
	io.ReadCloser
	n int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	return n, err
}

type statusWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.n += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController and the WebSocket upgrade reach the
// underlying writer (Flush, Hijack).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
