package store

import (
	"context"
	"testing"
	"time"
)

func TestMigrateCreatesCoreTables(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"users", "agent_tokens", "devices", "terminal_sessions", "audit_events", "terminal_output_chunks"} {
		var name string
		err := db.SQL.QueryRowContext(ctx, `select name from sqlite_master where type='table' and name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s was not created: %v", table, err)
		}
	}
}

func TestCreateAndUseAgentToken(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	token, err := db.CreateAgentToken(ctx, CreateAgentTokenParams{
		ID:        "tok-1",
		Name:      "thinkpad",
		TokenHash: "hash-1",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if token.UsedAt.Valid {
		t.Fatal("new token should not be used")
	}
	used, err := db.UseAgentTokenByHash(ctx, "hash-1", time.Now())
	if err != nil {
		t.Fatalf("use token: %v", err)
	}
	if !used.UsedAt.Valid {
		t.Fatal("used token should have used_at")
	}
	_, err = db.UseAgentTokenByHash(ctx, "hash-1", time.Now())
	if err == nil {
		t.Fatal("reusing token should fail")
	}
}

func TestDeviceSessionAndOutputRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateDevice(ctx, Device{
		ID:             "dev-1",
		Name:           "linux-box",
		Platform:       "linux",
		AgentVersion:   "0.1.0",
		Fingerprint:    "fp-1",
		CredentialHash: "cred-hash",
		Authorized:     true,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := db.CreateTerminalSession(ctx, TerminalSession{
		ID:               "sess-1",
		DeviceID:         "dev-1",
		Title:            "bash",
		ShellPath:        "/bin/bash",
		WorkingDirectory: "/home/dev",
		Status:           SessionStarting,
		LastOutputSeq:    0,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.UpdateTerminalSessionStatus(ctx, "sess-1", SessionRunning, 4242, 7); err != nil {
		t.Fatalf("update session: %v", err)
	}
	if _, err := db.CreateOutputChunk(ctx, OutputChunk{
		ID:          "chunk-1",
		SessionID:   "sess-1",
		StartSeq:    1,
		EndSeq:      7,
		StoragePath: "sessions/sess-1/000001.log",
		ByteSize:    128,
	}); err != nil {
		t.Fatalf("create output chunk: %v", err)
	}
	sessions, err := db.ListTerminalSessionsForDevice(ctx, "dev-1")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Status != SessionRunning || sessions[0].AgentPID != 4242 {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}
	chunks, err := db.ListOutputChunks(ctx, "sess-1")
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].EndSeq != 7 {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}
