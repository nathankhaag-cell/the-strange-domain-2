package rtc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/pion/rtp"
)

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "p" + hex.EncodeToString(b[:])
}

// parseRTP reads a packet and drops its header extensions: they were
// negotiated with the sender, not with the receivers (this node negotiates
// none, so browsers send none; this is a safeguard).
func parseRTP(b []byte) (*rtp.Packet, error) {
	pkt := &rtp.Packet{}
	if err := pkt.Unmarshal(b); err != nil {
		return nil, err
	}
	pkt.Header.Extension = false
	pkt.Header.ExtensionProfile = 0
	pkt.Header.Extensions = nil
	return pkt, nil
}

// unmarshal reads a client message. Unknown fields are ignored: browsers
// may add fields to ICE candidates.
func unmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
