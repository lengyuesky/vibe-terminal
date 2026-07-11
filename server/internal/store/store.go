package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrLoginRestartRequired = errors.New("login restart required")
var ErrInvalidSecondFactor = errors.New("invalid second factor")

const (
	SessionStarting = "starting"
	SessionRunning  = "running"
	SessionExited   = "exited"
	SessionLost     = "lost"
	SessionClosed   = "closed"
)

type DB struct {
	SQL *sql.DB
}

type NullTime = sql.NullTime

type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UserTwoFactor 保存用户的双因素认证状态。
type UserTwoFactor struct {
	UserID           string
	ConfigurationID  string
	SecretCiphertext string
	SetupExpiresAt   sql.NullTime
	EnabledAt        sql.NullTime
	LastTOTPCounter  sql.NullInt64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LoginChallenge 保存可由任意服务实例原子消费的网页登录挑战。
type LoginChallenge struct {
	JTI             string
	UserID          string
	ConfigurationID string
	ExpiresAt       time.Time
	ConsumedAt      sql.NullTime
	CreatedAt       time.Time
}

// ConsumeLoginSecondFactorParams 描述一次必须和登录挑战共同提交的二因素消费。
type ConsumeLoginSecondFactorParams struct {
	ChallengeJTI    string
	UserID          string
	ConfigurationID string
	TOTPCounter     sql.NullInt64
	RecoveryHash    string
	Now             time.Time
}

// RecoveryCodeInput 表示待持久化的恢复码标识和哈希。
type RecoveryCodeInput struct {
	ID   string
	Hash string
}

type AgentToken struct {
	ID        string
	Name      string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    sql.NullTime
	RevokedAt sql.NullTime
	CreatedAt time.Time
}

type CreateAgentTokenParams struct {
	ID        string
	Name      string
	TokenHash string
	ExpiresAt time.Time
}

type Device struct {
	ID             string
	Name           string
	Platform       string
	AgentVersion   string
	Fingerprint    string
	CredentialHash string
	Authorized     bool
	LastSeenAt     sql.NullTime
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TerminalSession struct {
	ID               string
	DeviceID         string
	Title            string
	ShellPath        string
	WorkingDirectory string
	Status           string
	AgentPID         int
	LastOutputSeq    int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AuditEvent struct {
	ID           string
	UserID       string
	DeviceID     string
	SessionID    string
	EventType    string
	Summary      string
	MetadataJSON string
	CreatedAt    time.Time
}

type OutputChunk struct {
	ID          string
	SessionID   string
	StartSeq    int64
	EndSeq      int64
	StoragePath string
	ByteSize    int64
	CreatedAt   time.Time
}

type CommandSnippet struct {
	ID        string
	Name      string
	Command   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func Open(ctx context.Context, path string) (*DB, error) {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	dsn := path + separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := sqlDB.ExecContext(ctx, `pragma foreign_keys = on`); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{SQL: sqlDB}, nil
}

func (db *DB) Close() error {
	return db.SQL.Close()
}

func (db *DB) Migrate(ctx context.Context) error {
	statements := []string{
		`create table if not exists users (
			id text primary key,
			username text not null unique,
			password_hash text not null,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create table if not exists user_two_factor (
			user_id text primary key references users(id) on delete cascade,
			configuration_id text not null,
			secret_ciphertext text not null,
			setup_expires_at datetime,
			enabled_at datetime,
			last_totp_counter integer,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create table if not exists two_factor_recovery_codes (
			id text primary key,
			user_id text not null references users(id) on delete cascade,
			code_hash text not null,
			used_at datetime,
			created_at datetime not null,
			unique(user_id, code_hash)
		)`,
		`create table if not exists login_challenges (
			jti text primary key,
			user_id text not null references users(id) on delete cascade,
			configuration_id text not null,
			expires_at datetime not null,
			consumed_at datetime,
			created_at datetime not null
		)`,
		`create index if not exists idx_login_challenges_expires_at on login_challenges(expires_at)`,
		`create table if not exists agent_tokens (
			id text primary key,
			name text not null,
			token_hash text not null unique,
			expires_at datetime not null,
			used_at datetime,
			revoked_at datetime,
			created_at datetime not null
		)`,
		`create table if not exists devices (
			id text primary key,
			name text not null,
			platform text not null,
			agent_version text not null,
			fingerprint text not null,
			credential_hash text not null,
			authorized integer not null,
			last_seen_at datetime,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create table if not exists terminal_sessions (
			id text primary key,
			device_id text not null references devices(id),
			title text not null,
			shell_path text not null,
			working_directory text not null,
			status text not null,
			agent_pid integer not null default 0,
			last_output_seq integer not null default 0,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create table if not exists audit_events (
			id text primary key,
			user_id text,
			device_id text,
			session_id text,
			event_type text not null,
			summary text not null,
			metadata_json text not null,
			created_at datetime not null
		)`,
		`create table if not exists terminal_output_chunks (
			id text primary key,
			session_id text not null references terminal_sessions(id),
			start_seq integer not null,
			end_seq integer not null,
			storage_path text not null,
			byte_size integer not null,
			created_at datetime not null
		)`,
		`create table if not exists command_snippets (
			id text primary key,
			name text not null,
			command text not null,
			created_at datetime not null,
			updated_at datetime not null
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.SQL.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) CreateUser(ctx context.Context, user User) (User, error) {
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into users (id, username, password_hash, created_at, updated_at) values (?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	return user, err
}

func (db *DB) GetUserByUsername(ctx context.Context, username string) (User, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, username, password_hash, created_at, updated_at from users where username = ?`,
		username)
	var user User
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (db *DB) GetUserByID(ctx context.Context, id string) (User, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, username, password_hash, created_at, updated_at from users where id = ?`,
		id)
	var user User
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

// CreateLoginChallenge 持久化尚未消费的登录挑战。
func (db *DB) CreateLoginChallenge(ctx context.Context, challenge LoginChallenge) error {
	challenge.ExpiresAt = challenge.ExpiresAt.UTC()
	if challenge.CreatedAt.IsZero() {
		challenge.CreatedAt = time.Now().UTC()
	} else {
		challenge.CreatedAt = challenge.CreatedAt.UTC()
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`delete from login_challenges where jti in (
			select jti from login_challenges
			where expires_at < ? order by expires_at, jti limit 100
		)`, challenge.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`insert into login_challenges (jti, user_id, configuration_id, expires_at, consumed_at, created_at)
		 values (?, ?, ?, ?, null, ?)`,
		challenge.JTI, challenge.UserID, challenge.ConfigurationID, challenge.ExpiresAt, challenge.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeLoginSecondFactor 在同一事务内校验配置并消费挑战和二因素凭据。
func (db *DB) ConsumeLoginSecondFactor(ctx context.Context, params ConsumeLoginSecondFactorParams) error {
	hasTOTP := params.TOTPCounter.Valid
	hasRecovery := params.RecoveryHash != ""
	if hasTOTP == hasRecovery {
		return errors.New("必须且只能提供一种二因素凭据")
	}
	now := params.Now.UTC()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	challengeResult, err := tx.ExecContext(ctx,
		`update login_challenges set consumed_at = ?
		 where jti = ? and user_id = ? and configuration_id = ?
		 and consumed_at is null and expires_at >= ?`,
		now, params.ChallengeJTI, params.UserID, params.ConfigurationID, now)
	if err := requireAffected(challengeResult, err, ErrLoginRestartRequired); err != nil {
		return err
	}

	var currentConfigurationID string
	err = tx.QueryRowContext(ctx,
		`select configuration_id from user_two_factor where user_id = ? and enabled_at is not null`,
		params.UserID).Scan(&currentConfigurationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLoginRestartRequired
	}
	if err != nil {
		return err
	}
	if currentConfigurationID != params.ConfigurationID {
		return ErrLoginRestartRequired
	}

	if hasTOTP {
		result, err := tx.ExecContext(ctx,
			`update user_two_factor set last_totp_counter = ?, updated_at = ?
			 where user_id = ? and configuration_id = ? and enabled_at is not null
			 and (last_totp_counter is null or last_totp_counter < ?)`,
			params.TOTPCounter.Int64, now, params.UserID, params.ConfigurationID, params.TOTPCounter.Int64)
		if err := requireAffected(result, err, ErrInvalidSecondFactor); err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx,
			`update two_factor_recovery_codes set used_at = ?
			 where user_id = ? and code_hash = ? and used_at is null`,
			now, params.UserID, params.RecoveryHash)
		if err := requireAffected(result, err, ErrInvalidSecondFactor); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SavePendingTwoFactor 保存尚待用户确认的双因素认证配置。
func (db *DB) SavePendingTwoFactor(ctx context.Context, setting UserTwoFactor) error {
	normalizePendingTwoFactor(&setting)
	result, err := db.SQL.ExecContext(ctx,
		`insert into user_two_factor (
			user_id, configuration_id, secret_ciphertext, setup_expires_at,
			enabled_at, last_totp_counter, created_at, updated_at
		) values (?, ?, ?, ?, null, null, ?, ?)
		on conflict(user_id) do update set
			configuration_id = excluded.configuration_id,
			secret_ciphertext = excluded.secret_ciphertext,
			setup_expires_at = excluded.setup_expires_at,
			enabled_at = null,
			last_totp_counter = null,
			updated_at = excluded.updated_at
		where user_two_factor.enabled_at is null`,
		setting.UserID, setting.ConfigurationID, setting.SecretCiphertext, setting.SetupExpiresAt,
		setting.CreatedAt, setting.UpdatedAt)
	return requireAffected(result, err, ErrConflict)
}

// SavePendingTwoFactorIfUnchanged 仅在调用方观察到的待确认版本未变化时保存配置。
func (db *DB) SavePendingTwoFactorIfUnchanged(ctx context.Context, setting UserTwoFactor, previousConfigurationID string) error {
	normalizePendingTwoFactor(&setting)
	var result sql.Result
	var err error
	if previousConfigurationID == "" {
		result, err = db.SQL.ExecContext(ctx,
			`insert into user_two_factor (
				user_id, configuration_id, secret_ciphertext, setup_expires_at,
				enabled_at, last_totp_counter, created_at, updated_at
			) values (?, ?, ?, ?, null, null, ?, ?)
			on conflict(user_id) do nothing`,
			setting.UserID, setting.ConfigurationID, setting.SecretCiphertext, setting.SetupExpiresAt,
			setting.CreatedAt, setting.UpdatedAt)
	} else {
		result, err = db.SQL.ExecContext(ctx,
			`update user_two_factor set
				configuration_id = ?, secret_ciphertext = ?, setup_expires_at = ?,
				enabled_at = null, last_totp_counter = null, updated_at = ?
			where user_id = ? and configuration_id = ? and enabled_at is null`,
			setting.ConfigurationID, setting.SecretCiphertext, setting.SetupExpiresAt, setting.UpdatedAt,
			setting.UserID, previousConfigurationID)
	}
	return requireAffected(result, err, ErrConflict)
}

func normalizePendingTwoFactor(setting *UserTwoFactor) {
	now := time.Now().UTC()
	if setting.SetupExpiresAt.Valid {
		setting.SetupExpiresAt.Time = setting.SetupExpiresAt.Time.UTC()
	}
	if setting.CreatedAt.IsZero() {
		setting.CreatedAt = now
	} else {
		setting.CreatedAt = setting.CreatedAt.UTC()
	}
	if setting.UpdatedAt.IsZero() {
		setting.UpdatedAt = now
	} else {
		setting.UpdatedAt = setting.UpdatedAt.UTC()
	}
}

// GetUserTwoFactor 返回用户当前的二因素记录，无论其处于待确认还是已启用状态。
func (db *DB) GetUserTwoFactor(ctx context.Context, userID string) (UserTwoFactor, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select user_id, configuration_id, secret_ciphertext, setup_expires_at,
			enabled_at, last_totp_counter, created_at, updated_at
		from user_two_factor where user_id = ?`,
		userID)
	return scanUserTwoFactor(row)
}

func (db *DB) GetEnabledTwoFactor(ctx context.Context, userID string) (UserTwoFactor, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select user_id, configuration_id, secret_ciphertext, setup_expires_at,
			enabled_at, last_totp_counter, created_at, updated_at
		from user_two_factor where user_id = ? and enabled_at is not null`,
		userID)
	return scanUserTwoFactor(row)
}

func (db *DB) GetPendingTwoFactor(ctx context.Context, userID string, now time.Time) (UserTwoFactor, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select user_id, configuration_id, secret_ciphertext, setup_expires_at,
			enabled_at, last_totp_counter, created_at, updated_at
		from user_two_factor
		where user_id = ? and enabled_at is null and setup_expires_at > ?`,
		userID, now.UTC())
	return scanUserTwoFactor(row)
}

// EnableTwoFactor 原子启用待确认配置并保存新的恢复码。
func (db *DB) EnableTwoFactor(ctx context.Context, userID, configurationID string, counter int64, codes []RecoveryCodeInput, now time.Time) error {
	now = now.UTC()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`update user_two_factor
		 set enabled_at = ?, setup_expires_at = null, last_totp_counter = ?, updated_at = ?
		 where user_id = ? and configuration_id = ? and enabled_at is null and setup_expires_at > ?`,
		now, counter, now, userID, configurationID, now)
	if err := requireAffected(result, err, ErrConflict); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	if err := insertRecoveryCodes(ctx, tx, userID, codes, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeTOTPCounter 原子推进 TOTP 计数器，拒绝重复或倒退的计数器。
func (db *DB) ConsumeTOTPCounter(ctx context.Context, userID, configurationID string, counter int64, now time.Time) error {
	now = now.UTC()
	result, err := db.SQL.ExecContext(ctx,
		`update user_two_factor set last_totp_counter = ?, updated_at = ?
		 where user_id = ? and configuration_id = ? and enabled_at is not null
		 and (last_totp_counter is null or last_totp_counter < ?)`,
		counter, now, userID, configurationID, counter)
	return requireAffected(result, err, ErrConflict)
}

// ConsumeRecoveryCode 原子标记一枚尚未使用的恢复码。
func (db *DB) ConsumeRecoveryCode(ctx context.Context, userID, hash string, now time.Time) error {
	now = now.UTC()
	result, err := db.SQL.ExecContext(ctx,
		`update two_factor_recovery_codes set used_at = ?
		 where user_id = ? and code_hash = ? and used_at is null`,
		now, userID, hash)
	return requireAffected(result, err, ErrNotFound)
}

// CountRecoveryCodes 返回用户尚未使用的恢复码数量。
func (db *DB) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var count int
	err := db.SQL.QueryRowContext(ctx,
		`select count(*) from two_factor_recovery_codes where user_id = ? and used_at is null`,
		userID).Scan(&count)
	return count, err
}

// GetTwoFactorStatus 在一个数据库快照中返回启用状态和剩余恢复码数量。
func (db *DB) GetTwoFactorStatus(ctx context.Context, userID string) (bool, int, error) {
	var enabledCount int
	var remaining int
	err := db.SQL.QueryRowContext(ctx,
		`select count(distinct settings.user_id), count(codes.id)
		 from user_two_factor settings
		 left join two_factor_recovery_codes codes
		   on codes.user_id = settings.user_id and codes.used_at is null
		 where settings.user_id = ? and settings.enabled_at is not null`,
		userID).Scan(&enabledCount, &remaining)
	return enabledCount != 0, remaining, err
}

// ReplaceRecoveryCodesAfterTOTP 在消费新的 TOTP 计数器后原子轮换恢复码。
func (db *DB) ReplaceRecoveryCodesAfterTOTP(ctx context.Context, userID, configurationID string, counter int64, codes []RecoveryCodeInput, now time.Time) error {
	now = now.UTC()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`update user_two_factor set last_totp_counter = ?, updated_at = ?
		 where user_id = ? and configuration_id = ? and enabled_at is not null
		 and (last_totp_counter is null or last_totp_counter < ?)`,
		counter, now, userID, configurationID, counter)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var currentConfigurationID string
		var currentCounter sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`select configuration_id, last_totp_counter
			 from user_two_factor where user_id = ? and enabled_at is not null`,
			userID).Scan(&currentConfigurationID, &currentCounter)
		if errors.Is(err, sql.ErrNoRows) || err == nil && currentConfigurationID != configurationID {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if currentCounter.Valid && currentCounter.Int64 >= counter {
			return ErrInvalidSecondFactor
		}
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	if err := insertRecoveryCodes(ctx, tx, userID, codes, now); err != nil {
		return err
	}
	return tx.Commit()
}

// DisableTwoFactor 原子删除已启用配置及其全部恢复码。
func (db *DB) DisableTwoFactor(ctx context.Context, userID string) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx,
		`delete from user_two_factor where user_id = ? and enabled_at is not null`,
		userID)
	if err := requireAffected(result, err, ErrNotFound); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) CreateAgentToken(ctx context.Context, params CreateAgentTokenParams) (AgentToken, error) {
	token := AgentToken{
		ID:        params.ID,
		Name:      params.Name,
		TokenHash: params.TokenHash,
		ExpiresAt: params.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into agent_tokens (id, name, token_hash, expires_at, created_at) values (?, ?, ?, ?, ?)`,
		token.ID, token.Name, token.TokenHash, token.ExpiresAt, token.CreatedAt)
	return token, err
}

func (db *DB) ListAgentTokens(ctx context.Context) ([]AgentToken, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, name, token_hash, expires_at, used_at, revoked_at, created_at
		 from agent_tokens order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []AgentToken
	for rows.Next() {
		var token AgentToken
		if err := rows.Scan(&token.ID, &token.Name, &token.TokenHash, &token.ExpiresAt, &token.UsedAt, &token.RevokedAt, &token.CreatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (db *DB) RevokeAgentToken(ctx context.Context, id string, revokedAt time.Time) (AgentToken, error) {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return AgentToken{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`select id, name, token_hash, expires_at, used_at, revoked_at, created_at
		 from agent_tokens where id = ?`,
		id)
	var token AgentToken
	err = row.Scan(&token.ID, &token.Name, &token.TokenHash, &token.ExpiresAt, &token.UsedAt, &token.RevokedAt, &token.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentToken{}, ErrNotFound
	}
	if err != nil {
		return AgentToken{}, err
	}
	if token.RevokedAt.Valid {
		return token, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `update agent_tokens set revoked_at = ? where id = ?`, revokedAt, id)
	if err != nil {
		return AgentToken{}, err
	}
	token.RevokedAt = sql.NullTime{Time: revokedAt, Valid: true}
	if err := tx.Commit(); err != nil {
		return AgentToken{}, err
	}
	return token, nil
}

func (db *DB) DeleteRevokedAgentToken(ctx context.Context, id string) error {
	var revokedAt sql.NullTime
	err := db.SQL.QueryRowContext(ctx, `select revoked_at from agent_tokens where id = ?`, id).Scan(&revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !revokedAt.Valid {
		return ErrConflict
	}
	result, err := db.SQL.ExecContext(ctx, `delete from agent_tokens where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UseAgentTokenByHash(ctx context.Context, tokenHash string, usedAt time.Time) (AgentToken, error) {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return AgentToken{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`select id, name, token_hash, expires_at, used_at, revoked_at, created_at
		 from agent_tokens
		 where token_hash = ? and used_at is null and revoked_at is null and expires_at > ?`,
		tokenHash, usedAt)
	var token AgentToken
	err = row.Scan(&token.ID, &token.Name, &token.TokenHash, &token.ExpiresAt, &token.UsedAt, &token.RevokedAt, &token.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentToken{}, ErrNotFound
	}
	if err != nil {
		return AgentToken{}, err
	}
	_, err = tx.ExecContext(ctx, `update agent_tokens set used_at = ? where id = ?`, usedAt, token.ID)
	if err != nil {
		return AgentToken{}, err
	}
	token.UsedAt = sql.NullTime{Time: usedAt, Valid: true}
	if err := tx.Commit(); err != nil {
		return AgentToken{}, err
	}
	return token, nil
}

func (db *DB) CreateDevice(ctx context.Context, device Device) (Device, error) {
	now := time.Now().UTC()
	if device.CreatedAt.IsZero() {
		device.CreatedAt = now
	}
	if device.UpdatedAt.IsZero() {
		device.UpdatedAt = now
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into devices (id, name, platform, agent_version, fingerprint, credential_hash, authorized, last_seen_at, created_at, updated_at)
		 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		device.ID, device.Name, device.Platform, device.AgentVersion, device.Fingerprint, device.CredentialHash,
		boolInt(device.Authorized), device.LastSeenAt, device.CreatedAt, device.UpdatedAt)
	return device, err
}

func (db *DB) GetDevice(ctx context.Context, id string) (Device, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, name, platform, agent_version, fingerprint, credential_hash, authorized, last_seen_at, created_at, updated_at
		 from devices where id = ?`,
		id)
	device, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return device, err
}

func (db *DB) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, name, platform, agent_version, fingerprint, credential_hash, authorized, last_seen_at, created_at, updated_at
		 from devices order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (db *DB) UpdateDeviceName(ctx context.Context, id string, name string) error {
	result, err := db.SQL.ExecContext(ctx,
		`update devices set name = ?, updated_at = ? where id = ?`,
		name, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) TouchDevice(ctx context.Context, id string, seenAt time.Time) error {
	result, err := db.SQL.ExecContext(ctx,
		`update devices set last_seen_at = ?, updated_at = ? where id = ?`,
		seenAt, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) CreateTerminalSession(ctx context.Context, session TerminalSession) (TerminalSession, error) {
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = now
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into terminal_sessions (id, device_id, title, shell_path, working_directory, status, agent_pid, last_output_seq, created_at, updated_at)
		 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.DeviceID, session.Title, session.ShellPath, session.WorkingDirectory, session.Status,
		session.AgentPID, session.LastOutputSeq, session.CreatedAt, session.UpdatedAt)
	return session, err
}

func (db *DB) GetTerminalSession(ctx context.Context, id string) (TerminalSession, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, device_id, title, shell_path, working_directory, status, agent_pid, last_output_seq, created_at, updated_at
		 from terminal_sessions where id = ?`,
		id)
	session, err := scanTerminalSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TerminalSession{}, ErrNotFound
	}
	return session, err
}

