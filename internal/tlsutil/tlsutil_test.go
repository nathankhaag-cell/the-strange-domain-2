package tlsutil

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSelfSignedIsCreatedOnceAndReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Now()
	c1, err := SelfSigned(dir, []string{"localhost", "127.0.0.1", "node.lan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	leaf := c1.Leaf
	if leaf == nil {
		t.Fatal("no leaf")
	}
	if err := leaf.VerifyHostname("node.lan"); err != nil {
		t.Errorf("node.lan: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("127.0.0.1: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "key.pem"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("key.pem mode %v, want 0600", fi.Mode().Perm())
		}
	}

	// A restart keeps the same certificate, so the fingerprint people
	// compared stays valid.
	c2, err := SelfSigned(dir, []string{"other"}, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(c1.Certificate[0]) != Fingerprint(c2.Certificate[0]) {
		t.Fatal("certificate changed across restarts")
	}

	// Once expired, a new one is made.
	c3, err := SelfSigned(dir, []string{"localhost"}, now.Add(validFor+48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(c1.Certificate[0]) == Fingerprint(c3.Certificate[0]) {
		t.Fatal("expired certificate was reused")
	}
}

func TestSelfSignedRejectsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("junk"), 0o644)
	os.WriteFile(filepath.Join(dir, "key.pem"), []byte("junk"), 0o600)
	if _, err := SelfSigned(dir, nil, time.Now()); err == nil {
		t.Fatal("corrupt files accepted")
	}
}

func TestFingerprintFormat(t *testing.T) {
	der := []byte("hello")
	sum := sha256.Sum256(der)
	want := strings.ToUpper(hex.EncodeToString(sum[:]))
	got := strings.ReplaceAll(Fingerprint(der), ":", "")
	if got != want || len(Fingerprint(der)) != 32*3-1 {
		t.Fatalf("got %s want %s", Fingerprint(der), want)
	}
}

func TestServesHTTPSWithSelfSigned(t *testing.T) {
	cert, err := SelfSigned(t.TempDir(), LocalHosts(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	ts.TLS = Config(cert)
	ts.StartTLS()
	defer ts.Close()

	// A client that trusts exactly this certificate (as a pinned browser or
	// the desktop app does) can connect and the hostname checks out.
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	res, err := client.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := Fingerprint(res.TLS.PeerCertificates[0].Raw); got != Fingerprint(cert.Certificate[0]) {
		t.Fatalf("served %s", got)
	}

	// An ordinary client refuses it: it is self-signed.
	if _, err := http.Get(ts.URL); err == nil {
		t.Fatal("self-signed certificate trusted by default")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := SelfSigned(dir, []string{"localhost"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "missing.pem"), filepath.Join(dir, "key.pem")); err == nil {
		t.Fatal("missing file accepted")
	}
}
