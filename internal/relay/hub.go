package relay

import "sync"

// Hub tracks live client connections per user and fans events out to them.
// A slow client is dropped rather than allowed to block delivery; it
// reconnects and catches up by fetching from its last seq.
type Hub struct {
	mu    sync.Mutex
	conns map[string]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{conns: map[string]map[chan Event]struct{}{}}
}

// Subscribe registers a connection for userID. Call the returned func to unsubscribe.
func (h *Hub) Subscribe(userID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	if h.conns[userID] == nil {
		h.conns[userID] = map[chan Event]struct{}{}
	}
	h.conns[userID][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.conns[userID][ch]; ok {
				delete(h.conns[userID], ch)
				close(ch)
			}
			if len(h.conns[userID]) == 0 {
				delete(h.conns, userID)
			}
			h.mu.Unlock()
		})
	}
}

// OnlineUsers returns the users with at least one live connection.
func (h *Hub) OnlineUsers() map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]bool, len(h.conns))
	for u := range h.conns {
		out[u] = true
	}
	return out
}

func (h *Hub) Notify(userIDs []string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, u := range userIDs {
		for ch := range h.conns[u] {
			select {
			case ch <- ev:
			default:
				// Buffer full: drop this connection; the client will resync.
				delete(h.conns[u], ch)
				close(ch)
			}
		}
		if len(h.conns[u]) == 0 {
			delete(h.conns, u)
		}
	}
}
