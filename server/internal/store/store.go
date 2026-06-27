package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

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
	LastSeenAt      sql.NullTime
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

func Open(ctx context.Context, path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
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

func (db *DB) ListTerminalSessionsForDevice(ctx context.Context, deviceID string) ([]TerminalSession, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, device_id, title, shell_path, working_directory, status, agent_pid, last_output_seq, created_at, updated_at
		 from terminal_sessions where device_id = ? order by created_at`,
		deviceID)
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

type scanner interface {
	Scan(dest ...any) error
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
