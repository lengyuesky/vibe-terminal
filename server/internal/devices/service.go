package devices

import "sync"

type Presence struct {
	mu     sync.RWMutex
	online map[string]bool
}

func NewPresence() *Presence {
	return &Presence{online: map[string]bool{}}
}

func (p *Presence) Set(deviceID string, online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online[deviceID] = online
}

func (p *Presence) Online(deviceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.online[deviceID]
}
