// Package rtc is the node's media relay for voice and video calls: a small
// selective forwarding unit (SFU) built on pion/webrtc (pure Go, no cgo).
//
// Each participant has one WebRTC connection to the node. The node receives
// their microphone, camera and screen tracks and forwards the RTP packets to
// everyone else in the room. It never decodes media and it cannot: the
// clients encrypt every audio and video frame end to end with AES-GCM under a
// key derived from the room's MLS group (see client/src/call/). The node only
// rewrites the RTP header fields routing needs (SSRC and payload type) and
// passes payloads through unchanged.
//
// A room is a domain Voice Relay or a Conclave (Confession or group DM)
// call. Who may join, and who may speak, is decided by an AccessFunc the
// server supplies from domain and Conclave membership.
//
// Signalling is JSON over a WebSocket (see Join). The node always makes the
// offers and the client always answers, so the two never collide. Each
// connection starts with receive-only slots for the participant's own audio,
// camera and screen; the client fills them with RTCRtpSender.replaceTrack,
// so turning a camera on or off needs no renegotiation. Forwarded tracks are
// added as send-only transceivers and announced with their owner.
package rtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// Config sets how the node listens for media.
type Config struct {
	// UDPPort carries all calls' media on one UDP port (ICE UDP mux), which
	// makes firewall rules and port forwarding simple. 0 uses PortMin to
	// PortMax instead, one port per connection, or any free port if unset.
	UDPPort          int
	PortMin, PortMax uint16
	// TCPPort, if set, also accepts ICE over TCP on that port, for networks
	// that block UDP.
	TCPPort int
	// PublicIPs are announced as extra candidates next to the machine's own
	// addresses: the router's public address when the node is behind NAT and
	// the UDP port is forwarded to it.
	PublicIPs []string
	// Video allows cameras and screen sharing. Off, calls are voice only.
	Video bool
	// MaxParticipants per room (default 25).
	MaxParticipants int
	Logger          *slog.Logger
}

// Access is what a user may do in a room.
type Access struct {
	Relay    bool     // a domain Voice Relay; false: a Conclave call
	CanSpeak bool     // false: may listen only (muted in the domain)
	Audience []string // users who are told when the room changes
}

// AccessFunc decides whether userID (on deviceID) may be in roomID. It
// returns an error when they may not.
type AccessFunc func(ctx context.Context, roomID, userID, deviceID string) (Access, error)

// NotifyFunc tells users that a room changed (someone joined or left, a call
// started ringing). Clients then fetch the room list.
type NotifyFunc func(userIDs []string, roomID string)

var (
	ErrRoomFull  = errors.New("this call is full")
	ErrBadHello  = errors.New("bad join request")
	errLeft      = errors.New("left")
	errNoAnswer  = errors.New("no answer to an offer")
	answerWait   = 20 * time.Second
	gatherWait   = 3 * time.Second
	RingDuration = 60 * time.Second
)

const (
	SlotAudio  = "audio"
	SlotCamera = "camera"
	SlotScreen = "screen"
)

// SFU holds every room on the node.
type SFU struct {
	cfg     Config
	api     *webrtc.API
	access  AccessFunc
	notify  NotifyFunc
	log     *slog.Logger
	closers []io.Closer

	mu    sync.Mutex
	rooms map[string]*room

	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	rateMu   sync.Mutex
	rate     rateSample
}

type rateSample struct {
	at            time.Time
	in, out       int64
	inBps, outBps int64
}

