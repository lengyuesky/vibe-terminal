package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenConfiguresPragmasOnEveryConnection(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "store.db")
	queryPath := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "store-with-query.db")) + "?cache=shared"
	for _, testCase := range []struct {
		name string
		path string
	}{
		{name: "memory", path: ":memory:"},
		{name: "file", path: filePath},
		{name: "file with query", path: queryPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, testCase.path)
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			defer db.Close()
			db.SQL.SetMaxOpenConns(2)

			first, err := db.SQL.Conn(ctx)
			if err != nil {
				t.Fatalf("get first connection: %v", err)
			}
			defer first.Close()
			second, err := db.SQL.Conn(ctx)
			if err != nil {
				t.Fatalf("get second connection: %v", err)
			}
			defer second.Close()

			for index, connection := range []*sql.Conn{first, second} {
				var enabled int
				if err := connection.QueryRowContext(ctx, `pragma foreign_keys`).Scan(&enabled); err != nil {
					t.Fatalf("query foreign_keys on connection %d: %v", index+1, err)
				}
				if enabled != 1 {
					t.Fatalf("foreign_keys on connection %d = %d, want 1", index+1, enabled)
				}
				var busyTimeout int
				if err := connection.QueryRowContext(ctx, `pragma busy_timeout`).Scan(&busyTimeout); err != nil {
					t.Fatalf("query busy_timeout on connection %d: %v", index+1, err)
				}
				if busyTimeout != 5000 {
					t.Fatalf("busy_timeout on connection %d = %d, want 5000", index+1, busyTimeout)
				}
			}
		})
	}
}

func TestLoginChallengeLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentStore(t, ctx)
	now := time.Date(2026, time.July, 11, 8, 0, 0, 0, time.UTC)
	if _, err := db.CreateUser(ctx, User{ID: "challenge-user", Username: "challenge-user", PasswordHash: "hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	challenge := LoginChallenge{
		JTI:             "challenge-jti",
		UserID:          "challenge-user",
		ConfigurationID: "challenge-configuration",
		ExpiresAt:       now.Add(5 * time.Minute),
		CreatedAt:       now,
	}
	if err := db.CreateLoginChallenge(ctx, challenge); err != nil {
		t.Fatalf("create login challenge: %v", err)
	}
	active, err := db.GetActiveLoginChallenge(ctx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now)
	if err != nil {
		t.Fatalf("get active login challenge: %v", err)
	}
	if active.JTI != challenge.JTI || active.UserID != challenge.UserID || active.ConfigurationID != challenge.ConfigurationID {
		t.Fatalf("active login challenge = %#v, want %#v", active, challenge)
	}
	if err := db.ConsumeLoginChallenge(ctx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now.Add(time.Minute)); err != nil {
		t.Fatalf("consume login challenge: %v", err)
	}
	if _, err := db.GetActiveLoginChallenge(ctx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consumed challenge lookup error = %v, want ErrNotFound", err)
	}
	if err := db.ConsumeLoginChallenge(ctx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("reconsume login challenge error = %v, want ErrConflict", err)
	}
}

func TestConcurrentConsumeLoginChallengeAllowsExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentStore(t, ctx)
	now := time.Date(2026, time.July, 11, 8, 0, 0, 0, time.UTC)
	if _, err := db.CreateUser(ctx, User{ID: "concurrent-challenge-user", Username: "concurrent-challenge-user", PasswordHash: "hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	challenge := LoginChallenge{
		JTI:             "concurrent-challenge-jti",
		UserID:          "concurrent-challenge-user",
		ConfigurationID: "concurrent-challenge-configuration",
		ExpiresAt:       now.Add(5 * time.Minute),
		CreatedAt:       now,
	}
	if err := db.CreateLoginChallenge(ctx, challenge); err != nil {
		t.Fatalf("create login challenge: %v", err)
	}

	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	errorsSeen := runConcurrentStoreCalls(t,
		func() error {
			return db.ConsumeLoginChallenge(writeCtx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now.Add(time.Minute))
		},
		func() error {
			return db.ConsumeLoginChallenge(writeCtx, challenge.JTI, challenge.UserID, challenge.ConfigurationID, now.Add(time.Minute))
		},
	)
	cancel()
	assertConcurrentDomainResults(t, errorsSeen, ErrConflict)
}

