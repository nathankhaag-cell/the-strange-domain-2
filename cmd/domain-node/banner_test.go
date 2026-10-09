package main

import (
	"bytes"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestListenerURLs(t *testing.T) {
	ifaces := []netip.Addr{netip.MustParseAddr("192.168.40.232"), netip.MustParseAddr("fd00::5")}
	tcp := func(s string) net.Addr { return net.TCPAddrFromAddrPort(netip.MustParseAddrPort(s)) }
	cases := []struct {
		ln   listener
		want []string
	}{
		{listener{"http", tcp("[::]:8743")}, []string{"http://localhost:8743", "http://192.168.40.232:8743", "http://[fd00::5]:8743"}},
		{listener{"https", tcp("0.0.0.0:8744")}, []string{"https://localhost:8744", "https://192.168.40.232:8744"}},
		{listener{"http", tcp("127.0.0.1:8743")}, []string{"http://localhost:8743"}},
		{listener{"http", tcp("192.168.40.232:80")}, []string{"http://192.168.40.232:80"}},
	}
	for _, c := range cases {
		if got := listenerURLs(c.ln, ifaces); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.ln.addr, got, c.want)
		}
	}
}

func TestBanner(t *testing.T) {
	var buf bytes.Buffer
	printBanner(&buf, "/data", []listener{{"http", net.TCPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:8743"))}}, "AB:CD")
	out := buf.String()
	for _, want := range []string{"The Strange Domain node", "Data folder: /data", "Open in a browser: http://localhost:8743", "AB:CD"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner lacks %q:\n%s", want, out)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KiB", 5 << 20: "5.0 MiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