// New sets up the media engine and opens the UDP (and TCP) ports.
func New(cfg Config, access AccessFunc, notify NotifyFunc) (*SFU, error) {
	if cfg.MaxParticipants <= 0 {
		cfg.MaxParticipants = 25
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if notify == nil {
		notify = func([]string, string) {}
	}
	s := &SFU{cfg: cfg, access: access, notify: notify, log: cfg.Logger, rooms: map[string]*room{}}

	m := &webrtc.MediaEngine{}
	// Opus only, about 32 kbit/s (the clients cap it; see client/src/call).
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1"},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	if cfg.Video {
		// VP8 only: every browser has it, and its payload header (which the
		// clients leave unencrypted) is simple.
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
				RTCPFeedback: []webrtc.RTCPFeedback{{Type: "ccm", Parameter: "fir"}, {Type: "nack", Parameter: "pli"}}},
			PayloadType: 96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	ir := &interceptor.Registry{}
	// NACK: ask senders to resend lost packets and resend to receivers; this
	// matters on lossy Wi-Fi and mesh links. RTCP reports keep senders informed.
	if err := webrtc.ConfigureNack(m, ir); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureRTCPReports(ir); err != nil {
		return nil, err
	}

	se := webrtc.SettingEngine{}
	// The node is reachable at its own addresses; clients send no STUN or
	// TURN requests and need no internet. A browser on the node's own
	// computer reaches it over loopback.
	se.SetIncludeLoopbackCandidate(true)
	// Clients' .local (mDNS) candidates are not resolved: their checks
	// reach the node anyway and are learnt as peer-reflexive candidates.
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	networks := []webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6}
	switch {
	case cfg.UDPPort > 0:
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: cfg.UDPPort})
		if err != nil {
			return nil, fmt.Errorf("call media UDP port %d: %w", cfg.UDPPort, err)
		}
		// One socket on all interfaces; addresses are read per call, so a
		// Pi that changes address keeps working.
		mux := webrtc.NewICEUDPMux(nil, conn)
		se.SetICEUDPMux(mux)
		s.closers = append(s.closers, mux)
	case cfg.PortMin > 0 && cfg.PortMax >= cfg.PortMin:
		if err := se.SetEphemeralUDPPortRange(cfg.PortMin, cfg.PortMax); err != nil {
			return nil, err
		}
	}
	if cfg.TCPPort > 0 {
		ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: cfg.TCPPort})
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("call media TCP port %d: %w", cfg.TCPPort, err)
		}
		mux := webrtc.NewICETCPMux(nil, ln, 32)
		se.SetICETCPMux(mux)
		s.closers = append(s.closers, mux)
		networks = append(networks, webrtc.NetworkTypeTCP4, webrtc.NetworkTypeTCP6)
	}
	se.SetNetworkTypes(networks)
	if len(cfg.PublicIPs) > 0 {
		if err := se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        cfg.PublicIPs,
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteAppend,
		}); err != nil {
			s.Close()
			return nil, err
		}
	}
	s.api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return s, nil
}

// VideoEnabled reports whether cameras and screen sharing are allowed.
func (s *SFU) VideoEnabled() bool { return s.cfg.Video }

// Close ends every call and closes the media ports.
func (s *SFU) Close() {
	s.mu.Lock()
	var ps []*participant
	for _, r := range s.rooms {
		for _, p := range r.parts {
			ps = append(ps, p)
		}
	}
	s.mu.Unlock()
	for _, p := range ps {
		p.kick("The node is shutting down.")
	}
	for _, c := range s.closers {
		c.Close()
	}
}

// ---- rooms ----

type room struct {
	id        string
	relay     bool
	audience  []string
	startedAt time.Time
	startedBy string
	ring      bool
	video     bool
	declined  map[string]bool
	parts     map[string]*participant
}

// PartInfo is one person in a room, as everyone in it sees them.
type PartInfo struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	Muted    bool   `json:"muted"`
	Camera   bool   `json:"camera"`
	Screen   bool   `json:"screen"`
	CanSpeak bool   `json:"can_speak"`
	Joined   int64  `json:"joined"`
}

// RoomInfo describes a live room.
type RoomInfo struct {
	ID           string     `json:"id"`
	Relay        bool       `json:"relay"`
	StartedAt    int64      `json:"started_at"`
	StartedBy    string     `json:"started_by"`
	Ringing      bool       `json:"ringing"`
	Video        bool       `json:"video"`
	Declined     []string   `json:"declined"`
	Participants []PartInfo `json:"participants"`
}

// s.mu held.
func (r *room) info() RoomInfo {
	ri := RoomInfo{ID: r.id, Relay: r.relay, StartedAt: r.startedAt.Unix(), StartedBy: r.startedBy,
		Ringing: r.ring && time.Since(r.startedAt) < RingDuration, Video: r.video, Declined: []string{}, Participants: []PartInfo{}}
	for u := range r.declined {
		ri.Declined = append(ri.Declined, u)
	}
	sort.Strings(ri.Declined)
	for _, p := range r.parts {
		ri.Participants = append(ri.Participants, p.info())
	}
	sort.Slice(ri.Participants, func(i, j int) bool {
		return ri.Participants[i].Joined < ri.Participants[j].Joined ||
			(ri.Participants[i].Joined == ri.Participants[j].Joined && ri.Participants[i].ID < ri.Participants[j].ID)
	})
	return ri
}

