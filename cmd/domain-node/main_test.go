package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/tlsutil"
)

// startNode runs the node in the background and returns its addresses ("" if
// not served).
func startNode(t *testing.T, o options) (httpAddr, httpsAddr string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan [2]string, 1)
	done := make(chan error, 1)
	str := func(a net.Addr) string {
		if a == nil {
			return ""
		}
		return a.String()
	}
	go func() { done <- run(ctx, o, func(h, hs net.Addr) { addrc <- [2]string{str(h), str(hs)} }) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	select {
	case a := <-addrc:
		return a[0], a[1]
	case err := <-done:
		t.Fatalf("run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("node did not start")
	}
	return "", ""
}

// pinnedClient trusts exactly the certificate whose fingerprint is want, as
// a person does after comparing it with the node's log.
func pinnedClient(t *testing.T, want string) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // checked by fingerprint below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if got := tlsutil.Fingerprint(cs.PeerCertificates[0].Raw); got != want {
				t.Errorf("served %s, stored %s", got, want)
			}
			return nil
		},
	}}}
}

func storedFingerprint(t *testing.T, data string) string {
	cert, err := tlsutil.Load(filepath.Join(data, "tls", "cert.pem"), filepath.Join(data, "tls", "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	return tlsutil.Fingerprint(cert.Certificate[0])
}

func TestRunPlainHTTP(t *testing.T) {
	addr, tlsAddr := startNode(t, options{listen: "127.0.0.1:0", dataDir: t.TempDir()})
	if tlsAddr != "" {
		t.Fatal("HTTPS served without a TLS option")
	}
	res, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("healthz: %d", res.StatusCode)
	}
}

func TestRunSelfSignedTLS(t *testing.T) {
	data := t.TempDir()
	plain, addr := startNode(t, options{listen: "127.0.0.1:0", dataDir: data, tlsSelfSigned: true})
	if plain != "" {
		t.Fatal("plain HTTP served alongside TLS without -tls-listen")
	}

	// The certificate is kept in the data folder and is the one served.
	client := pinnedClient(t, storedFingerprint(t, data))
	res, err := client.Get("https://" + addr + "/api/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"version"`) {
		t.Fatalf("info: %d %s", res.StatusCode, body)
	}

	// Plain HTTP on the TLS port is refused.
	if res, err := http.Get("http://" + addr + "/healthz"); err == nil {
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("plain HTTP served on the TLS port")
		}
	}
}

func TestRunHTTPAndHTTPS(t *testing.T) {
	data := t.TempDir()
	plain, secure := startNode(t, options{listen: "127.0.0.1:0", tlsListen: "127.0.0.1:0", dataDir: data, tlsSelfSigned: true})
	if plain == "" || secure == "" || plain == secure {
		t.Fatalf("addresses %q %q", plain, secure)
	}
	res, err := http.Get("http://" + plain + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = pinnedClient(t, storedFingerprint(t, data)).Get("https://" + secure + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("https healthz: %d", res.StatusCode)
	}
}

func TestTLSFlagValidation(t *testing.T) {
	for _, o := range []options{
		{tlsSelfSigned: true, tlsCert: "a.pem", tlsKey: "b.pem"},
		{tlsCert: "a.pem"},
		{tlsKey: "b.pem"},
		{tlsCert: "missing.pem", tlsKey: "missing.pem"},
		{tlsListen: ":8744"},
	} {
		o.dataDir = t.TempDir()
		if _, err := o.tlsConfig(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}
