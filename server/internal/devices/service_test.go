package devices

import "testing"

func TestPresenceCapabilities(t *testing.T) {
	p := NewPresence()
	p.Set("dev-1", true)
	p.SetCapabilities("dev-1", []string{"fs"})
	if !p.HasCapability("dev-1", "fs") {
		t.Fatal("expected fs capability")
	}
	if p.HasCapability("dev-1", "gpu") {
		t.Fatal("unexpected gpu capability")
	}
	if p.HasCapability("dev-2", "fs") {
		t.Fatal("unknown device should have no capability")
	}
	p.Set("dev-1", false)
	if p.HasCapability("dev-1", "fs") {
		t.Fatal("capabilities must clear on disconnect")
	}
}
