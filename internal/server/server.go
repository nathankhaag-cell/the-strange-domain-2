// Package server exposes the Domain Node over HTTP and serves the web client.
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"runtime"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
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
	started time.Time
	mux     *http.ServeMux
}

func New(st *store.Store, dom *domains.Service, au *auth.Service, rl *relay.Service, hub *relay.Hub) *Server {
	s := &Server{st: st, dom: dom, auth: au, relay: rl, hub: hub, started: time.Now(), mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /api/v1/info", s.info)
	s.routes()
	s.relayRoutes()
	s.deviceRoutes()
	web, _ := fs.Sub(webFS, "web")
	s.mux.Handle("GET /", http.FileServer(http.FS(web)))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; frame-ancestors 'none'")
	s.mux.ServeHTTP(w, r)
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
	})
}
