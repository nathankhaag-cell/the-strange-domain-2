package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/server"
)

// listener is one address the node serves on.
type listener struct {
	scheme string // http or https
	addr   net.Addr
}

// printBanner writes the start-up summary: version, data folder, every
// address the node can be opened at, and the certificate fingerprint when it
// is self-signed.
func printBanner(w io.Writer, dataDir string, lns []listener, fingerprint string) {
	fmt.Fprintf(w, "\nThe Strange Domain node %s\n", server.Version)
	fmt.Fprintf(w, "Data folder: %s\n", dataDir)
	for _, ln := range lns {
		for _, u := range listenerURLs(ln, interfaceAddrs()) {
			fmt.Fprintf(w, "Open in a browser: %s\n", u)
		}
	}
	if fingerprint != "" {
		fmt.Fprintf(w, "Self-signed certificate fingerprint (SHA-256):\n  %s\n", fingerprint)
		fmt.Fprintln(w, "Check that your browser or app shows the same fingerprint before you trust it.")
	}
	fmt.Fprintln(w)
}

// interfaceAddrs lists this machine's addresses that other devices can use:
// IPv4 and IPv6 on interfaces that are up, without loopback and link-local
// addresses (those need a zone and rarely work in a browser).
func interfaceAddrs() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			ip := pfx.Addr().Unmap()
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			out = append(out, ip)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Is4() && !out[j].Is4() })
	return out
}

// listenerURLs returns the URLs a listener can be reached at. A listener on
// all interfaces (":8743") is reachable at localhost and every interface
// address; one bound to a single address only there.
func listenerURLs(ln listener, ifaceAddrs []netip.Addr) []string {
	ap, err := netip.ParseAddrPort(ln.addr.String())
	if err != nil {
		return []string{ln.scheme + "://" + ln.addr.String()}
	}
	port := strconv.Itoa(int(ap.Port()))
	ip := ap.Addr().Unmap()
	url := func(host string) string { return ln.scheme + "://" + net.JoinHostPort(host, port) }
	if !ip.IsUnspecified() {
		if ip.IsLoopback() {
			return []string{url("localhost")}
		}
		return []string{url(ip.String())}
	}
	// An IPv4-only wildcard ("0.0.0.0:8743") does not answer on IPv6.
	v4only := ap.Addr().Is4()
	out := []string{url("localhost")}
	for _, a := range ifaceAddrs {
		if v4only && !a.Is4() {
			continue
		}
		out = append(out, url(a.String()))
	}
	return out
}

// statusLoop logs a one-line summary of the node's traffic every interval.
func statusLoop(ctx context.Context, srv *server.Server, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var prev server.Snapshot
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := srv.Metrics().Snapshot()
		counts, err := srv.Counts(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("status: could not read counts", "err", err)
		}
		slog.Info("status",
			"devices_online", cur.Devices,
			"streams", cur.Streams,
			"users", counts.Users,
			"domains", counts.Domains,
			"req_per_min", cur.RequestsPerMin,
			"messages", fmt.Sprintf("%d (+%d)", cur.Messages, cur.Messages-prev.Messages),
			"in", humanBytes(cur.BytesIn),
			"out", humanBytes(cur.BytesOut),
			"errors", fmt.Sprintf("%d (+%d)", cur.Errors, cur.Errors-prev.Errors),
		)
		prev = cur
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// parseLevel reads -log-level.
func parseLevel(s string) (slog.Level, error) {
	var l slog.Level
	err := l.UnmarshalText([]byte(strings.TrimSpace(s)))
	return l, err
}
