// Command domain-node runs a Strange Domain node: one self-hosted server
// that can live on a home PC, a Raspberry Pi or a cloud VPS.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/auth"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/relay"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/server"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/tlsutil"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/update"
)

type options struct {
	listen        string
	tlsListen     string
	dataDir       string
	tlsCert       string
	tlsKey        string
	tlsSelfSigned bool

	statusInterval time.Duration // 0: no status line
	updateCheck    bool
	out            io.Writer // start-up summary; nil: none
}

func main() {
	o := options{out: os.Stdout}
	flag.StringVar(&o.listen, "listen", ":8743", "address to listen on")
	flag.StringVar(&o.dataDir, "data", defaultDataDir(), "directory for the node's database and files")
	flag.StringVar(&o.tlsCert, "tls-cert", "", "serve HTTPS with this certificate (PEM file; needs -tls-key)")
	flag.StringVar(&o.tlsKey, "tls-key", "", "private key for -tls-cert (PEM file)")
	flag.BoolVar(&o.tlsSelfSigned, "tls-self-signed", false, "serve HTTPS with a self-signed certificate kept in <data>/tls (made on first start)")
	flag.StringVar(&o.tlsListen, "tls-listen", "", "with a TLS option: serve HTTPS on this address and keep plain HTTP on -listen (for example :8744)")
	flag.DurationVar(&o.statusInterval, "status-interval", time.Minute, "log a one-line traffic summary this often (0 turns it off)")
	flag.BoolVar(&o.updateCheck, "update-check", true, "check GitHub for a newer release at start-up and once a day (needs internet; failures are ignored)")
	logLevel := flag.String("log-level", "info", "log detail: debug (also logs every request), info, warn or error")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *version {
		fmt.Println(server.Version)
		return
	}
	level, err := parseLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "-log-level must be debug, info, warn or error")
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, o, nil); err != nil {
		slog.Error("node stopped", "err", err)
		os.Exit(1)
	}
}

// tlsConfig returns nil for plain HTTP.
func (o options) tlsConfig() (*tls.Config, error) {
	switch {
	case o.tlsListen != "" && !o.tlsSelfSigned && o.tlsCert == "":
		return nil, errors.New("-tls-listen needs -tls-self-signed or -tls-cert/-tls-key")
	case o.tlsSelfSigned && (o.tlsCert != "" || o.tlsKey != ""):
		return nil, errors.New("use either -tls-self-signed or -tls-cert/-tls-key, not both")
	case (o.tlsCert == "") != (o.tlsKey == ""):
		return nil, errors.New("-tls-cert and -tls-key go together")
	case o.tlsCert != "":
		cert, err := tlsutil.Load(o.tlsCert, o.tlsKey)
		if err != nil {
			return nil, err
		}
		return tlsutil.Config(cert), nil
	case o.tlsSelfSigned:
		dir := filepath.Join(o.dataDir, "tls")
		cert, err := tlsutil.SelfSigned(dir, tlsutil.LocalHosts(), time.Now())
		if err != nil {
			return nil, err
		}
		slog.Info("using a self-signed certificate; check this fingerprint matches the one your browser or app shows",
			"sha256", tlsutil.Fingerprint(cert.Certificate[0]), "dir", dir)
		return tlsutil.Config(cert), nil
	}
	return nil, nil
}

// run serves until ctx is done. ready, if set, gets the listening addresses
// (nil when that kind is not served).
func run(ctx context.Context, o options, ready func(httpAddr, httpsAddr net.Addr)) error {
	tlsCfg, err := o.tlsConfig()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, o.dataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	dom := domains.NewService(st)
	hub := relay.NewHub()
	handler := server.New(st, dom, auth.NewService(st, dom), relay.NewService(st, hub), hub)
	var updates *update.Checker
	if o.updateCheck {
		updates = &update.Checker{Current: server.Version}
		handler.SetUpdateChecker(updates)
	}

	// Plain HTTP on -listen, unless TLS is on without -tls-listen, in which
	// case -listen serves HTTPS only.
	var httpLn, httpsLn net.Listener
	httpsAddr := ""
	if tlsCfg != nil {
		httpsAddr = o.listen
		if o.tlsListen != "" {
			httpsAddr = o.tlsListen
		}
	}
	if tlsCfg == nil || o.tlsListen != "" {
		if httpLn, err = net.Listen("tcp", o.listen); err != nil {
			return err
		}
	}
	if httpsAddr != "" {
		ln, err := net.Listen("tcp", httpsAddr)
		if err != nil {
			if httpLn != nil {
				httpLn.Close()
			}
			return err
		}
		httpsLn = tls.NewListener(ln, tlsCfg)
	}

	var servers []*http.Server
	errc := make(chan error, 2)
	serve := func(ln net.Listener, scheme string) {
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, TLSConfig: tlsCfg}
		servers = append(servers, srv)
		slog.Info("node listening", "addr", ln.Addr().String(), "scheme", scheme, "data", o.dataDir, "version", server.Version)
		go func() { errc <- srv.Serve(ln) }()
	}
	var ha, hsa net.Addr
	var lns []listener
	if httpLn != nil {
		serve(httpLn, "http")
		ha = httpLn.Addr()
		lns = append(lns, listener{"http", ha})
	}
	if httpsLn != nil {
		serve(httpsLn, "https")
		hsa = httpsLn.Addr()
		lns = append(lns, listener{"https", hsa})
	}
	if o.out != nil {
		fingerprint := ""
		if o.tlsSelfSigned && tlsCfg != nil && len(tlsCfg.Certificates) > 0 {
			fingerprint = tlsutil.Fingerprint(tlsCfg.Certificates[0].Certificate[0])
		}
		printBanner(o.out, o.dataDir, lns, fingerprint)
	}
	if ready != nil {
		ready(ha, hsa)
	}

	bg, stopBg := context.WithCancel(ctx)
	defer stopBg()
	if updates != nil && updates.Enabled() {
		go updates.Run(bg, 24*time.Hour)
	}
	if o.statusInterval > 0 {
		go statusLoop(bg, handler, o.statusInterval)
	}

	var runErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}

// defaultDataDir picks a per-user location that works on Windows, macOS and Linux.
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "strange-domain")
	}
	return "strange-domain-data"
}
