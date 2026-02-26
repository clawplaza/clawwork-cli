// Package web provides an embedded web console for the ClawWork CLI,
// offering real-time mining logs via SSE and a chat interface.
package web

import (
	"sync"
	"time"
)

const maxHistory = 200

// Event is a single event broadcast to SSE clients.
type Event struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Time    string `json:"time"`
	Data    any    `json:"data,omitempty"`
}

// EventHub broadcasts mining events to connected SSE clients.
type EventHub struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
	history []Event
}

// NewEventHub creates a new event hub.
func NewEventHub() *EventHub {
	return &EventHub{
		clients: make(map[chan Event]struct{}),
		history: make([]Event, 0, maxHistory),
	}
}

// Publish sends an event to all connected clients and stores it in history.
func (h *EventHub) Publish(e Event) {
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}

	h.mu.Lock()
	if len(h.history) >= maxHistory {
		h.history = h.history[1:]
	}
	h.history = append(h.history, e)
	h.mu.Unlock()

	h.mu.RLock()
	for ch := range h.clients {
		select {
		case ch <- e:
		default:
			// Slow client — drop event to avoid blocking the miner.
		}
	}
	h.mu.RUnlock()
}

// Subscribe returns a channel of events and an unsubscribe function.
// The caller receives a replay of recent history followed by live events.
func (h *EventHub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)

	h.mu.Lock()
	h.clients[ch] = struct{}{}
	snapshot := make([]Event, len(h.history))
	copy(snapshot, h.history)
	h.mu.Unlock()

	// Replay history in background so Subscribe doesn't block.
	go func() {
		for _, e := range snapshot {
			ch <- e
		}
	}()

	unsubscribe := func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		// Drain channel to unblock any pending writes.
		go func() {
			for range ch {
			}
		}()
		close(ch)
	}

	return ch, unsubscribe
}
