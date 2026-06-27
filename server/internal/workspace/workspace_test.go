package workspace

import (
	"context"
	"testing"
)

func TestEnsureUserWorkspaceCreatesMissingContainer(t *testing.T) {
	client := NewFakeClient()
	manager := Manager{
		Client:      client,
		Image:       "vibe-terminal-workspace:latest",
		DataVolume:  "vibe_workspace_data",
		ContainerID: "vibe-workspace-user-1",
	}
	if err := manager.Ensure(context.Background(), "user-1"); err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	if !client.Created["vibe-workspace-user-1"] {
		t.Fatalf("container was not created: %#v", client.Created)
	}
	if !client.Started["vibe-workspace-user-1"] {
		t.Fatalf("container was not started: %#v", client.Started)
	}
}
