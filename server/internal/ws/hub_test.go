package ws

import (
	"testing"

	"github.com/djy/vibe-terminal/server/internal/protocol"
)

func TestHubRoutesWebInputToAgent(t *testing.T) {
	hub := NewHub()
	agent := NewMemoryPeer("agent-dev-1")
	web := NewMemoryPeer("web-1")
	hub.AttachAgent("dev-1", agent)
	hub.SubscribeWeb("sess-1", web)
	hub.BindSession("sess-1", "dev-1")

	err := hub.FromWeb(protocol.Stdin{SessionID: "sess-1", Data: "ls\n"})
	if err != nil {
		t.Fatalf("route stdin: %v", err)
	}
	got := agent.Pop()
	if got.Type != protocol.TypeStdin || got.SessionID != "sess-1" {
		t.Fatalf("unexpected agent message: %#v", got)
	}
}

func TestHubBroadcastsAgentOutputToSubscribedWeb(t *testing.T) {
	hub := NewHub()
	agent := NewMemoryPeer("agent-dev-1")
	web := NewMemoryPeer("web-1")
	hub.AttachAgent("dev-1", agent)
	hub.SubscribeWeb("sess-1", web)
	hub.BindSession("sess-1", "dev-1")

	err := hub.FromAgent("dev-1", protocol.Stdout{SessionID: "sess-1", Seq: 1, Data: "ok\r\n"})
	if err != nil {
		t.Fatalf("route stdout: %v", err)
	}
	got := web.Pop()
	if got.Type != protocol.TypeStdout || got.SessionID != "sess-1" {
		t.Fatalf("unexpected web message: %#v", got)
	}
}

func TestHubToDeviceDeliversWithRequestID(t *testing.T) {
	hub := NewHub()
	agent := NewMemoryPeer("agent-dev-1")
	hub.AttachAgent("dev-1", agent)

	err := hub.ToDevice("dev-1", Outbound{Type: protocol.TypeFsList, RequestID: "req-1", Payload: protocol.FsList{Path: "/tmp"}})
	if err != nil {
		t.Fatalf("to device: %v", err)
	}
	got := agent.Pop()
	if got.Type != protocol.TypeFsList || got.RequestID != "req-1" {
		t.Fatalf("unexpected message: %#v", got)
	}
}

func TestHubToDeviceErrorsWhenAgentMissing(t *testing.T) {
	hub := NewHub()
	if err := hub.ToDevice("dev-x", Outbound{Type: protocol.TypeFsList}); err != ErrNoAgent {
		t.Fatalf("err = %v, want ErrNoAgent", err)
	}
}