func TestConcurrentConsumeTOTPCounterAllowsExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentStore(t, ctx)

	for iteration := 0; iteration < 5; iteration++ {
		userID := fmt.Sprintf("concurrent-totp-user-%d", iteration)
		configurationID := fmt.Sprintf("concurrent-totp-configuration-%d", iteration)
		now := time.Date(2026, time.July, 10, 8, 0, iteration, 0, time.UTC)
		createEnabledTwoFactorFixture(t, ctx, db, userID, configurationID, 100, nil, now)

		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		errorsSeen := runConcurrentStoreCalls(t,
			func() error {
				return db.ConsumeTOTPCounter(writeCtx, userID, configurationID, 101, now.Add(time.Minute))
			},
			func() error {
				return db.ConsumeTOTPCounter(writeCtx, userID, configurationID, 101, now.Add(time.Minute))
			},
		)
		cancel()
		assertConcurrentDomainResults(t, errorsSeen, ErrConflict)

		enabled, err := db.GetEnabledTwoFactor(ctx, userID)
		if err != nil {
			t.Fatalf("get enabled two factor: %v", err)
		}
		if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 101 {
			t.Fatalf("last_totp_counter = %#v, want 101", enabled.LastTOTPCounter)
		}
	}
}

func TestConcurrentConsumeRecoveryCodeAllowsExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentStore(t, ctx)

	for iteration := 0; iteration < 5; iteration++ {
		userID := fmt.Sprintf("concurrent-recovery-user-%d", iteration)
		configurationID := fmt.Sprintf("concurrent-recovery-configuration-%d", iteration)
		hash := fmt.Sprintf("concurrent-recovery-hash-%d", iteration)
		now := time.Date(2026, time.July, 10, 8, 1, iteration, 0, time.UTC)
		createEnabledTwoFactorFixture(t, ctx, db, userID, configurationID, 100, []RecoveryCodeInput{
			{ID: fmt.Sprintf("concurrent-recovery-code-%d", iteration), Hash: hash},
		}, now)

		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		errorsSeen := runConcurrentStoreCalls(t,
			func() error { return db.ConsumeRecoveryCode(writeCtx, userID, hash, now.Add(time.Minute)) },
			func() error { return db.ConsumeRecoveryCode(writeCtx, userID, hash, now.Add(time.Minute)) },
		)
		cancel()
		assertConcurrentDomainResults(t, errorsSeen, ErrNotFound)

		count, err := db.CountRecoveryCodes(ctx, userID)
		if err != nil {
			t.Fatalf("count recovery codes: %v", err)
		}
		if count != 0 {
			t.Fatalf("unused recovery code count = %d, want 0", count)
		}
	}
}

func TestConcurrentReplaceRecoveryCodesAllowsExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentStore(t, ctx)

	for iteration := 0; iteration < 5; iteration++ {
		userID := fmt.Sprintf("concurrent-replace-user-%d", iteration)
		configurationID := fmt.Sprintf("concurrent-replace-configuration-%d", iteration)
		now := time.Date(2026, time.July, 10, 8, 2, iteration, 0, time.UTC)
		createEnabledTwoFactorFixture(t, ctx, db, userID, configurationID, 100, []RecoveryCodeInput{
			{ID: fmt.Sprintf("concurrent-old-code-%d", iteration), Hash: fmt.Sprintf("concurrent-old-hash-%d", iteration)},
		}, now)
		firstCode := RecoveryCodeInput{ID: fmt.Sprintf("concurrent-first-code-%d", iteration), Hash: fmt.Sprintf("concurrent-first-hash-%d", iteration)}
		secondCode := RecoveryCodeInput{ID: fmt.Sprintf("concurrent-second-code-%d", iteration), Hash: fmt.Sprintf("concurrent-second-hash-%d", iteration)}

		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		errorsSeen := runConcurrentStoreCalls(t,
			func() error {
				return db.ReplaceRecoveryCodesAfterTOTP(writeCtx, userID, configurationID, 101, []RecoveryCodeInput{firstCode}, now.Add(time.Minute))
			},
			func() error {
				return db.ReplaceRecoveryCodesAfterTOTP(writeCtx, userID, configurationID, 101, []RecoveryCodeInput{secondCode}, now.Add(time.Minute))
			},
		)
		cancel()
		assertConcurrentDomainResults(t, errorsSeen, ErrConflict)

		enabled, err := db.GetEnabledTwoFactor(ctx, userID)
		if err != nil {
			t.Fatalf("get enabled two factor: %v", err)
		}
		if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 101 {
			t.Fatalf("last_totp_counter = %#v, want 101", enabled.LastTOTPCounter)
		}
		rows, err := db.SQL.QueryContext(ctx,
			`select code_hash from two_factor_recovery_codes where user_id = ? and used_at is null`,
			userID)
		if err != nil {
			t.Fatalf("query recovery codes: %v", err)
		}
		var hashes []string
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				_ = rows.Close()
				t.Fatalf("scan recovery code: %v", err)
			}
			hashes = append(hashes, hash)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("iterate recovery codes: %v", err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close recovery code rows: %v", err)
		}
		if len(hashes) != 1 || hashes[0] != firstCode.Hash && hashes[0] != secondCode.Hash {
			t.Fatalf("winning recovery code hashes = %v, want exactly one transaction's code", hashes)
		}
	}
}