// Rooms returns every live room.
func (s *SFU) Rooms() []RoomInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RoomInfo, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Room returns one live room.
func (s *SFU) Room(id string) (RoomInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[id]
	if !ok {
		return RoomInfo{}, false
	}
	return r.info(), true
}

// Decline records that userID will not answer a Conclave call and tells the
// people in it. The caller checks that userID may see the room.
func (s *SFU) Decline(roomID, userID string) bool {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok || r.relay {
		s.mu.Unlock()
		return false
	}
	r.declined[userID] = true
	parts := r.list()
	audience := r.audience
	s.mu.Unlock()
	for _, p := range parts {
		p.send(map[string]any{"type": "declined", "user_id": userID})
	}
	s.notify(audience, roomID)
	return true
}

// s.mu held.
func (r *room) list() []*participant {
	out := make([]*participant, 0, len(r.parts))
	for _, p := range r.parts {
		out = append(out, p)
	}
	return out
}

// broadcast sends the room's state to everyone in it and tells the audience.
func (s *SFU) broadcast(roomID string) {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return
	}
	info := r.info()
	parts := r.list()
	audience := r.audience
	s.mu.Unlock()
	for _, p := range parts {
		p.send(map[string]any{"type": "room", "room": info})
	}
	s.notify(audience, roomID)
}

// Recheck asks AccessFunc again for everyone in every room: people who lost
// access are disconnected and people who were muted (or unmuted) in their
// domain stop (or start) being heard. Call it after membership or role
// changes; the node also calls it every 30 seconds.
func (s *SFU) Recheck(ctx context.Context) {
	s.mu.Lock()
	var ps []*participant
	for _, r := range s.rooms {
		ps = append(ps, r.list()...)
	}
	s.mu.Unlock()
	changed := map[string]bool{}
	for _, p := range ps {
		a, err := s.access(ctx, p.room.id, p.userID, p.deviceID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.kick("You no longer have access to this call.")
			continue
		}
		if p.canSpeak.Swap(a.CanSpeak) != a.CanSpeak {
			p.send(map[string]any{"type": "speak", "can_speak": a.CanSpeak})
			changed[p.room.id] = true
		}
		s.mu.Lock()
		p.room.audience = a.Audience
		s.mu.Unlock()
	}
	for id := range changed {
		s.broadcast(id)
	}
}

// RunRecheck calls Recheck every interval until ctx is done.
func (s *SFU) RunRecheck(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Recheck(ctx)
		}
	}
}

// ---- stats ----

// Stats are live call counters for the node admin.
type Stats struct {
	Rooms        int   `json:"rooms"`
	Participants int   `json:"participants"`
	BytesIn      int64 `json:"bytes_in"`
	BytesOut     int64 `json:"bytes_out"`
	InBps        int64 `json:"in_bps"`
	OutBps       int64 `json:"out_bps"`
	Video        bool  `json:"video"`
	UDPPort      int   `json:"udp_port,omitempty"`
	TCPPort      int   `json:"tcp_port,omitempty"`
}

// Stats returns room and participant counts and media traffic. The rates
// are averaged since the previous call (at least a second apart).
func (s *SFU) Stats() Stats {
	st := Stats{Video: s.cfg.Video, UDPPort: s.cfg.UDPPort, TCPPort: s.cfg.TCPPort}
	s.mu.Lock()
	st.Rooms = len(s.rooms)
	for _, r := range s.rooms {
		st.Participants += len(r.parts)
	}
	s.mu.Unlock()
	st.BytesIn, st.BytesOut = s.bytesIn.Load(), s.bytesOut.Load()
	s.rateMu.Lock()
	now := time.Now()
	if s.rate.at.IsZero() {
		s.rate = rateSample{at: now, in: st.BytesIn, out: st.BytesOut}
	} else if d := now.Sub(s.rate.at); d >= time.Second {
		s.rate.inBps = int64(float64(st.BytesIn-s.rate.in) * 8 / d.Seconds())
		s.rate.outBps = int64(float64(st.BytesOut-s.rate.out) * 8 / d.Seconds())
		s.rate.at, s.rate.in, s.rate.out = now, st.BytesIn, st.BytesOut
	}
	st.InBps, st.OutBps = s.rate.inBps, s.rate.outBps
	s.rateMu.Unlock()
	return st
}

