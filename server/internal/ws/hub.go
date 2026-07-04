package ws

import (
	"errors"
	"sync"

	"github.com/djy/vibe-terminal/server/internal/protocol"
)

var ErrNoAgent = errors.New("agent not connected")
var ErrNoSessionRoute = errors.New("session route not found")

type Outbound struct {
	Type      string
	SessionID string
	RequestID string
	Payload   any
}

type Peer interface {
	Send(Outbound) error
}

type MemoryPeer struct {
	ID       string
	mu       sync.Mutex
	Messages []Outbound
}

func NewMemoryPeer(id string) *MemoryPeer {
	return &MemoryPeer{ID: id}
}

func (p *MemoryPeer) Send(msg Outbound) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Messages = append(p.Messages, msg)
	return nil
}

func (p *MemoryPeer) Pop() Outbound {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Messages) == 0 {
		return Outbound{}
	}
	msg := p.Messages[0]
	p.Messages = p.Messages[1:]
	return msg
}

type Hub struct {
	mu             sync.RWMutex
	agents         map[string]Peer
	sessionDevices map[string]string
	webSubscribers map[string]map[Peer]struct{}
}

func NewHub() *Hub {
	return &Hub{
		agents:         map[string]Peer{},
		sessionDevices: map[string]string{},
		webSubscribers: map[string]map[Peer]struct{}{},
	}
}

func (h *Hub) AttachAgent(deviceID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[deviceID] = peer
}

func (h *Hub) DetachAgent(deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.agents, deviceID)
}

func (h *Hub) ToDevice(deviceID string, msg Outbound) error {
	h.mu.RLock()
	agent, ok := h.agents[deviceID]
	h.mu.RUnlock()
	if !ok {
		return ErrNoAgent
	}
	return agent.Send(msg)
}

func (h *Hub) BindSession(sessionID string, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionDevices[sessionID] = deviceID
}

func (h *Hub) SubscribeWeb(sessionID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.webSubscribers[sessionID] == nil {
		h.webSubscribers[sessionID] = map[Peer]struct{}{}
	}
	h.webSubscribers[sessionID][peer] = struct{}{}
}

func (h *Hub) UnsubscribeWeb(sessionID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.webSubscribers[sessionID], peer)
}

func (h *Hub) UnsubscribePeer(peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sessionID, subscribers := range h.webSubscribers {
		delete(subscribers, peer)
		if len(subscribers) == 0 {
			delete(h.webSubscribers, sessionID)
		}
	}
}

func (h *Hub) FromWeb(msg any) error {
	sessionID := sessionIDOf(msg)
	h.mu.RLock()
	deviceID, ok := h.sessionDevices[sessionID]
	if !ok {
		h.mu.RUnlock()
		return ErrNoSessionRoute
	}
	agent, ok := h.agents[deviceID]
	h.mu.RUnlock()
	if !ok {
		return ErrNoAgent
	}
	return agent.Send(Outbound{Type: typeOf(msg), SessionID: sessionID, Payload: msg})
}

func (h *Hub) FromAgent(deviceID string, msg any) error {
	sessionID := sessionIDOf(msg)
	h.mu.RLock()
	subs := make([]Peer, 0, len(h.webSubscribers[sessionID]))
	for peer := range h.webSubscribers[sessionID] {
		subs = append(subs, peer)
	}
	h.mu.RUnlock()
	for _, peer := range subs {
		if err := peer.Send(Outbound{Type: typeOf(msg), SessionID: sessionID, Payload: msg}); err != nil {
			return err
		}
	}
	return nil
}

func sessionIDOf(msg any) string {
	switch m := msg.(type) {
	case protocol.Stdin:
		return m.SessionID
	case protocol.Resize:
		return m.SessionID
	case protocol.Stdout:
		return m.SessionID
	case protocol.SessionState:
		return m.SessionID
	case protocol.SessionStarted:
		return m.SessionID
	case protocol.SessionExit:
		return m.SessionID
	case protocol.CloseSession:
		return m.SessionID
	case protocol.StartSession:
		return m.SessionID
	default:
		return ""
	}
}

func typeOf(msg any) string {
	switch msg.(type) {
	case protocol.Stdin:
		return protocol.TypeStdin
	case protocol.Resize:
		return protocol.TypeResize
	case protocol.Stdout:
		return protocol.TypeStdout
	case protocol.SessionState:
		return protocol.TypeSessionState
	case protocol.SessionStarted:
		return protocol.TypeSessionStarted
	case protocol.SessionExit:
		return protocol.TypeSessionExit
	case protocol.CloseSession:
		return protocol.TypeCloseSession
	case protocol.StartSession:
		return protocol.TypeStartSession
	default:
		return protocol.TypeError
	}
}
