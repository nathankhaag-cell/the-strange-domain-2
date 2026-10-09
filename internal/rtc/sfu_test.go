package rtc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// ---- a fake signalling connection and a pion client ----

type memConn struct {
	toServer chan []byte
	toClient chan []byte
	closed   chan struct{}
	once     sync.Once
}

func newMemConn() *memConn {
	return &memConn{toServer: make(chan []byte, 64), toClient: make(chan []byte, 64), closed: make(chan struct{})}
}

func (c *memConn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case c.toClient <- b:
		return nil
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *memConn) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-c.toServer:
		return b, nil
	case <-c.closed:
		return nil, errors.New("closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *memConn) close() { c.once.Do(func() { close(c.closed) }) }

type serverMsg struct {
	Type     string               `json:"type"`
	SDP      string               `json:"sdp"`
	Tracks   map[string]TrackInfo `json:"tracks"`
	Publish  map[string]string    `json:"publish"`
	ID       string               `json:"id"`
	CanSpeak bool                 `json:"can_speak"`
	Room     RoomInfo             `json:"room"`
	Reason   string               `json:"reason"`
	UserID   string               `json:"user_id"`
}

type client struct {
	t      *testing.T
	conn   *memConn
	pc     *webrtc.PeerConnection
	audio  *webrtc.TrackLocalStaticRTP
	joined chan error

	mu       sync.Mutex
	msgs     []serverMsg
	received map[string][][]byte // forwarded payloads, by owner participant
	tracks   map[string]TrackInfo
}

func newClient(t *testing.T) *client {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "a", "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTrack(audio); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, conn: newMemConn(), pc: pc, audio: audio, joined: make(chan error, 1),
		received: map[string][][]byte{}, tracks: map[string]TrackInfo{}}
	pc.OnTrack(func(tr *webrtc.TrackRemote, recv *webrtc.RTPReceiver) {
		var mid string
		for _, x := range pc.GetTransceivers() {
			if x.Receiver() == recv {
				mid = x.Mid()
			}
		}
		go func() {
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil {
					return
				}
				c.mu.Lock()
				owner := c.tracks[mid].Participant
				c.received[owner] = append(c.received[owner], append([]byte(nil), pkt.Payload...))
				c.mu.Unlock()
			}
		}()
	})
	t.Cleanup(func() {
		c.conn.close()
		pc.Close()
	})
	go c.loop()
	return c
}

func (c *client) loop() {
	for {
		var b []byte
		select {
		case b = <-c.conn.toClient:
		case <-c.conn.closed:
			return
		}
		var m serverMsg
		if err := json.Unmarshal(b, &m); err != nil {
			c.t.Errorf("bad server message: %v", err)
			return
		}
		c.mu.Lock()
		c.msgs = append(c.msgs, m)
		if m.Type == "offer" {
			for k, v := range m.Tracks {
				c.tracks[k] = v
			}
		}
		c.mu.Unlock()
		if m.Type == "offer" {
			if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				c.t.Errorf("set offer: %v", err)
				return
			}
			ans, err := c.pc.CreateAnswer(nil)
			if err != nil {
				c.t.Errorf("answer: %v", err)
				return
			}
			g := webrtc.GatheringCompletePromise(c.pc)
			if err := c.pc.SetLocalDescription(ans); err != nil {
				c.t.Errorf("set answer: %v", err)
				return
			}
			<-g
			out, _ := json.Marshal(map[string]string{"type": "answer", "sdp": c.pc.LocalDescription().SDP})
			select {
			case c.conn.toServer <- out:
			case <-c.conn.closed:
				return
			}
		}
	}
}

func (c *client) join(s *SFU, user string, h Hello) {
	go func() { c.joined <- s.Join(context.Background(), c.conn, user, user+"-dev", h) }()
}

func (c *client) last(typ string) (serverMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].Type == typ {
			return c.msgs[i], true
		}
	}
	return serverMsg{}, false
}

func (c *client) payloadsFrom(pid string) [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.received[pid]...)
}

// sendAudio writes numbered packets with a recognisable payload until stop.
func (c *client) sendAudio(tag string, stop <-chan struct{}) {
	var seq uint16
	tk := time.NewTicker(20 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
		}
		seq++
		payload := fakeCiphertext(tag, seq)
		_ = c.audio.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: payload})
	}
}