// debugTap, when set (only in builds with the rtcdebug tag), sees every
// forwarded RTP payload. Tests use it to show the node only ever holds
// encrypted frames.
var debugTap func(roomID, slot string, pkt *rtp.Packet)

// ---- joining ----

// Conn is one participant's signalling connection.
type Conn interface {
	// Send writes one JSON message. It must be safe for concurrent use.
	Send(v any) error
	// Recv reads one JSON message.
	Recv(ctx context.Context) ([]byte, error)
}

// Hello is the join request.
type Hello struct {
	Room string `json:"room"`
	// Ring starts a Conclave call that rings the other members (ignored when
	// the call is already running, and in Voice Relays).
	Ring bool `json:"ring,omitempty"`
	// Video marks a ringing call as a video call.
	Video bool `json:"video,omitempty"`
}

// Join puts the participant in the room and runs their connection until they
// leave, the connection fails or ctx ends. It returns an access error before
// anything is set up if they may not join.
func (s *SFU) Join(ctx context.Context, conn Conn, userID, deviceID string, hello Hello) error {
	if hello.Room == "" {
		return ErrBadHello
	}
	a, err := s.access(ctx, hello.Room, userID, deviceID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errLeft)

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{
		BundlePolicy:  webrtc.BundlePolicyMaxBundle,
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
	})
	if err != nil {
		return err
	}
	p := &participant{
		sfu: s, id: newID(), userID: userID, deviceID: deviceID, conn: conn, pc: pc, cancel: cancel,
		joined: time.Now(), pubSlots: map[*webrtc.RTPTransceiver]string{}, pubs: map[string]*pubTrack{},
		subs: map[*pubTrack]*webrtc.RTPSender{},
	}
	p.canSpeak.Store(a.CanSpeak)
	slots := []string{SlotAudio}
	if s.cfg.Video {
		slots = append(slots, SlotCamera, SlotScreen)
	}
	for _, slot := range slots {
		kind := webrtc.RTPCodecTypeAudio
		if slot != SlotAudio {
			kind = webrtc.RTPCodecTypeVideo
		}
		t, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		if err != nil {
			pc.Close()
			return err
		}
		p.pubSlots[t] = slot
	}
	pc.OnTrack(func(t *webrtc.TrackRemote, recv *webrtc.RTPReceiver) { s.onTrack(p, t, recv) })
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			cancel(fmt.Errorf("connection %s", st))
		}
	})

	// Into the room.
	s.mu.Lock()
	r := s.rooms[hello.Room]
	if r == nil {
		r = &room{id: hello.Room, relay: a.Relay, startedAt: time.Now(), startedBy: userID,
			declined: map[string]bool{}, parts: map[string]*participant{}}
		if !a.Relay && hello.Ring {
			r.ring = true
			r.video = hello.Video && s.cfg.Video
		}
		s.rooms[hello.Room] = r
	}
	if len(r.parts) >= s.cfg.MaxParticipants {
		s.mu.Unlock()
		pc.Close()
		return ErrRoomFull
	}
	r.audience = a.Audience
	r.parts[p.id] = p
	p.room = r
	delete(r.declined, userID) // answered on another device after declining on this one
	var existing []*pubTrack
	for _, q := range r.parts {
		for _, pt := range q.pubs {
			existing = append(existing, pt)
		}
	}
	for _, pt := range existing {
		p.subscribeLocked(pt)
	}
	s.mu.Unlock()

	defer s.leave(p)

	p.send(map[string]any{"type": "welcome", "id": p.id, "can_speak": a.CanSpeak, "video": s.cfg.Video})
	s.broadcast(r.id)
	go p.negotiate()

	for {
		b, err := conn.Recv(ctx)
		if err != nil {
			return nil
		}
		if done := p.handle(b); done {
			return nil
		}
	}
}

// leave takes the participant out of the room and stops forwarding their media.
func (s *SFU) leave(p *participant) {
	p.closed.Store(true)
	s.mu.Lock()
	r := p.room
	delete(r.parts, p.id)
	type unsub struct {
		q      *participant
		sender *webrtc.RTPSender
	}
	var drops []unsub
	for _, pt := range p.pubs {
		for _, q := range r.parts {
			if sender, ok := q.subs[pt]; ok {
				delete(q.subs, pt)
				drops = append(drops, unsub{q, sender})
			}
		}
	}
	p.pubs = map[string]*pubTrack{}
	empty := len(r.parts) == 0
	if empty && s.rooms[r.id] == r {
		delete(s.rooms, r.id)
	}
	audience := r.audience
	s.mu.Unlock()

	p.pc.Close()
	touched := map[*participant]bool{}
	for _, d := range drops {
		_ = d.q.pc.RemoveTrack(d.sender)
		touched[d.q] = true
	}
	for q := range touched {
		go q.negotiate()
	}
	if empty {
		s.notify(audience, r.id)
	} else {
		s.broadcast(r.id)
	}
}