func openConcurrentStore(t *testing.T, ctx context.Context) *DB {
	t.Helper()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	db.SQL.SetMaxOpenConns(2)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	first, err := db.SQL.Conn(ctx)
	if err != nil {
		t.Fatalf("get first connection: %v", err)
	}
	second, err := db.SQL.Conn(ctx)
	if err != nil {
		_ = first.Close()
		t.Fatalf("get second connection: %v", err)
	}
	if err := second.Close(); err != nil {
		_ = first.Close()
		t.Fatalf("close second connection: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first connection: %v", err)
	}
	return db
}

func createEnabledTwoFactorFixture(t *testing.T, ctx context.Context, db *DB, userID, configurationID string, counter int64, codes []RecoveryCodeInput, now time.Time) {
	t.Helper()
	if _, err := db.CreateUser(ctx, User{ID: userID, Username: userID, PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           userID,
		ConfigurationID:  configurationID,
		SecretCiphertext: "ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, userID, configurationID, counter, codes, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}
}

func runConcurrentStoreCalls(t *testing.T, calls ...func() error) []error {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, len(calls))
	var ready sync.WaitGroup
	ready.Add(len(calls))
	for _, call := range calls {
		call := call
		go func() {
			ready.Done()
			<-start
			results <- call()
		}()
	}
	ready.Wait()
	close(start)

	errorsSeen := make([]error, 0, len(calls))
	for range calls {
		select {
		case err := <-results:
			errorsSeen = append(errorsSeen, err)
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent store calls timed out")
		}
	}
	return errorsSeen
}

func assertConcurrentDomainResults(t *testing.T, errorsSeen []error, loserError error) {
	t.Helper()
	var successes int
	var expectedFailures int
	for _, err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, loserError):
			expectedFailures++
		default:
			t.Fatalf("concurrent write returned unexpected error: %v", err)
		}
	}
	if successes != 1 || expectedFailures != 1 {
		t.Fatalf("concurrent results = %v, want one success and one %v", errorsSeen, loserError)
	}
}