// fakeCiphertext stands in for an encrypted frame: the node must pass it on
// byte for byte.
func fakeCiphertext(tag string, seq uint16) []byte {
	b := []byte("E2EE:" + tag + ":")
	b = binary.BigEndian.AppendUint16(b, seq)
	return append(b, bytes.Repeat([]byte{0xA5}, 40)...)
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---- access rules for the tests ----

type rules struct {
	mu    sync.Mutex
	allow map[string]Access // by user; missing: forbidden
}

var errForbidden = errors.New("forbidden")

func (r *rules) access(_ context.Context, room, user, _ string) (Access, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.allow[room+"/"+user]
	if !ok {
		return Access{}, errForbidden
	}
	return a, nil
}

func (r *rules) set(room, user string, a *Access) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == nil {
		delete(r.allow, room+"/"+user)
		return
	}
	r.allow[room+"/"+user] = *a
}

type notes struct {
	mu   sync.Mutex
	seen map[string]int
}

func (n *notes) notify(users []string, room string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, u := range users {
		n.seen[u+"/"+room]++
	}
}

func (n *notes) count(user, room string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.seen[user+"/"+room]
}

func newTestSFU(t *testing.T, video bool) (*SFU, *rules, *notes) {
	t.Helper()
	r := &rules{allow: map[string]Access{}}
	n := &notes{seen: map[string]int{}}
	s, err := New(Config{Video: video}, r.access, n.notify)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, r, n
}

// ---- tests ----

func TestJoinNeedsAccess(t *testing.T) {
	s, r, _ := newTestSFU(t, false)
	r.set("relay", "alice", &Access{Relay: true, CanSpeak: true})
	c := newClient(t)
	err := s.Join(context.Background(), c.conn, "eve", "eve-dev", Hello{Room: "relay"})
	if !errors.Is(err, errForbidden) {
		t.Fatalf("outsider joined: %v", err)
	}
	if err := s.Join(context.Background(), c.conn, "alice", "d", Hello{}); !errors.Is(err, ErrBadHello) {
		t.Fatalf("join without a room: %v", err)
	}
	if len(s.Rooms()) != 0 {
		t.Fatal("a refused join left a room behind")
	}
}

// Two people in a room hear each other; the node forwards payloads byte for
// byte (it has no keys and does not touch frames); someone muted in the
// domain can listen but is not heard; leaving and losing access work.
func TestRoomForwardingAndLifecycle(t *testing.T) {
	s, r, n := newTestSFU(t, false)
	audience := []string{"alice", "bob", "mallory", "watcher"}
	r.set("relay", "alice", &Access{Relay: true, CanSpeak: true, Audience: audience})
	r.set("relay", "bob", &Access{Relay: true, CanSpeak: true, Audience: audience})
	r.set("relay", "mallory", &Access{Relay: true, CanSpeak: false, Audience: audience})

	alice, bob, mallory := newClient(t), newClient(t), newClient(t)
	alice.join(s, "alice", Hello{Room: "relay"})
	waitFor(t, "alice's welcome", 5*time.Second, func() bool { _, ok := alice.last("welcome"); return ok })
	bob.join(s, "bob", Hello{Room: "relay"})
	mallory.join(s, "mallory", Hello{Room: "relay"})
	waitFor(t, "three in the room", 5*time.Second, func() bool {
		ri, ok := s.Room("relay")
		return ok && len(ri.Participants) == 3
	})
	if w, _ := mallory.last("welcome"); w.CanSpeak {
		t.Fatal("a muted member was told they can speak")
	}
	if n.count("watcher", "relay") == 0 {
		t.Fatal("the audience was not told about the room")
	}
	ids := map[string]string{}
	for _, c := range []struct {
		name string
		c    *client
	}{{"alice", alice}, {"bob", bob}, {"mallory", mallory}} {
		w, _ := c.c.last("welcome")
		ids[c.name] = w.ID
	}

	stop := make(chan struct{})
	defer close(stop)
	go alice.sendAudio("alice", stop)
	go mallory.sendAudio("mallory", stop)

	waitFor(t, "bob to receive alice's audio", 15*time.Second, func() bool { return len(bob.payloadsFrom(ids["alice"])) >= 20 })
	for _, p := range bob.payloadsFrom(ids["alice"]) {
		if !bytes.HasPrefix(p, []byte("E2EE:alice:")) || len(p) != len(fakeCiphertext("alice", 1)) {
			t.Fatalf("payload changed in transit: %x", p)
		}
	}
	waitFor(t, "mallory to receive alice's audio", 15*time.Second, func() bool { return len(mallory.payloadsFrom(ids["alice"])) >= 5 })
	time.Sleep(300 * time.Millisecond)
	if got := len(bob.payloadsFrom(ids["mallory"])); got != 0 {
		t.Fatalf("a member muted in the domain was heard (%d packets)", got)
	}
	if st := s.Stats(); st.Rooms != 1 || st.Participants != 3 || st.BytesIn == 0 || st.BytesOut == 0 {
		t.Fatalf("stats: %+v", st)
	}

	// Unmuted in the domain: Recheck lets mallory be heard.
	r.set("relay", "mallory", &Access{Relay: true, CanSpeak: true, Audience: audience})
	s.Recheck(context.Background())
	waitFor(t, "bob to hear mallory after unmute", 10*time.Second, func() bool { return len(bob.payloadsFrom(ids["mallory"])) >= 5 })

	// Bob loses access (kicked from the domain): Recheck disconnects him.
	r.set("relay", "bob", nil)
	s.Recheck(context.Background())
	select {
	case <-bob.joined:
	case <-time.After(5 * time.Second):
		t.Fatal("bob was not disconnected after losing access")
	}
	if m, ok := bob.last("bye"); !ok || m.Reason == "" {
		t.Fatal("bob was not told why he was disconnected")
	}

	// Alice leaves; mallory sees the room shrink.
	alice.conn.toServer <- []byte(`{"type":"leave"}`)
	select {
	case <-alice.joined:
	case <-time.After(5 * time.Second):
		t.Fatal("leave did not end alice's connection")
	}
	waitFor(t, "mallory to see one participant", 5*time.Second, func() bool {
		m, ok := mallory.last("room")
		return ok && len(m.Room.Participants) == 1
	})
	mallory.conn.close()
	waitFor(t, "the empty room to close", 5*time.Second, func() bool { return len(s.Rooms()) == 0 })
}