// onTrack starts forwarding a track the participant publishes.
func (s *SFU) onTrack(p *participant, remote *webrtc.TrackRemote, recv *webrtc.RTPReceiver) {
	var slot string
	for _, t := range p.pc.GetTransceivers() {
		if t.Receiver() == recv {
			slot = p.pubSlots[t]
		}
	}
	if slot == "" || (slot != SlotAudio && !s.cfg.Video) {
		return
	}
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, p.id+"-"+slot, p.id)
	if err != nil {
		s.log.Warn("call: could not forward a track", "err", err)
		return
	}
	pt := &pubTrack{owner: p, slot: slot, local: local, ssrc: uint32(remote.SSRC()), video: slot != SlotAudio}

	s.mu.Lock()
	if p.closed.Load() || p.room.parts[p.id] != p {
		s.mu.Unlock()
		return
	}
	if old := p.pubs[slot]; old != nil {
		// A replaced track on the same slot: keep the old forwarding.
		s.mu.Unlock()
		return
	}
	p.pubs[slot] = pt
	var subs []*participant
	for _, q := range p.room.parts {
		if q != p {
			q.subscribeLocked(pt)
			subs = append(subs, q)
		}
	}
	s.mu.Unlock()
	for _, q := range subs {
		go q.negotiate()
	}
	go s.forward(p, pt, remote)
}

// forward copies RTP packets from a published track to its subscribers.
// Payloads are passed through untouched: they are end-to-end encrypted.
func (s *SFU) forward(p *participant, pt *pubTrack, remote *webrtc.TrackRemote) {
	buf := make([]byte, 1500)
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			return
		}
		s.bytesIn.Add(int64(n))
		if !p.canSpeak.Load() {
			continue // muted in the domain: listen only
		}
		pkt, err := parseRTP(buf[:n])
		if err != nil {
			continue
		}
		if debugTap != nil {
			debugTap(p.room.id, p.id+"/"+pt.slot, pkt)
		}
		if err := pt.local.WriteRTP(pkt); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			continue
		}
		s.bytesOut.Add(int64(n) * int64(pt.subscribers.Load()))
	}
}

// relayRTCP passes keyframe requests from one subscriber to the publisher.
func (s *SFU) relayRTCP(pt *pubTrack, sender *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		n, _, err := sender.Read(buf)
		if err != nil {
			return
		}
		if !pt.video {
			continue
		}
		pkts, err := rtcp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		for _, pk := range pkts {
			switch pk.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				pt.requestKeyframe()
			}
		}
	}
}

// ---- participants ----

type participant struct {
	sfu      *SFU
	id       string
	userID   string
	deviceID string
	conn     Conn
	pc       *webrtc.PeerConnection
	cancel   context.CancelCauseFunc
	joined   time.Time
	canSpeak atomic.Bool

	// guarded by sfu.mu
	room     *room
	state    PartInfo
	pubSlots map[*webrtc.RTPTransceiver]string // fixed at creation
	pubs     map[string]*pubTrack              // by slot
	subs     map[*pubTrack]*webrtc.RTPSender

	closed atomic.Bool

	negMu          sync.Mutex
	awaitingAnswer bool
	pending        bool
	answerTimer    *time.Timer

	sendMu sync.Mutex
}

type pubTrack struct {
	owner       *participant
	slot        string
	local       *webrtc.TrackLocalStaticRTP
	ssrc        uint32
	video       bool
	subscribers atomic.Int32
	lastPLI     atomic.Int64
}

func (pt *pubTrack) requestKeyframe() {
	now := time.Now().UnixMilli()
	last := pt.lastPLI.Load()
	if now-last < 500 || !pt.lastPLI.CompareAndSwap(last, now) {
		return
	}
	_ = pt.owner.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: pt.ssrc}})
}

