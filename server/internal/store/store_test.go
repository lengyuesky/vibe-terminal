package store

import (
	"context"
	"database/sql"
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

func TestMigrateCreatesTwoFactorTables(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"user_two_factor", "two_factor_recovery_codes"} {
		var name string
		err := db.SQL.QueryRowContext(ctx, `select name from sqlite_master where type='table' and name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s was not created: %v", table, err)
		}
	}
}

func TestPendingTwoFactorRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "user-2fa", Username: "alice", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	setting := UserTwoFactor{
		UserID:           "user-2fa",
		ConfigurationID:  "configuration-1",
		SecretCiphertext: "ciphertext-1",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(10 * time.Minute), Valid: true},
		EnabledAt:        sql.NullTime{Time: now, Valid: true},
		LastTOTPCounter:  sql.NullInt64{Int64: 42, Valid: true},
	}
	if err := db.SavePendingTwoFactor(ctx, setting); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}

	pending, err := db.GetPendingTwoFactor(ctx, "user-2fa", now)
	if err != nil {
		t.Fatalf("get pending two factor: %v", err)
	}
	if pending.ConfigurationID != "configuration-1" || pending.SecretCiphertext != "ciphertext-1" {
		t.Fatalf("pending two factor = %#v", pending)
	}
	if pending.EnabledAt.Valid || pending.LastTOTPCounter.Valid {
		t.Fatalf("pending two factor must not be enabled: %#v", pending)
	}
	if pending.CreatedAt.IsZero() || pending.UpdatedAt.IsZero() {
		t.Fatalf("pending timestamps were not set: %#v", pending)
	}
	if pending.CreatedAt.Location() != time.UTC || pending.UpdatedAt.Location() != time.UTC {
		t.Fatalf("pending timestamps must use UTC: %#v", pending)
	}
	if _, err := db.GetEnabledTwoFactor(ctx, "user-2fa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get enabled two factor error = %v, want ErrNotFound", err)
	}
}

func TestPendingTwoFactorExpiredIsNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "expired-user", Username: "expired", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "expired-user",
		ConfigurationID:  "expired-configuration",
		SecretCiphertext: "expired-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(-time.Second), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}

	if _, err := db.GetPendingTwoFactor(ctx, "expired-user", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get expired pending two factor error = %v, want ErrNotFound", err)
	}
}

func TestPendingTwoFactorReplacesExistingPending(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "replace-user", Username: "replace", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	createdAt := now.Add(-time.Hour)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "replace-user",
		ConfigurationID:  "old-configuration",
		SecretCiphertext: "old-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(5 * time.Minute), Valid: true},
		CreatedAt:        createdAt,
		UpdatedAt:        createdAt,
	}); err != nil {
		t.Fatalf("save first pending two factor: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`update user_two_factor set last_totp_counter = ? where user_id = ?`, 42, "replace-user"); err != nil {
		t.Fatalf("seed last TOTP counter: %v", err)
	}

	updatedAt := now.Add(time.Minute)
	expiresAt := now.Add(10 * time.Minute)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "replace-user",
		ConfigurationID:  "new-configuration",
		SecretCiphertext: "new-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: expiresAt, Valid: true},
		CreatedAt:        now,
		UpdatedAt:        updatedAt,
	}); err != nil {
		t.Fatalf("replace pending two factor: %v", err)
	}

	pending, err := db.GetPendingTwoFactor(ctx, "replace-user", now)
	if err != nil {
		t.Fatalf("get replaced pending two factor: %v", err)
	}
	if pending.ConfigurationID != "new-configuration" || pending.SecretCiphertext != "new-ciphertext" {
		t.Fatalf("replaced pending two factor = %#v", pending)
	}
	if !pending.SetupExpiresAt.Valid || !pending.SetupExpiresAt.Time.Equal(expiresAt) {
		t.Fatalf("setup_expires_at = %#v, want %s", pending.SetupExpiresAt, expiresAt)
	}
	if !pending.CreatedAt.Equal(createdAt) || !pending.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("timestamps after replacement = created %s updated %s", pending.CreatedAt, pending.UpdatedAt)
	}
	if pending.EnabledAt.Valid || pending.LastTOTPCounter.Valid {
		t.Fatalf("replaced pending two factor retained enabled state: %#v", pending)
	}
}

func TestPendingTwoFactorDoesNotReplaceEnabled(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "enabled-user", Username: "enabled", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	expiresAt := now.Add(5 * time.Minute)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "enabled-user",
		ConfigurationID:  "enabled-configuration",
		SecretCiphertext: "enabled-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: expiresAt, Valid: true},
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	enabledAt := now.Add(-30 * time.Second)
	if _, err := db.SQL.ExecContext(ctx,
		`update user_two_factor set enabled_at = ?, last_totp_counter = ? where user_id = ?`,
		enabledAt, 42, "enabled-user"); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	err = db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "enabled-user",
		ConfigurationID:  "replacement-configuration",
		SecretCiphertext: "replacement-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Hour), Valid: true},
		UpdatedAt:        now,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("replace enabled two factor error = %v, want ErrConflict", err)
	}

	enabled, err := db.GetEnabledTwoFactor(ctx, "enabled-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if enabled.ConfigurationID != "enabled-configuration" || enabled.SecretCiphertext != "enabled-ciphertext" {
		t.Fatalf("enabled two factor was overwritten: %#v", enabled)
	}
	if !enabled.SetupExpiresAt.Valid || !enabled.SetupExpiresAt.Time.Equal(expiresAt) {
		t.Fatalf("enabled setup_expires_at = %#v, want %s", enabled.SetupExpiresAt, expiresAt)
	}
	if !enabled.EnabledAt.Valid || !enabled.EnabledAt.Time.Equal(enabledAt) {
		t.Fatalf("enabled_at = %#v, want %s", enabled.EnabledAt, enabledAt)
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 42 {
		t.Fatalf("last_totp_counter = %#v, want 42", enabled.LastTOTPCounter)
	}
	if !enabled.UpdatedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("updated_at = %s, want unchanged", enabled.UpdatedAt)
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