func TestDeleteUserCascadesTwoFactorRowsOnPooledConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cascade.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SQL.SetMaxOpenConns(2)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "cascade-user", Username: "cascade", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "cascade-user",
		ConfigurationID:  "cascade-configuration",
		SecretCiphertext: "cascade-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: time.Now().UTC().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at) values (?, ?, ?, ?)`,
		"recovery-code-1", "cascade-user", "recovery-hash-1", time.Now().UTC()); err != nil {
		t.Fatalf("insert recovery code: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`insert into login_challenges (jti, user_id, configuration_id, expires_at, created_at) values (?, ?, ?, ?, ?)`,
		"cascade-challenge", "cascade-user", "cascade-configuration", time.Now().UTC().Add(time.Hour), time.Now().UTC()); err != nil {
		t.Fatalf("insert login challenge: %v", err)
	}

	first, err := db.SQL.Conn(ctx)
	if err != nil {
		t.Fatalf("get first connection: %v", err)
	}
	second, err := db.SQL.Conn(ctx)
	if err != nil {
		_ = first.Close()
		t.Fatalf("get second connection: %v", err)
	}
	if _, err := second.ExecContext(ctx, `delete from users where id = ?`, "cascade-user"); err != nil {
		_ = second.Close()
		_ = first.Close()
		t.Fatalf("delete user: %v", err)
	}
	if err := second.Close(); err != nil {
		_ = first.Close()
		t.Fatalf("close second connection: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first connection: %v", err)
	}

	for _, table := range []string{"user_two_factor", "two_factor_recovery_codes", "login_challenges"} {
		var count int
		if err := db.SQL.QueryRowContext(ctx, `select count(*) from `+table+` where user_id = ?`, "cascade-user").Scan(&count); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%s rows after user delete = %d, want 0", table, count)
		}
	}
}

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
	for _, table := range []string{"user_two_factor", "two_factor_recovery_codes", "login_challenges"} {
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

func TestPendingTwoFactorNormalizesTimesToUTC(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "timezone-user", Username: "timezone", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	chinaStandardTime := time.FixedZone("UTC+8", 8*60*60)
	expiresAt := time.Date(2026, time.July, 10, 16, 0, 0, 0, chinaStandardTime)
	createdAt := expiresAt.Add(-time.Hour)
	updatedAt := expiresAt.Add(-time.Minute)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "timezone-user",
		ConfigurationID:  "timezone-configuration",
		SecretCiphertext: "timezone-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: expiresAt, Valid: true},
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}

	if _, err := db.GetPendingTwoFactor(ctx, "timezone-user", expiresAt.UTC().Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get cross-timezone expired two factor error = %v, want ErrNotFound", err)
	}

	var storedSetupExpiresAt sql.NullTime
	var storedCreatedAt time.Time
	var storedUpdatedAt time.Time
	if err := db.SQL.QueryRowContext(ctx,
		`select setup_expires_at, created_at, updated_at from user_two_factor where user_id = ?`,
		"timezone-user").Scan(&storedSetupExpiresAt, &storedCreatedAt, &storedUpdatedAt); err != nil {
		t.Fatalf("query stored timestamps: %v", err)
	}
	if storedSetupExpiresAt.Time.Location() != time.UTC || storedCreatedAt.Location() != time.UTC || storedUpdatedAt.Location() != time.UTC {
		t.Fatalf("stored timestamps must use UTC: setup=%s created=%s updated=%s",
			storedSetupExpiresAt.Time, storedCreatedAt, storedUpdatedAt)
	}
	if !storedSetupExpiresAt.Time.Equal(expiresAt) || !storedCreatedAt.Equal(createdAt) || !storedUpdatedAt.Equal(updatedAt) {
		t.Fatalf("stored timestamps changed instant: setup=%s created=%s updated=%s",
			storedSetupExpiresAt.Time, storedCreatedAt, storedUpdatedAt)
	}
}

func TestPendingTwoFactorExpiresAtNowIsNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "boundary-user", Username: "boundary", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "boundary-user",
		ConfigurationID:  "boundary-configuration",
		SecretCiphertext: "boundary-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if _, err := db.GetPendingTwoFactor(ctx, "boundary-user", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get boundary pending two factor error = %v, want ErrNotFound", err)
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

func TestEnableTwoFactorAtomicallyEnablesPendingAndStoresRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "enable-user", Username: "enable", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "enable-user",
		ConfigurationID:  "enable-configuration",
		SecretCiphertext: "enable-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at) values (?, ?, ?, ?)`,
		"old-enable-code", "enable-user", "old-enable-hash", now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert old recovery code: %v", err)
	}

	err = db.EnableTwoFactor(ctx, "enable-user", "enable-configuration", 100, []RecoveryCodeInput{
		{ID: "enable-code-1", Hash: "enable-hash-1"},
		{ID: "enable-code-2", Hash: "enable-hash-2"},
	}, now)
	if err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	enabled, err := db.GetEnabledTwoFactor(ctx, "enable-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if enabled.SetupExpiresAt.Valid {
		t.Fatalf("setup_expires_at = %#v, want null", enabled.SetupExpiresAt)
	}
	if !enabled.EnabledAt.Valid || !enabled.EnabledAt.Time.Equal(now) || enabled.EnabledAt.Time.Location() != time.UTC {
		t.Fatalf("enabled_at = %#v, want UTC %s", enabled.EnabledAt, now.UTC())
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 100 {
		t.Fatalf("last_totp_counter = %#v, want 100", enabled.LastTOTPCounter)
	}
	if !enabled.UpdatedAt.Equal(now) || enabled.UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at = %s, want UTC %s", enabled.UpdatedAt, now.UTC())
	}

	var recoveryCount int
	if err := db.SQL.QueryRowContext(ctx,
		`select count(*) from two_factor_recovery_codes where user_id = ? and used_at is null`,
		"enable-user").Scan(&recoveryCount); err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if recoveryCount != 2 {
		t.Fatalf("recovery code count = %d, want 2", recoveryCount)
	}
	var storedUserID string
	var storedHash string
	var storedCreatedAt time.Time
	if err := db.SQL.QueryRowContext(ctx,
		`select user_id, code_hash, created_at from two_factor_recovery_codes where id = ?`,
		"enable-code-1").Scan(&storedUserID, &storedHash, &storedCreatedAt); err != nil {
		t.Fatalf("query inserted recovery code: %v", err)
	}
	if storedUserID != "enable-user" || storedHash != "enable-hash-1" {
		t.Fatalf("stored recovery code = user %q hash %q", storedUserID, storedHash)
	}
	if !storedCreatedAt.Equal(now) || storedCreatedAt.Location() != time.UTC {
		t.Fatalf("recovery code created_at = %s, want UTC %s", storedCreatedAt, now.UTC())
	}
}