func (db *DB) UpdateTerminalSessionStatus(ctx context.Context, id string, status string, agentPID int, lastSeq int64) error {
	result, err := db.SQL.ExecContext(ctx,
		`update terminal_sessions set status = ?, agent_pid = ?, last_output_seq = ?, updated_at = ? where id = ?`,
		status, agentPID, lastSeq, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateTerminalSessionTitle(ctx context.Context, id string, title string) error {
	result, err := db.SQL.ExecContext(ctx,
		`update terminal_sessions set title = ?, updated_at = ? where id = ?`,
		title, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) ListTerminalSessionsForDevice(ctx context.Context, deviceID string) ([]TerminalSession, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, device_id, title, shell_path, working_directory, status, agent_pid, last_output_seq, created_at, updated_at
		 from terminal_sessions where device_id = ? and status != ? order by created_at`,
		deviceID, SessionClosed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []TerminalSession
	for rows.Next() {
		session, err := scanTerminalSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (db *DB) CreateAuditEvent(ctx context.Context, event AuditEvent) (AuditEvent, error) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into audit_events (id, user_id, device_id, session_id, event_type, summary, metadata_json, created_at)
		 values (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.UserID, event.DeviceID, event.SessionID, event.EventType, event.Summary, event.MetadataJSON, event.CreatedAt)
	return event, err
}

func (db *DB) CreateOutputChunk(ctx context.Context, chunk OutputChunk) (OutputChunk, error) {
	if chunk.CreatedAt.IsZero() {
		chunk.CreatedAt = time.Now().UTC()
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into terminal_output_chunks (id, session_id, start_seq, end_seq, storage_path, byte_size, created_at)
		 values (?, ?, ?, ?, ?, ?, ?)`,
		chunk.ID, chunk.SessionID, chunk.StartSeq, chunk.EndSeq, chunk.StoragePath, chunk.ByteSize, chunk.CreatedAt)
	return chunk, err
}

func (db *DB) ListOutputChunks(ctx context.Context, sessionID string) ([]OutputChunk, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, session_id, start_seq, end_seq, storage_path, byte_size, created_at
		 from terminal_output_chunks where session_id = ? order by start_seq`,
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chunks []OutputChunk
	for rows.Next() {
		var chunk OutputChunk
		if err := rows.Scan(&chunk.ID, &chunk.SessionID, &chunk.StartSeq, &chunk.EndSeq, &chunk.StoragePath, &chunk.ByteSize, &chunk.CreatedAt); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (db *DB) CreateCommandSnippet(ctx context.Context, snippet CommandSnippet) (CommandSnippet, error) {
	now := time.Now().UTC()
	if snippet.CreatedAt.IsZero() {
		snippet.CreatedAt = now
	}
	if snippet.UpdatedAt.IsZero() {
		snippet.UpdatedAt = now
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into command_snippets (id, name, command, created_at, updated_at) values (?, ?, ?, ?, ?)`,
		snippet.ID, snippet.Name, snippet.Command, snippet.CreatedAt, snippet.UpdatedAt)
	return snippet, err
}

func (db *DB) GetCommandSnippet(ctx context.Context, id string) (CommandSnippet, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, name, command, created_at, updated_at from command_snippets where id = ?`, id)
	var snippet CommandSnippet
	err := row.Scan(&snippet.ID, &snippet.Name, &snippet.Command, &snippet.CreatedAt, &snippet.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandSnippet{}, ErrNotFound
	}
	return snippet, err
}

func (db *DB) ListCommandSnippets(ctx context.Context) ([]CommandSnippet, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, name, command, created_at, updated_at from command_snippets order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snippets []CommandSnippet
	for rows.Next() {
		var snippet CommandSnippet
		if err := rows.Scan(&snippet.ID, &snippet.Name, &snippet.Command, &snippet.CreatedAt, &snippet.UpdatedAt); err != nil {
			return nil, err
		}
		snippets = append(snippets, snippet)
	}
	return snippets, rows.Err()
}

func (db *DB) UpdateCommandSnippet(ctx context.Context, id string, name string, command string) (CommandSnippet, error) {
	result, err := db.SQL.ExecContext(ctx,
		`update command_snippets set name = ?, command = ?, updated_at = ? where id = ?`,
		name, command, time.Now().UTC(), id)
	if err != nil {
		return CommandSnippet{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandSnippet{}, err
	}
	if affected == 0 {
		return CommandSnippet{}, ErrNotFound
	}
	return db.GetCommandSnippet(ctx, id)
}

func (db *DB) DeleteCommandSnippet(ctx context.Context, id string) error {
	result, err := db.SQL.ExecContext(ctx, `delete from command_snippets where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUserTwoFactor(s scanner) (UserTwoFactor, error) {
	var setting UserTwoFactor
	err := s.Scan(&setting.UserID, &setting.ConfigurationID, &setting.SecretCiphertext, &setting.SetupExpiresAt,
		&setting.EnabledAt, &setting.LastTOTPCounter, &setting.CreatedAt, &setting.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserTwoFactor{}, ErrNotFound
	}
	return setting, err
}

func scanDevice(s scanner) (Device, error) {
	var device Device
	var authorized int
	err := s.Scan(&device.ID, &device.Name, &device.Platform, &device.AgentVersion, &device.Fingerprint,
		&device.CredentialHash, &authorized, &device.LastSeenAt, &device.CreatedAt, &device.UpdatedAt)
	device.Authorized = authorized != 0
	return device, err
}

func scanTerminalSession(s scanner) (TerminalSession, error) {
	var session TerminalSession
	err := s.Scan(&session.ID, &session.DeviceID, &session.Title, &session.ShellPath, &session.WorkingDirectory,
		&session.Status, &session.AgentPID, &session.LastOutputSeq, &session.CreatedAt, &session.UpdatedAt)
	return session, err
}

// insertRecoveryCodes 在同一事务中逐项写入恢复码。
func insertRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string, codes []RecoveryCodeInput, now time.Time) error {
	for _, code := range codes {
		if _, err := tx.ExecContext(ctx,
			`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at) values (?, ?, ?, ?)`,
			code.ID, userID, code.Hash, now); err != nil {
			return err
		}
	}
	return nil
}

// requireAffected 将条件写入未命中映射为调用方指定的错误。
func requireAffected(result sql.Result, err error, emptyError error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return emptyError
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