// s.mu held.
func (p *participant) info() PartInfo {
	i := p.state
	i.ID, i.UserID, i.DeviceID, i.CanSpeak, i.Joined = p.id, p.userID, p.deviceID, p.canSpeak.Load(), p.joined.UnixMilli()
	return i
}

func (p *participant) send(v any) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if err := p.conn.Send(v); err != nil {
		p.cancel(err)
	}
}

func (p *participant) kick(reason string) {
	p.send(map[string]any{"type": "bye", "reason": reason})
	p.cancel(errors.New(reason))
}

// subscribeLocked adds pt to p's connection. s.mu held; the caller
// renegotiates afterwards.
func (p *participant) subscribeLocked(pt *pubTrack) {
	if pt.owner == p || p.closed.Load() {
		return
	}
	if _, ok := p.subs[pt]; ok {
		return
	}
	t, err := p.pc.AddTransceiverFromTrack(pt.local, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		p.sfu.log.Warn("call: could not add a track", "err", err)
		return
	}
	p.subs[pt] = t.Sender()
	pt.subscribers.Add(1)
	go func() {
		p.sfu.relayRTCP(pt, t.Sender())
		pt.subscribers.Add(-1)
	}()
	if pt.video {
		go func() {
			time.Sleep(time.Second)
			pt.requestKeyframe()
		}()
	}
}

// TrackInfo says whose track a forwarded m-line carries.
type TrackInfo struct {
	Participant string `json:"participant"`
	Slot        string `json:"slot"`
}

// negotiate sends a new offer, or marks one as needed if the client has not
// answered the last.
func (p *participant) negotiate() {
	p.negMu.Lock()
	defer p.negMu.Unlock()
	if p.closed.Load() {
		return
	}
	if p.awaitingAnswer {
		p.pending = true
		return
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.cancel(err)
		return
	}
	gathered := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.cancel(err)
		return
	}
	// Host candidates only (plus -rtc-public-ip): gathering takes moments,
	// and sending them in the offer saves trickling them.
	select {
	case <-gathered:
	case <-time.After(gatherWait):
	}
	tracks := map[string]TrackInfo{}
	publish := map[string]string{}
	p.sfu.mu.Lock()
	senders := map[*webrtc.RTPSender]*pubTrack{}
	for pt, sender := range p.subs {
		senders[sender] = pt
	}
	for _, t := range p.pc.GetTransceivers() {
		mid := t.Mid()
		if slot, ok := p.pubSlots[t]; ok {
			publish[slot] = mid
			continue
		}
		if pt, ok := senders[t.Sender()]; ok && t.Sender() != nil {
			tracks[mid] = TrackInfo{Participant: pt.owner.id, Slot: pt.slot}
		}
	}
	p.sfu.mu.Unlock()
	p.awaitingAnswer = true
	p.answerTimer = time.AfterFunc(answerWait, func() { p.cancel(errNoAnswer) })
	p.send(map[string]any{"type": "offer", "sdp": p.pc.LocalDescription().SDP, "tracks": tracks, "publish": publish})
}

type inbound struct {
	Type      string                   `json:"type"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Muted     bool                     `json:"muted,omitempty"`
	Camera    bool                     `json:"camera,omitempty"`
	Screen    bool                     `json:"screen,omitempty"`
}

// handle processes one message from the client. It reports whether the
// participant left.
func (p *participant) handle(b []byte) bool {
	var m inbound
	if err := unmarshal(b, &m); err != nil {
		p.send(map[string]any{"type": "error", "error": "bad message"})
		return false
	}
	switch m.Type {
	case "answer":
		p.negMu.Lock()
		if !p.awaitingAnswer {
			p.negMu.Unlock()
			return false
		}
		p.answerTimer.Stop()
		err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		p.awaitingAnswer = false
		again := p.pending
		p.pending = false
		p.negMu.Unlock()
		if err != nil {
			p.sfu.log.Debug("call: bad answer", "err", err)
			p.cancel(err)
			return true
		}
		if again {
			go p.negotiate()
		}
	case "candidate":
		if m.Candidate != nil {
			_ = p.pc.AddICECandidate(*m.Candidate)
		}
	case "state":
		p.sfu.mu.Lock()
		video := p.sfu.cfg.Video
		p.state.Muted, p.state.Camera, p.state.Screen = m.Muted, m.Camera && video, m.Screen && video
		id := p.room.id
		p.sfu.mu.Unlock()
		p.sfu.broadcast(id)
	case "leave":
		return true
	}
	return false
}