func TestConsumeTOTPCounterRejectsReplay(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "totp-user", Username: "totp", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "totp-user",
		ConfigurationID:  "totp-configuration",
		SecretCiphertext: "totp-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "totp-user", "totp-configuration", 100, nil, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	consumedAt := now.Add(time.Minute).In(time.FixedZone("UTC+8", 8*60*60))
	if err := db.ConsumeTOTPCounter(ctx, "totp-user", "totp-configuration", 101, consumedAt); err != nil {
		t.Fatalf("consume TOTP counter: %v", err)
	}
	if err := db.ConsumeTOTPCounter(ctx, "totp-user", "totp-configuration", 101, consumedAt.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay TOTP counter error = %v, want ErrConflict", err)
	}

	enabled, err := db.GetEnabledTwoFactor(ctx, "totp-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 101 {
		t.Fatalf("last_totp_counter = %#v, want 101", enabled.LastTOTPCounter)
	}
	if !enabled.UpdatedAt.Equal(consumedAt) || enabled.UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at = %s, want UTC %s", enabled.UpdatedAt, consumedAt.UTC())
	}
}

func TestConsumeRecoveryCodeOnlyOnceAndCountsUnused(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "recovery-user", Username: "recovery", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "recovery-user",
		ConfigurationID:  "recovery-configuration",
		SecretCiphertext: "recovery-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "recovery-user", "recovery-configuration", 100, []RecoveryCodeInput{
		{ID: "recovery-code-1", Hash: "recovery-hash-1"},
		{ID: "recovery-code-2", Hash: "recovery-hash-2"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	usedAt := now.Add(time.Minute).In(time.FixedZone("UTC+8", 8*60*60))
	if err := db.ConsumeRecoveryCode(ctx, "recovery-user", "recovery-hash-1", usedAt); err != nil {
		t.Fatalf("consume recovery code: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "recovery-user", "recovery-hash-1", usedAt.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reuse recovery code error = %v, want ErrNotFound", err)
	}
	count, err := db.CountRecoveryCodes(ctx, "recovery-user")
	if err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if count != 1 {
		t.Fatalf("unused recovery code count = %d, want 1", count)
	}

	var storedUsedAt sql.NullTime
	if err := db.SQL.QueryRowContext(ctx,
		`select used_at from two_factor_recovery_codes where user_id = ? and code_hash = ?`,
		"recovery-user", "recovery-hash-1").Scan(&storedUsedAt); err != nil {
		t.Fatalf("query used_at: %v", err)
	}
	if !storedUsedAt.Valid || !storedUsedAt.Time.Equal(usedAt) || storedUsedAt.Time.Location() != time.UTC {
		t.Fatalf("used_at = %#v, want UTC %s", storedUsedAt, usedAt.UTC())
	}
}

func TestReplaceRecoveryCodesAfterTOTPAtomicallyRotatesCodes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "rotate-user", Username: "rotate", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "rotate-user",
		ConfigurationID:  "rotate-configuration",
		SecretCiphertext: "rotate-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "rotate-user", "rotate-configuration", 100, []RecoveryCodeInput{
		{ID: "old-code-1", Hash: "old-hash-1"},
		{ID: "old-code-2", Hash: "old-hash-2"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}
	if err := db.ConsumeTOTPCounter(ctx, "rotate-user", "rotate-configuration", 101, now.Add(time.Minute)); err != nil {
		t.Fatalf("consume counter 101: %v", err)
	}

	replacedAt := now.Add(2 * time.Minute).In(time.FixedZone("UTC+8", 8*60*60))
	if err := db.ReplaceRecoveryCodesAfterTOTP(ctx, "rotate-user", "rotate-configuration", 102, []RecoveryCodeInput{
		{ID: "new-code-1", Hash: "new-hash-1"},
	}, replacedAt); err != nil {
		t.Fatalf("replace recovery codes: %v", err)
	}

	enabled, err := db.GetEnabledTwoFactor(ctx, "rotate-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 102 {
		t.Fatalf("last_totp_counter = %#v, want 102", enabled.LastTOTPCounter)
	}
	if !enabled.UpdatedAt.Equal(replacedAt) || enabled.UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at = %s, want UTC %s", enabled.UpdatedAt, replacedAt.UTC())
	}
	if err := db.ConsumeRecoveryCode(ctx, "rotate-user", "old-hash-1", replacedAt.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consume old recovery code error = %v, want ErrNotFound", err)
	}
	count, err := db.CountRecoveryCodes(ctx, "rotate-user")
	if err != nil {
		t.Fatalf("count replacement recovery codes: %v", err)
	}
	if count != 1 {
		t.Fatalf("replacement recovery code count = %d, want 1", count)
	}
	if err := db.ConsumeRecoveryCode(ctx, "rotate-user", "new-hash-1", replacedAt.Add(time.Second)); err != nil {
		t.Fatalf("consume new recovery code: %v", err)
	}
}

