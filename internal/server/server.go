// Package server exposes the Domain Node over HTTP and serves the web client.
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/blobs"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/rtc"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/update"
)

// The web client is embedded so the node serves it with no internet access.
// It is built from client/ (npm run build writes here) and committed, so
// `go build` needs no Node toolchain. "all:" keeps bundler chunks whose names
// start with "_".
//
//go:embed all:web
var webFS embed.FS

// Version is set at build time with -ldflags "-X .../internal/server.Version=v0.1.0".
var Version = "dev"

type Server struct {
	st      *store.Store
	dom     *domains.Service
	auth    *auth.Service
	relay   *relay.Service
	hub     *relay.Hub
	blobs   *blobs.Service
	started time.Time
	mux     *http.ServeMux
	handler http.Handler
	metrics *Metrics
	updates *update.Checker
	rtc     *rtc.SFU // nil: calls are off
}

// Option configures a Server.
type Option func(*Server)

// WithUploadLimits sets the largest attachment (bytes) and how much
// attachment data each person may keep on the node. Zero keeps the default.
func WithUploadLimits(maxSize, quota int64) Option {
	return func(s *Server) {
		if maxSize > 0 {
			s.blobs.MaxSize = maxSize
		}
		if quota > 0 {
			s.blobs.Quota = quota
		}
	}
}

func New(st *store.Store, dom *domains.Service, au *auth.Service, rl *relay.Service, hub *relay.Hub, opts ...Option) *Server {
	s := &Server{st: st, dom: dom, auth: au, relay: rl, hub: hub, blobs: blobs.NewService(st), started: time.Now(), mux: http.NewServeMux(), metrics: newMetrics()}
	for _, o := range opts {
		o(s)
	}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /api/v1/info", s.info)
	s.routes()
	s.relayRoutes()
	s.deviceRoutes()
	s.mediaRoutes()
	s.adminRoutes()
	s.callRoutes()
	web, _ := fs.Sub(webFS, "web")
	s.mux.Handle("GET /", http.FileServer(http.FS(web)))
	s.handler = s.instrument(http.HandlerFunc(s.serve))
	return s
}

// csp allows images from blob: URLs only so the client can show attachments
// and profile pictures it decrypted or fetched itself; nothing else changes.
const csp = "default-src 'self'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'"

// Metrics returns the node's traffic counters.
func (s *Server) Metrics() *Metrics { return s.metrics }

// SetUpdateChecker shows the checker's result to the node admin. Call it
// before serving.
func (s *Server) SetUpdateChecker(c *update.Checker) { s.updates = c }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", csp)
	if allowAppOrigin(w, r) {
		return // preflight answered
	}
	s.mux.ServeHTTP(w, r)
}

// appOrigins are where the phone app's bundled client runs. Capacitor serves
// it from http://localhost on Android (the app's own origin, which browsers
// count as secure, so it can call a node on the LAN over plain http without
// mixed-content blocking); https://localhost is Capacitor's other Android
// scheme and capacitor://localhost its iOS one. Only these origins may read
// API responses cross-origin. That grants nothing a page could not already
// do: the API authenticates with a bearer token the app keeps to itself,
// never with cookies, so a request from any other page carries no one's
// session. Browsers and the desktop app load the client from the node itself
// and need no CORS.
var appOrigins = map[string]bool{
	"http://localhost":      true,
	"https://localhost":     true,
	"capacitor://localhost": true,
}

// allowAppOrigin adds CORS headers for the app origins on /api/ requests. It
// reports whether it answered the request (a preflight).
func allowAppOrigin(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	h := w.Header()
	h.Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if !appOrigins[origin] {
		return false
	}
	h.Set("Access-Control-Allow-Origin", origin)
	if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	h.Set("Access-Control-Max-Age", "600")
	// The app (on localhost) talks to a node on the LAN (Private Network Access).
	if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"name":     "Strange Domain node",
		"version":  Version,
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
		"uptime_s": int(time.Since(s.started).Seconds()),
		// See compat.go.
		"api":            APILevel,
		"min_client_api": MinClientAPI,
		"features":       s.features(),
	})
}