func TestConclaveRingAndDecline(t *testing.T) {
	s, r, n := newTestSFU(t, true)
	aud := []string{"alice", "bob"}
	r.set("conclave", "alice", &Access{CanSpeak: true, Audience: aud})
	r.set("conclave", "bob", &Access{CanSpeak: true, Audience: aud})
	alice := newClient(t)
	alice.join(s, "alice", Hello{Room: "conclave", Ring: true, Video: true})
	waitFor(t, "the call to start", 5*time.Second, func() bool { _, ok := s.Room("conclave"); return ok })
	ri, _ := s.Room("conclave")
	if !ri.Ringing || !ri.Video || ri.Relay || ri.StartedBy != "alice" {
		t.Fatalf("room: %+v", ri)
	}
	waitFor(t, "bob to be told", 2*time.Second, func() bool { return n.count("bob", "conclave") > 0 })
	if !s.Decline("conclave", "bob") {
		t.Fatal("decline failed")
	}
	waitFor(t, "alice to hear of the decline", 2*time.Second, func() bool {
		m, ok := alice.last("declined")
		return ok && m.UserID == "bob"
	})
	ri, _ = s.Room("conclave")
	if len(ri.Declined) != 1 || ri.Declined[0] != "bob" {
		t.Fatalf("declined: %v", ri.Declined)
	}
	if s.Decline("nowhere", "bob") {
		t.Fatal("declined a call that does not exist")
	}
}

func TestRoomFull(t *testing.T) {
	r := &rules{allow: map[string]Access{}}
	s, err := New(Config{MaxParticipants: 1}, r.access, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r.set("x", "a", &Access{CanSpeak: true})
	r.set("x", "b", &Access{CanSpeak: true})
	a := newClient(t)
	a.join(s, "a", Hello{Room: "x"})
	waitFor(t, "a to join", 5*time.Second, func() bool { _, ok := s.Room("x"); return ok })
	b := newClient(t)
	if err := s.Join(context.Background(), b.conn, "b", "b-dev", Hello{Room: "x"}); !errors.Is(err, ErrRoomFull) {
		t.Fatalf("joined a full room: %v", err)
	}
}

func TestSingleUDPPort(t *testing.T) {
	r := &rules{allow: map[string]Access{}}
	s, err := New(Config{UDPPort: 0, PortMin: 40100, PortMax: 40200}, r.access, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(Config{UDPPort: 48745, PublicIPs: []string{"203.0.113.7"}}, r.access, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r.set("x", "a", &Access{CanSpeak: true})
	a := newClient(t)
	a.join(s, "a", Hello{Room: "x"})
	waitFor(t, "an offer", 5*time.Second, func() bool { _, ok := a.last("offer"); return ok })
	offer, _ := a.last("offer")
	if !bytes.Contains([]byte(offer.SDP), []byte(" 48745 typ host")) {
		t.Fatalf("offer does not use the single UDP port:\n%s", offer.SDP)
	}
	if !bytes.Contains([]byte(offer.SDP), []byte("203.0.113.7 48745 typ host")) {
		t.Fatalf("offer lacks the public address:\n%s", offer.SDP)
	}
}