func TestDisableTwoFactorAtomicallyRemovesEnabledSettingAndRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "disable-user", Username: "disable", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "disable-user",
		ConfigurationID:  "disable-configuration",
		SecretCiphertext: "disable-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "disable-user", "disable-configuration", 100, []RecoveryCodeInput{
		{ID: "disable-code-1", Hash: "disable-hash-1"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	if err := db.DisableTwoFactor(ctx, "disable-user"); err != nil {
		t.Fatalf("disable two factor: %v", err)
	}
	if _, err := db.GetEnabledTwoFactor(ctx, "disable-user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get disabled two factor error = %v, want ErrNotFound", err)
	}
	count, err := db.CountRecoveryCodes(ctx, "disable-user")
	if err != nil {
		t.Fatalf("count recovery codes after disable: %v", err)
	}
	if count != 0 {
		t.Fatalf("recovery code count after disable = %d, want 0", count)
	}
}

func TestTwoFactorCredentialLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "lifecycle-user", Username: "lifecycle", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "lifecycle-user",
		ConfigurationID:  "lifecycle-configuration",
		SecretCiphertext: "lifecycle-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "lifecycle-user", "lifecycle-configuration", 100, []RecoveryCodeInput{
		{ID: "lifecycle-code-1", Hash: "lifecycle-hash-1"},
		{ID: "lifecycle-code-2", Hash: "lifecycle-hash-2"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}
	enabled, err := db.GetEnabledTwoFactor(ctx, "lifecycle-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 100 {
		t.Fatalf("initial last_totp_counter = %#v, want 100", enabled.LastTOTPCounter)
	}

	if err := db.ConsumeTOTPCounter(ctx, "lifecycle-user", "lifecycle-configuration", 101, now.Add(time.Minute)); err != nil {
		t.Fatalf("consume counter 101: %v", err)
	}
	if err := db.ConsumeTOTPCounter(ctx, "lifecycle-user", "lifecycle-configuration", 101, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay counter 101 error = %v, want ErrConflict", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "lifecycle-user", "lifecycle-hash-1", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("consume first recovery code: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "lifecycle-user", "lifecycle-hash-1", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reuse first recovery code error = %v, want ErrNotFound", err)
	}
	count, err := db.CountRecoveryCodes(ctx, "lifecycle-user")
	if err != nil {
		t.Fatalf("count remaining recovery codes: %v", err)
	}
	if count != 1 {
		t.Fatalf("remaining recovery code count = %d, want 1", count)
	}

	if err := db.ReplaceRecoveryCodesAfterTOTP(ctx, "lifecycle-user", "lifecycle-configuration", 102, []RecoveryCodeInput{
		{ID: "lifecycle-new-code", Hash: "lifecycle-new-hash"},
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("replace recovery codes: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "lifecycle-user", "lifecycle-hash-2", now.Add(4*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consume old recovery code error = %v, want ErrNotFound", err)
	}
	count, err = db.CountRecoveryCodes(ctx, "lifecycle-user")
	if err != nil {
		t.Fatalf("count new recovery codes: %v", err)
	}
	if count != 1 {
		t.Fatalf("new recovery code count = %d, want 1", count)
	}
	if err := db.ConsumeRecoveryCode(ctx, "lifecycle-user", "lifecycle-new-hash", now.Add(4*time.Minute)); err != nil {
		t.Fatalf("consume new recovery code: %v", err)
	}

	if err := db.DisableTwoFactor(ctx, "lifecycle-user"); err != nil {
		t.Fatalf("disable two factor: %v", err)
	}
	if _, err := db.GetEnabledTwoFactor(ctx, "lifecycle-user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get disabled two factor error = %v, want ErrNotFound", err)
	}
	count, err = db.CountRecoveryCodes(ctx, "lifecycle-user")
	if err != nil {
		t.Fatalf("count recovery codes after disable: %v", err)
	}
	if count != 0 {
		t.Fatalf("recovery code count after disable = %d, want 0", count)
	}
}

