// Package tlsutil gives the node HTTPS: either a certificate the operator
// supplies, or a self-signed one it creates once and keeps in its data folder.
//
// Browsers only offer WebCrypto and the other secure-context features the
// client needs on https:// pages (or on localhost), so a browser on another
// machine on the LAN needs the node to speak TLS.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
	// Self-signed certificates are trusted by fingerprint, not by a CA, so a
	// long life is fine and keeps the fingerprint people compared stable.
	validFor = 10 * 365 * 24 * time.Hour
)

// Config returns a server TLS configuration for cert.
func Config(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

// Load reads a certificate and key the operator supplied (PEM files).
func Load(certPath, keyPath string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load TLS certificate: %w", err)
	}
	return cert, nil
}

// SelfSigned returns the node's self-signed certificate from dir, creating it
// (and dir) if it does not exist or has expired. The same certificate is
// reused across restarts so its fingerprint stays the same. hosts are the
// names and IP addresses to put in a new certificate.
func SelfSigned(dir string, hosts []string, now time.Time) (tls.Certificate, error) {
	cp, kp := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	cert, err := tls.LoadX509KeyPair(cp, kp)
	switch {
	case err == nil:
		leaf, perr := x509.ParseCertificate(cert.Certificate[0])
		if perr == nil && now.Before(leaf.NotAfter) {
			cert.Leaf = leaf
			return cert, nil
		}
		// Expired or unreadable: make a new one.
	case !errors.Is(err, fs.ErrNotExist):
		return tls.Certificate{}, fmt.Errorf("load self-signed certificate (delete %s and %s to make a new one): %w", cp, kp, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	certPEM, keyPEM, err := generate(hosts, now)
	if err != nil {
		return tls.Certificate{}, err
	}
	// Key first, and only readable by the node's user.
	if err := os.WriteFile(kp, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(cp, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	cert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	return cert, err
}

func generate(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Strange Domain node", Organization: []string{"The Strange Domain"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// Fingerprint is the SHA-256 of a certificate's DER bytes as colon-separated
// upper-case hex, the form browsers and the desktop app show.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// LocalHosts lists the names and addresses this machine is likely to be
// reached by on a LAN: localhost, its hostname (and hostname.local), and the
// addresses of its network interfaces.
func LocalHosts() []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if h, err := os.Hostname(); err == nil && h != "" {
		hosts = append(hosts, h)
		if !strings.Contains(h, ".") {
			hosts = append(hosts, h+".local")
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		hosts = append(hosts, ipn.IP.String())
	}
	return hosts
}
