//go:build rtcdebug

package rtc

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/pion/rtp"
)

// Builds with -tags rtcdebug (never releases) write the first payloads the
// node forwards to the file named by SD_RTC_DUMP, one line per packet:
// "<room> <participant>/<slot> <timestamp> <marker> <hex payload>". The
// browser end-to-end test reads it to show that what passes through the
// node is ciphertext.
func init() {
	path := os.Getenv("SD_RTC_DUMP")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	var mu sync.Mutex
	count := map[string]int{}
	debugTap = func(roomID, slot string, pkt *rtp.Packet) {
		mu.Lock()
		defer mu.Unlock()
		if count[slot] >= 400 {
			return
		}
		count[slot]++
		fmt.Fprintf(f, "%s %s %d %t %s\n", roomID, slot, pkt.Timestamp, pkt.Marker, hex.EncodeToString(pkt.Payload))
		f.Sync()
	}
}