func TestEnableTwoFactorRejectsInvalidPendingWithoutWritingCodes(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		configurationID string
		expiresOffset   time.Duration
	}{
		{name: "expired setup", configurationID: "valid-configuration", expiresOffset: -time.Second},
		{name: "wrong configuration", configurationID: "wrong-configuration", expiresOffset: time.Minute},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			defer db.Close()
			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if _, err := db.CreateUser(ctx, User{ID: "invalid-enable-user", Username: "invalid-enable", PasswordHash: "password-hash"}); err != nil {
				t.Fatalf("create user: %v", err)
			}
			now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
			if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
				UserID:           "invalid-enable-user",
				ConfigurationID:  "valid-configuration",
				SecretCiphertext: "invalid-enable-ciphertext",
				SetupExpiresAt:   sql.NullTime{Time: now.Add(testCase.expiresOffset), Valid: true},
			}); err != nil {
				t.Fatalf("save pending two factor: %v", err)
			}

			err = db.EnableTwoFactor(ctx, "invalid-enable-user", testCase.configurationID, 100, []RecoveryCodeInput{
				{ID: "must-not-be-written", Hash: "must-not-be-written"},
			}, now)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("enable invalid pending error = %v, want ErrConflict", err)
			}
			count, err := db.CountRecoveryCodes(ctx, "invalid-enable-user")
			if err != nil {
				t.Fatalf("count recovery codes: %v", err)
			}
			if count != 0 {
				t.Fatalf("recovery code count = %d, want 0", count)
			}
		})
	}
}

func TestEnableTwoFactorRecoveryCodeInsertFailureRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "enable-rollback-user", Username: "enable-rollback", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "enable-rollback-user",
		ConfigurationID:  "enable-rollback-configuration",
		SecretCiphertext: "enable-rollback-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at) values (?, ?, ?, ?)`,
		"enable-rollback-old-code", "enable-rollback-user", "enable-rollback-old-hash", now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert old recovery code: %v", err)
	}

	err = db.EnableTwoFactor(ctx, "enable-rollback-user", "enable-rollback-configuration", 100, []RecoveryCodeInput{
		{ID: "enable-rollback-duplicate-code", Hash: "enable-rollback-new-hash-1"},
		{ID: "enable-rollback-duplicate-code", Hash: "enable-rollback-new-hash-2"},
	}, now)
	if err == nil {
		t.Fatal("enable two factor with duplicate recovery code IDs succeeded")
	}
	pending, err := db.GetPendingTwoFactor(ctx, "enable-rollback-user", now)
	if err != nil {
		t.Fatalf("get pending two factor after rollback: %v", err)
	}
	if pending.EnabledAt.Valid || pending.LastTOTPCounter.Valid {
		t.Fatalf("pending setting retained partial enabled state: %#v", pending)
	}
	if !pending.SetupExpiresAt.Valid || !pending.SetupExpiresAt.Time.Equal(now.Add(time.Minute)) {
		t.Fatalf("setup_expires_at after rollback = %#v", pending.SetupExpiresAt)
	}

	var oldCodeCount int
	if err := db.SQL.QueryRowContext(ctx,
		`select count(*) from two_factor_recovery_codes
		 where id = ? and user_id = ? and code_hash = ? and used_at is null`,
		"enable-rollback-old-code", "enable-rollback-user", "enable-rollback-old-hash").Scan(&oldCodeCount); err != nil {
		t.Fatalf("count old recovery code: %v", err)
	}
	if oldCodeCount != 1 {
		t.Fatalf("old recovery code count after rollback = %d, want 1", oldCodeCount)
	}
	var newCodeCount int
	if err := db.SQL.QueryRowContext(ctx,
		`select count(*) from two_factor_recovery_codes where user_id = ? and code_hash in (?, ?)`,
		"enable-rollback-user", "enable-rollback-new-hash-1", "enable-rollback-new-hash-2").Scan(&newCodeCount); err != nil {
		t.Fatalf("count rolled-back new recovery codes: %v", err)
	}
	if newCodeCount != 0 {
		t.Fatalf("new recovery code count after rollback = %d, want 0", newCodeCount)
	}
}

