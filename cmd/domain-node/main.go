// Command domain-node runs a Strange Domain node: one self-hosted server
// that can live on a home PC, a Raspberry Pi or a cloud VPS.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/server"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

func main() {
	listen := flag.String("listen", ":8743", "address to listen on")
	dataDir := flag.String("data", defaultDataDir(), "directory for the node's database and files")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *version {
		fmt.Println(server.Version)
		return
	}
	if err := run(*listen, *dataDir); err != nil {
		slog.Error("node stopped", "err", err)
		os.Exit(1)
	}
}

func run(listen, dataDir string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	dom := domains.NewService(st)

	srv := &http.Server{
		Addr:              listen,
		Handler:           server.New(st, dom, auth.NewService(st, dom)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("node listening", "addr", listen, "data", dataDir, "version", server.Version)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// defaultDataDir picks a per-user location that works on Windows, macOS and Linux.
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "strange-domain")
	}
	return "strange-domain-data"
}
