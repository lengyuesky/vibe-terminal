package store

import (
	"context"
	"errors"
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

func TestRevokeAgentToken(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour)
	_, err = db.CreateAgentToken(ctx, CreateAgentTokenParams{
		ID:        "tok-revoke",
		Name:      "laptop",
		TokenHash: "hash-revoke",
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	revokedAt := time.Now().UTC().Truncate(time.Second)
	revoked, err := db.RevokeAgentToken(ctx, "tok-revoke", revokedAt)
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if !revoked.RevokedAt.Valid {
		t.Fatal("revoked token should have revoked_at")
	}
	if !revoked.RevokedAt.Time.Equal(revokedAt) {
		t.Fatalf("revoked_at = %s, want %s", revoked.RevokedAt.Time, revokedAt)
	}

	_, err = db.UseAgentTokenByHash(ctx, "hash-revoke", time.Now().UTC())
	if err == nil {
		t.Fatal("revoked token should not be usable")
	}

	later := revokedAt.Add(time.Hour)
	again, err := db.RevokeAgentToken(ctx, "tok-revoke", later)
	if err != nil {
		t.Fatalf("revoke token again: %v", err)
	}
	if !again.RevokedAt.Time.Equal(revokedAt) {
		t.Fatalf("second revoke changed revoked_at to %s", again.RevokedAt.Time)
	}

	_, err = db.RevokeAgentToken(ctx, "missing-token", time.Now().UTC())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token error = %v, want ErrNotFound", err)
	}
}

func TestDeleteRevokedAgentToken(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, err = db.CreateAgentToken(ctx, CreateAgentTokenParams{
		ID:        "tok-delete",
		Name:      "cleanup",
		TokenHash: "hash-delete",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := db.DeleteRevokedAgentToken(ctx, "tok-delete"); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete active token error = %v, want ErrConflict", err)
	}
	if _, err := db.RevokeAgentToken(ctx, "tok-delete", time.Now().UTC()); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if err := db.DeleteRevokedAgentToken(ctx, "tok-delete"); err != nil {
		t.Fatalf("delete revoked token: %v", err)
	}
	tokens, err := db.ListAgentTokens(ctx)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("tokens after delete = %#v, want empty", tokens)
	}
	if err := db.DeleteRevokedAgentToken(ctx, "tok-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing token error = %v, want ErrNotFound", err)
	}
}

func newSnippetTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCommandSnippetCRUD(t *testing.T) {
	ctx := context.Background()
	db := newSnippetTestDB(t)

	created, err := db.CreateCommandSnippet(ctx, CommandSnippet{ID: "snip-1", Name: "disk", Command: "df -h"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be set")
	}

	list, err := db.ListCommandSnippets(ctx)
	if err != nil || len(list) != 1 || list[0].Command != "df -h" {
		t.Fatalf("list = %#v err = %v", list, err)
	}

	updated, err := db.UpdateCommandSnippet(ctx, "snip-1", "disk usage", "df -h /")
	if err != nil || updated.Name != "disk usage" || updated.Command != "df -h /" {
		t.Fatalf("update = %#v err = %v", updated, err)
	}

	if err := db.DeleteCommandSnippet(ctx, "snip-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.GetCommandSnippet(ctx, "snip-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := db.DeleteCommandSnippet(ctx, "snip-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
	if _, err := db.UpdateCommandSnippet(ctx, "snip-1", "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
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
	if err := db.UpdateDeviceName(ctx, "dev-1", "office-laptop"); err != nil {
		t.Fatalf("update device name: %v", err)
	}
	device, err := db.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get renamed device: %v", err)
	}
	if device.Name != "office-laptop" {
		t.Fatalf("device name = %q, want office-laptop", device.Name)
	}
	if err := db.UpdateDeviceName(ctx, "missing", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing device error = %v, want ErrNotFound", err)
	}
}