func TestReplaceRecoveryCodesReplayPreservesOldCodes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "replay-user", Username: "replay", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "replay-user",
		ConfigurationID:  "replay-configuration",
		SecretCiphertext: "replay-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "replay-user", "replay-configuration", 100, []RecoveryCodeInput{
		{ID: "replay-old-code", Hash: "replay-old-hash"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	err = db.ReplaceRecoveryCodesAfterTOTP(ctx, "replay-user", "replay-configuration", 100, []RecoveryCodeInput{
		{ID: "replay-new-code", Hash: "replay-new-hash"},
	}, now.Add(time.Minute))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("replace with replayed counter error = %v, want ErrConflict", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "replay-user", "replay-old-hash", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("old recovery code was not preserved: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "replay-user", "replay-new-hash", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consume uncommitted new recovery code error = %v, want ErrNotFound", err)
	}
}

func TestReplaceRecoveryCodesInsertFailureRollsBackCounterAndCodes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "insert-failure-user", Username: "insert-failure", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "insert-failure-user",
		ConfigurationID:  "insert-failure-configuration",
		SecretCiphertext: "insert-failure-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "insert-failure-user", "insert-failure-configuration", 100, []RecoveryCodeInput{
		{ID: "insert-failure-old-code", Hash: "insert-failure-old-hash"},
	}, now); err != nil {
		t.Fatalf("enable two factor: %v", err)
	}

	err = db.ReplaceRecoveryCodesAfterTOTP(ctx, "insert-failure-user", "insert-failure-configuration", 101, []RecoveryCodeInput{
		{ID: "duplicate-code", Hash: "new-hash-1"},
		{ID: "duplicate-code", Hash: "new-hash-2"},
	}, now.Add(time.Minute))
	if err == nil {
		t.Fatal("replace recovery codes with duplicate IDs succeeded")
	}
	enabled, err := db.GetEnabledTwoFactor(ctx, "insert-failure-user")
	if err != nil {
		t.Fatalf("get enabled two factor: %v", err)
	}
	if !enabled.LastTOTPCounter.Valid || enabled.LastTOTPCounter.Int64 != 100 {
		t.Fatalf("last_totp_counter after rollback = %#v, want 100", enabled.LastTOTPCounter)
	}
	if err := db.ConsumeRecoveryCode(ctx, "insert-failure-user", "insert-failure-old-hash", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("old recovery code was not restored: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "insert-failure-user", "new-hash-1", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consume rolled-back new recovery code error = %v, want ErrNotFound", err)
	}
}

func TestDisableTwoFactorMissingEnabledRollsBackRecoveryDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.CreateUser(ctx, User{ID: "rollback-user", Username: "rollback", PasswordHash: "password-hash"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	if err := db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "rollback-user",
		ConfigurationID:  "rollback-configuration",
		SecretCiphertext: "rollback-ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("save pending two factor: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at) values (?, ?, ?, ?)`,
		"rollback-code", "rollback-user", "rollback-hash", now); err != nil {
		t.Fatalf("insert recovery code: %v", err)
	}

	if err := db.DisableTwoFactor(ctx, "rollback-user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disable missing enabled setting error = %v, want ErrNotFound", err)
	}
	count, err := db.CountRecoveryCodes(ctx, "rollback-user")
	if err != nil {
		t.Fatalf("count recovery codes after rollback: %v", err)
	}
	if count != 1 {
		t.Fatalf("recovery code count after rollback = %d, want 1", count)
	}
	if _, err := db.GetPendingTwoFactor(ctx, "rollback-user", now); err != nil {
		t.Fatalf("pending setting changed during rollback: %v", err)
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
