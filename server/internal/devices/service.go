package devices

import "sync"

type Presence struct {
	mu           sync.RWMutex
	online       map[string]bool
	capabilities map[string]map[string]bool
}

func NewPresence() *Presence {
	return &Presence{online: map[string]bool{}, capabilities: map[string]map[string]bool{}}
}

func (p *Presence) Set(deviceID string, online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online[deviceID] = online
	if !online {
		delete(p.capabilities, deviceID)
	}
}

func (p *Presence) Online(deviceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.online[deviceID]
}

func (p *Presence) SetCapabilities(deviceID string, capabilities []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := map[string]bool{}
	for _, capability := range capabilities {
		set[capability] = true
	}
	p.capabilities[deviceID] = set
}

func (p *Presence) HasCapability(deviceID string, capability string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities[deviceID][capability]
}
