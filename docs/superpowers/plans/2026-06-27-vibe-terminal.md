# Vibe Terminal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working `vibe-terminal` MVP: a Go server on a VPS, a Rust agent that actively connects from controlled machines, and a React/xterm.js web UI for multi-tab terminal sessions with recovery after browser or server restarts.

**Architecture:** The Go server is the authoritative control plane for users, devices, sessions, audit indexes, WebSocket routing, SQLite persistence, and user workspace container lifecycle. The Rust agent is the execution plane for local PTY processes and reconnectable terminal state. The React web app is a stateless interaction layer that restores state through REST and WebSocket subscriptions.

**Tech Stack:** Go 1.22, SQLite, `net/http`, `github.com/coder/websocket`, `golang.org/x/crypto/bcrypt`, Rust 2021, Tokio, `tokio-tungstenite`, `portable-pty`, React, TypeScript, Vite, Vitest, xterm.js, Docker Compose.

---

## Scope Check

This plan implements one MVP because `server`, `agent`, and `web` share a single protocol and one acceptance path: register an agent, open multiple web terminal tabs, refresh or restart the server, and reconnect to the same agent-owned PTY sessions. Mobile apps, multi-user RBAC, native Windows shells, Kubernetes, E2EE, and server-side command execution stay outside this implementation plan.

## File Structure

Create or modify these files:

- `README.md`: local development and MVP usage commands.
- `.gitignore`: build artifacts, local databases, credentials, and dependency caches.
- `Makefile`: unified local checks for server, agent, web, and Docker config.
- `docs/protocol/v1.md`: stable protocol contract used by server, agent, and web.
- `docs/deployment.md`: Docker Compose, reverse proxy, and agent service usage.
- `docker-compose.yml`: single-machine server deployment.
- `deploy/Caddyfile.example`: HTTPS reverse proxy example.
- `deploy/systemd/vibe-agent.service`: Linux service template.
- `deploy/launchd/com.vibe-terminal.agent.plist`: macOS service template.
- `deploy/scripts/vibe-agent-wsl.sh`: WSL foreground runner.
- `server/go.mod`: Go module and server dependencies.
- `server/cmd/server/main.go`: server entrypoint and CLI/env bootstrap.
- `server/internal/config/config.go`: environment-backed server config.
- `server/internal/protocol/messages.go`: shared JSON envelope and message structs.
- `server/internal/store/store.go`: SQLite connection, migrations, and repositories.
- `server/internal/auth/auth.go`: password hashing and session cookie management.
- `server/internal/devices/service.go`: device registration and online state service.
- `server/internal/terminal/service.go`: session state machine and output index service.
- `server/internal/audit/audit.go`: audit event writer.
- `server/internal/workspace/workspace.go`: workspace lifecycle interface and Docker-backed implementation boundary.
- `server/internal/httpapi/router.go`: REST API and web static file routing.
- `server/internal/ws/hub.go`: in-memory routing between web sockets and agent sockets.
- `server/internal/testutil/testdb.go`: test SQLite setup.
- `server/Dockerfile`: production server image.
- `agent/Cargo.toml`: Rust agent package and dependencies.
- `agent/src/main.rs`: CLI entrypoint.
- `agent/src/config.rs`: agent config and credential file handling.
- `agent/src/protocol.rs`: protocol envelope and payload structs.
- `agent/src/registry.rs`: persistent local session registry.
- `agent/src/buffer.rs`: output ring buffer with sequence numbers.
- `agent/src/pty_manager.rs`: PTY process lifecycle.
- `agent/src/client.rs`: WebSocket registration and control loop.
- `agent/tests/pty_smoke.rs`: local PTY integration smoke test.
- `web/package.json`: web dependencies and scripts.
- `web/index.html`: Vite entry HTML.
- `web/vite.config.ts`: Vite and Vitest config.
- `web/tsconfig.json`: TypeScript config.
- `web/src/main.tsx`: React mount.
- `web/src/api.ts`: REST client.
- `web/src/ws.ts`: WebSocket terminal client.
- `web/src/App.tsx`: page composition and state orchestration.
- `web/src/components/LoginView.tsx`: login form.
- `web/src/components/DeviceList.tsx`: device list.
- `web/src/components/TerminalTabs.tsx`: multi-tab terminal shell.
- `web/src/components/TerminalPane.tsx`: xterm.js integration.
- `web/src/styles.css`: compact operations-oriented UI styling.
- `web/src/test/App.test.tsx`: UI behavior tests.
- `web/src/test/ws.test.ts`: WebSocket client tests.

## Task 1: Monorepo Skeleton And Protocol Contract

**Files:**
- Create: `.gitignore`
- Create: `README.md`
- Create: `Makefile`
- Create: `docs/protocol/v1.md`
- Create: `server/go.mod`
- Create: `server/internal/protocol/messages_test.go`
- Create: `server/internal/protocol/messages.go`

- [ ] **Step 1: Write the failing protocol test and server module**

Apply this patch:

```diff
*** Begin Patch
*** Add File: .gitignore
+.DS_Store
+node_modules/
+dist/
+coverage/
+target/
+*.db
+*.db-shm
+*.db-wal
+.env
+.env.*
+!.env.example
+server/server
+agent/vibe-agent
+tmp/
+data/
+workspace-data/
*** Add File: README.md
+# vibe-terminal
+
+`vibe-terminal` lets a browser reconnect to terminal sessions running on controlled machines. The controlled machine runs `vibe-agent`, which actively connects to the Go server. The server exposes REST and WebSocket APIs for the React/xterm.js web UI.
+
+## Local checks
+
+```bash
+make test
+```
+
+## MVP boundaries
+
+- Commands run on the controlled machine through the Rust agent.
+- The Go server owns authentication, device/session metadata, routing, audit indexes, and workspace container lifecycle.
+- The web app does not persist critical state; it restores state from the server.
*** Add File: Makefile
+.PHONY: test test-server test-agent test-web docker-config
+
+test: test-server test-agent test-web docker-config
+
+test-server:
+	cd server && go test ./...
+
+test-agent:
+	cd agent && cargo test
+
+test-web:
+	cd web && npm test -- --run && npm run build
+
+docker-config:
+	docker compose config >/dev/null
*** Add File: server/go.mod
+module github.com/djy/vibe-terminal/server
+
+go 1.22
*** Add File: server/internal/protocol/messages_test.go
+package protocol
+
+import (
+	"encoding/json"
+	"testing"
+)
+
+func TestDecodeEnvelopeRejectsMissingType(t *testing.T) {
+	_, err := DecodeEnvelope([]byte(`{"payload":{}}`))
+	if err == nil {
+		t.Fatal("expected missing type error")
+	}
+}
+
+func TestEncodeAndDecodeStartSession(t *testing.T) {
+	payload := StartSession{
+		SessionID:        "sess-1",
+		ShellPath:        "/bin/bash",
+		WorkingDirectory: "/home/dev",
+		Cols:             120,
+		Rows:             32,
+	}
+	data, err := EncodeEnvelope(TypeStartSession, payload)
+	if err != nil {
+		t.Fatalf("encode envelope: %v", err)
+	}
+	env, err := DecodeEnvelope(data)
+	if err != nil {
+		t.Fatalf("decode envelope: %v", err)
+	}
+	if env.Type != TypeStartSession {
+		t.Fatalf("type = %q, want %q", env.Type, TypeStartSession)
+	}
+	var got StartSession
+	if err := json.Unmarshal(env.Payload, &got); err != nil {
+		t.Fatalf("decode payload: %v", err)
+	}
+	if got.SessionID != "sess-1" || got.Cols != 120 || got.Rows != 32 {
+		t.Fatalf("unexpected payload: %#v", got)
+	}
+}
*** End Patch
```

- [ ] **Step 2: Run the protocol test and verify it fails**

Run:

```bash
cd server && go test ./internal/protocol -run 'TestDecodeEnvelopeRejectsMissingType|TestEncodeAndDecodeStartSession' -v
```

Expected: FAIL with compile errors containing `undefined: DecodeEnvelope`, `undefined: EncodeEnvelope`, and `undefined: StartSession`.

- [ ] **Step 3: Implement the protocol contract**

Apply this patch:

```diff
*** Begin Patch
*** Add File: server/internal/protocol/messages.go
+package protocol
+
+import (
+	"encoding/json"
+	"errors"
+)
+
+const ProtocolVersion = "v1"
+
+const (
+	TypeAgentHello       = "agent_hello"
+	TypeHeartbeat        = "heartbeat"
+	TypeSyncSessions     = "sync_sessions"
+	TypeStartSession     = "start_session"
+	TypeSessionStarted   = "session_started"
+	TypeStdin            = "stdin"
+	TypeResize           = "resize"
+	TypeStdout           = "stdout"
+	TypeSessionExit      = "session_exit"
+	TypeCloseSession     = "close_session"
+	TypeSubscribeSession = "subscribe_session"
+	TypeSessionState     = "session_state"
+	TypeError            = "error"
+)
+
+type Envelope struct {
+	Type      string          `json:"type"`
+	RequestID string          `json:"request_id,omitempty"`
+	SessionID string          `json:"session_id,omitempty"`
+	Payload   json.RawMessage `json:"payload,omitempty"`
+}
+
+func DecodeEnvelope(data []byte) (Envelope, error) {
+	var env Envelope
+	if err := json.Unmarshal(data, &env); err != nil {
+		return Envelope{}, err
+	}
+	if env.Type == "" {
+		return Envelope{}, errors.New("protocol envelope missing type")
+	}
+	return env, nil
+}
+
+func EncodeEnvelope(messageType string, payload any) ([]byte, error) {
+	raw, err := json.Marshal(payload)
+	if err != nil {
+		return nil, err
+	}
+	env := Envelope{Type: messageType, Payload: raw}
+	if sessionPayload, ok := payload.(interface{ SessionIdentifier() string }); ok {
+		env.SessionID = sessionPayload.SessionIdentifier()
+	}
+	return json.Marshal(env)
+}
+
+type SessionSummary struct {
+	SessionID        string `json:"session_id"`
+	Title            string `json:"title"`
+	ShellPath        string `json:"shell_path"`
+	WorkingDirectory string `json:"working_directory"`
+	Status           string `json:"status"`
+	AgentPID         int    `json:"agent_pid"`
+	LastOutputSeq    int64  `json:"last_output_seq"`
+}
+
+type AgentHello struct {
+	DeviceID        string           `json:"device_id"`
+	Credential      string           `json:"credential"`
+	Platform        string           `json:"platform"`
+	AgentVersion    string           `json:"agent_version"`
+	ProtocolVersion string           `json:"protocol_version"`
+	Sessions        []SessionSummary `json:"sessions"`
+}
+
+type Heartbeat struct {
+	DeviceID string `json:"device_id"`
+}
+
+type SyncSessions struct {
+	DeviceID string           `json:"device_id"`
+	Sessions []SessionSummary `json:"sessions"`
+}
+
+type StartSession struct {
+	SessionID        string `json:"session_id"`
+	ShellPath        string `json:"shell_path"`
+	WorkingDirectory string `json:"working_directory"`
+	Cols             int    `json:"cols"`
+	Rows             int    `json:"rows"`
+}
+
+func (m StartSession) SessionIdentifier() string { return m.SessionID }
+
+type SessionStarted struct {
+	SessionID     string `json:"session_id"`
+	AgentPID      int    `json:"agent_pid"`
+	Title         string `json:"title"`
+	LastOutputSeq int64  `json:"last_output_seq"`
+}
+
+func (m SessionStarted) SessionIdentifier() string { return m.SessionID }
+
+type Stdin struct {
+	SessionID string `json:"session_id"`
+	Data      string `json:"data"`
+}
+
+func (m Stdin) SessionIdentifier() string { return m.SessionID }
+
+type Resize struct {
+	SessionID string `json:"session_id"`
+	Cols      int    `json:"cols"`
+	Rows      int    `json:"rows"`
+}
+
+func (m Resize) SessionIdentifier() string { return m.SessionID }
+
+type Stdout struct {
+	SessionID string `json:"session_id"`
+	Seq       int64  `json:"seq"`
+	Data      string `json:"data"`
+}
+
+func (m Stdout) SessionIdentifier() string { return m.SessionID }
+
+type SessionExit struct {
+	SessionID string `json:"session_id"`
+	ExitCode  int    `json:"exit_code"`
+	Message   string `json:"message"`
+}
+
+func (m SessionExit) SessionIdentifier() string { return m.SessionID }
+
+type CloseSession struct {
+	SessionID string `json:"session_id"`
+}
+
+func (m CloseSession) SessionIdentifier() string { return m.SessionID }
+
+type SubscribeSession struct {
+	SessionID string `json:"session_id"`
+}
+
+func (m SubscribeSession) SessionIdentifier() string { return m.SessionID }
+
+type SessionState struct {
+	SessionID string `json:"session_id"`
+	Status    string `json:"status"`
+	Message   string `json:"message,omitempty"`
+}
+
+func (m SessionState) SessionIdentifier() string { return m.SessionID }
+
+type ErrorMessage struct {
+	Code    string `json:"code"`
+	Message string `json:"message"`
+}
*** Add File: docs/protocol/v1.md
+# vibe-terminal Protocol v1
+
+All WebSocket messages use this JSON envelope:
+
+```json
+{
+  "type": "start_session",
+  "session_id": "sess-1",
+  "payload": {}
+}
+```
+
+The server accepts two WebSocket entry points:
+
+- `/ws/agent`: authenticated by device credentials.
+- `/ws/web`: authenticated by the administrator session cookie.
+
+Required message types:
+
+- `agent_hello`
+- `heartbeat`
+- `sync_sessions`
+- `start_session`
+- `session_started`
+- `stdin`
+- `resize`
+- `stdout`
+- `session_exit`
+- `close_session`
+- `subscribe_session`
+- `session_state`
+- `error`
*** End Patch
```

- [ ] **Step 4: Run the protocol test and verify it passes**

Run:

```bash
cd server && go test ./internal/protocol -v
```

Expected: PASS for both protocol tests.

- [ ] **Step 5: Commit**

```bash
git add .gitignore README.md Makefile docs/protocol/v1.md server/go.mod server/internal/protocol/messages.go server/internal/protocol/messages_test.go
git commit -m "feat: add protocol contract"
```

## Task 2: Server SQLite Store And Migrations

**Files:**
- Modify: `server/go.mod`
- Create: `server/internal/store/store_test.go`
- Create: `server/internal/store/store.go`
- Create: `server/internal/testutil/testdb.go`

- [ ] **Step 1: Write failing store tests**

Apply this patch:

```diff
*** Begin Patch
*** Update File: server/go.mod
@@
 go 1.22
+
+require modernc.org/sqlite v1.33.1
*** Add File: server/internal/store/store_test.go
+package store
+
+import (
+	"context"
+	"testing"
+	"time"
+)
+
+func TestMigrateCreatesCoreTables(t *testing.T) {
+	ctx := context.Background()
+	db, err := Open(ctx, ":memory:")
+	if err != nil {
+		t.Fatalf("open db: %v", err)
+	}
+	defer db.Close()
+	if err := db.Migrate(ctx); err != nil {
+		t.Fatalf("migrate: %v", err)
+	}
+	for _, table := range []string{"users", "agent_tokens", "devices", "terminal_sessions", "audit_events", "terminal_output_chunks"} {
+		var name string
+		err := db.SQL.QueryRowContext(ctx, `select name from sqlite_master where type='table' and name=?`, table).Scan(&name)
+		if err != nil {
+			t.Fatalf("table %s was not created: %v", table, err)
+		}
+	}
+}
+
+func TestCreateAndUseAgentToken(t *testing.T) {
+	ctx := context.Background()
+	db, err := Open(ctx, ":memory:")
+	if err != nil {
+		t.Fatalf("open db: %v", err)
+	}
+	defer db.Close()
+	if err := db.Migrate(ctx); err != nil {
+		t.Fatalf("migrate: %v", err)
+	}
+	token, err := db.CreateAgentToken(ctx, CreateAgentTokenParams{
+		ID:        "tok-1",
+		Name:      "thinkpad",
+		TokenHash: "hash-1",
+		ExpiresAt: time.Now().Add(time.Hour),
+	})
+	if err != nil {
+		t.Fatalf("create token: %v", err)
+	}
+	if token.UsedAt.Valid {
+		t.Fatal("new token should not be used")
+	}
+	used, err := db.UseAgentTokenByHash(ctx, "hash-1", time.Now())
+	if err != nil {
+		t.Fatalf("use token: %v", err)
+	}
+	if !used.UsedAt.Valid {
+		t.Fatal("used token should have used_at")
+	}
+	_, err = db.UseAgentTokenByHash(ctx, "hash-1", time.Now())
+	if err == nil {
+		t.Fatal("reusing token should fail")
+	}
+}
+
+func TestDeviceSessionAndOutputRoundTrip(t *testing.T) {
+	ctx := context.Background()
+	db, err := Open(ctx, ":memory:")
+	if err != nil {
+		t.Fatalf("open db: %v", err)
+	}
+	defer db.Close()
+	if err := db.Migrate(ctx); err != nil {
+		t.Fatalf("migrate: %v", err)
+	}
+	if _, err := db.CreateDevice(ctx, Device{
+		ID:             "dev-1",
+		Name:           "linux-box",
+		Platform:       "linux",
+		AgentVersion:   "0.1.0",
+		Fingerprint:    "fp-1",
+		CredentialHash: "cred-hash",
+		Authorized:     true,
+	}); err != nil {
+		t.Fatalf("create device: %v", err)
+	}
+	if _, err := db.CreateTerminalSession(ctx, TerminalSession{
+		ID:               "sess-1",
+		DeviceID:         "dev-1",
+		Title:            "bash",
+		ShellPath:        "/bin/bash",
+		WorkingDirectory: "/home/dev",
+		Status:           SessionStarting,
+		LastOutputSeq:    0,
+	}); err != nil {
+		t.Fatalf("create session: %v", err)
+	}
+	if err := db.UpdateTerminalSessionStatus(ctx, "sess-1", SessionRunning, 4242, 7); err != nil {
+		t.Fatalf("update session: %v", err)
+	}
+	if _, err := db.CreateOutputChunk(ctx, OutputChunk{
+		ID:          "chunk-1",
+		SessionID:   "sess-1",
+		StartSeq:    1,
+		EndSeq:      7,
+		StoragePath: "sessions/sess-1/000001.log",
+		ByteSize:    128,
+	}); err != nil {
+		t.Fatalf("create output chunk: %v", err)
+	}
+	sessions, err := db.ListTerminalSessionsForDevice(ctx, "dev-1")
+	if err != nil {
+		t.Fatalf("list sessions: %v", err)
+	}
+	if len(sessions) != 1 || sessions[0].Status != SessionRunning || sessions[0].AgentPID != 4242 {
+		t.Fatalf("unexpected sessions: %#v", sessions)
+	}
+	chunks, err := db.ListOutputChunks(ctx, "sess-1")
+	if err != nil {
+		t.Fatalf("list chunks: %v", err)
+	}
+	if len(chunks) != 1 || chunks[0].EndSeq != 7 {
+		t.Fatalf("unexpected chunks: %#v", chunks)
+	}
+}
*** Add File: server/internal/testutil/testdb.go
+package testutil
+
+import (
+	"context"
+	"testing"
+
+	"github.com/djy/vibe-terminal/server/internal/store"
+)
+
+func NewStore(t *testing.T) *store.DB {
+	t.Helper()
+	db, err := store.Open(context.Background(), ":memory:")
+	if err != nil {
+		t.Fatalf("open test db: %v", err)
+	}
+	if err := db.Migrate(context.Background()); err != nil {
+		t.Fatalf("migrate test db: %v", err)
+	}
+	t.Cleanup(func() { _ = db.Close() })
+	return db
+}
*** End Patch
```

- [ ] **Step 2: Run store tests and verify they fail**

Run:

```bash
cd server && go test ./internal/store -v
```

Expected: FAIL with compile errors containing `undefined: Open`, `undefined: CreateAgentTokenParams`, and `undefined: Device`.

- [ ] **Step 3: Implement migrations and repository methods**

Create `server/internal/store/store.go` with this structure and exact exported API:

```go
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
```

Then add methods with these exact names and behavior:

- `CreateUser(ctx, User) (User, error)`: inserts a user with `created_at` and `updated_at` set to `time.Now().UTC()` when zero.
- `GetUserByUsername(ctx, username string) (User, error)`: returns `ErrNotFound` when no row exists.
- `CreateAgentToken(ctx, CreateAgentTokenParams) (AgentToken, error)`: inserts an unused token.
- `UseAgentTokenByHash(ctx, tokenHash string, usedAt time.Time) (AgentToken, error)`: succeeds only when token exists, not used, not revoked, and `expires_at > usedAt`.
- `CreateDevice(ctx, Device) (Device, error)`: inserts an authorized device.
- `GetDevice(ctx, id string) (Device, error)`: returns `ErrNotFound` when missing.
- `ListDevices(ctx) ([]Device, error)`: orders by `created_at`.
- `TouchDevice(ctx, id string, seenAt time.Time) error`: updates `last_seen_at`.
- `CreateTerminalSession(ctx, TerminalSession) (TerminalSession, error)`: inserts a session.
- `UpdateTerminalSessionStatus(ctx, id string, status string, agentPID int, lastSeq int64) error`: updates status, pid, sequence, and `updated_at`.
- `ListTerminalSessionsForDevice(ctx, deviceID string) ([]TerminalSession, error)`: orders by `created_at`.
- `CreateAuditEvent(ctx, AuditEvent) (AuditEvent, error)`: inserts audit event.
- `CreateOutputChunk(ctx, OutputChunk) (OutputChunk, error)`: inserts output chunk.
- `ListOutputChunks(ctx, sessionID string) ([]OutputChunk, error)`: orders by `start_seq`.

Use `sql.ErrNoRows` mapping to `ErrNotFound`. Use `time.Now().UTC()` for zero timestamps. Keep SQL in this package only.

- [ ] **Step 4: Run store tests and verify they pass**

Run:

```bash
cd server && go test ./internal/store ./internal/testutil -v
```

Expected: PASS for migration, token, device, session, and output chunk tests.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/store server/internal/testutil
git commit -m "feat: add sqlite store"
```

## Task 3: Server Authentication And Session Cookies

**Files:**
- Modify: `server/go.mod`
- Create: `server/internal/auth/auth_test.go`
- Create: `server/internal/auth/auth.go`

- [ ] **Step 1: Write failing auth tests**

Apply this patch:

```diff
*** Begin Patch
*** Update File: server/go.mod
@@
-require modernc.org/sqlite v1.33.1
+require (
+	github.com/google/uuid v1.6.0
+	golang.org/x/crypto v0.28.0
+	modernc.org/sqlite v1.33.1
+)
*** Add File: server/internal/auth/auth_test.go
+package auth
+
+import (
+	"net/http"
+	"net/http/httptest"
+	"testing"
+	"time"
+)
+
+func TestPasswordHashRoundTrip(t *testing.T) {
+	hash, err := HashPassword("correct horse battery staple")
+	if err != nil {
+		t.Fatalf("hash password: %v", err)
+	}
+	if !CheckPassword(hash, "correct horse battery staple") {
+		t.Fatal("password should match")
+	}
+	if CheckPassword(hash, "wrong") {
+		t.Fatal("wrong password should not match")
+	}
+}
+
+func TestSessionCookieRoundTrip(t *testing.T) {
+	manager := NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
+	rr := httptest.NewRecorder()
+	if err := manager.Set(rr, "user-1"); err != nil {
+		t.Fatalf("set session: %v", err)
+	}
+	req := httptest.NewRequest(http.MethodGet, "/", nil)
+	for _, cookie := range rr.Result().Cookies() {
+		req.AddCookie(cookie)
+	}
+	userID, err := manager.Get(req)
+	if err != nil {
+		t.Fatalf("get session: %v", err)
+	}
+	if userID != "user-1" {
+		t.Fatalf("userID = %q", userID)
+	}
+}
*** End Patch
```

- [ ] **Step 2: Run auth tests and verify they fail**

Run:

```bash
cd server && go test ./internal/auth -v
```

Expected: FAIL with compile errors containing `undefined: HashPassword` and `undefined: NewSessionManager`.

- [ ] **Step 3: Implement auth helpers**

Create `server/internal/auth/auth.go`:

```go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const CookieName = "vibe_session"

var ErrNoSession = errors.New("no session")
var ErrInvalidSession = errors.New("invalid session")

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func CheckPassword(hash string, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type SessionManager struct {
	secret []byte
	ttl    time.Duration
}

func NewSessionManager(secret []byte, ttl time.Duration) *SessionManager {
	return &SessionManager{secret: secret, ttl: ttl}
}

func (m *SessionManager) Set(w http.ResponseWriter, userID string) error {
	expires := time.Now().UTC().Add(m.ttl)
	value := userID + "|" + expires.Format(time.RFC3339Nano)
	sig := m.sign(value)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(value + "|" + sig)),
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *SessionManager) Get(r *http.Request) (string, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", ErrNoSession
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return "", ErrInvalidSession
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", ErrInvalidSession
	}
	value := parts[0] + "|" + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(m.sign(value))) {
		return "", ErrInvalidSession
	}
	expires, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return "", ErrInvalidSession
	}
	if time.Now().UTC().After(expires) {
		return "", ErrInvalidSession
	}
	return parts[0], nil
}

func (m *SessionManager) sign(value string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
```

- [ ] **Step 4: Run auth tests and verify they pass**

Run:

```bash
cd server && go test ./internal/auth -v
```

Expected: PASS for password and cookie tests.

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/internal/auth
git commit -m "feat: add server auth helpers"
```

## Task 4: Server REST API For Login, Devices, Tokens, And Sessions

**Files:**
- Create: `server/internal/config/config.go`
- Create: `server/internal/devices/service.go`
- Create: `server/internal/terminal/service.go`
- Create: `server/internal/audit/audit.go`
- Create: `server/internal/httpapi/router_test.go`
- Create: `server/internal/httpapi/router.go`
- Create: `server/cmd/server/main.go`

- [ ] **Step 1: Write failing REST API tests**

Create `server/internal/httpapi/router_test.go`:

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

func TestLoginMeAndAgentTokenFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})

	loginBody := bytes.NewBufferString(`{"username":"admin","password":"secret"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", loginBody)
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginRR.Code, loginRR.Body.String())
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	for _, cookie := range loginRR.Result().Cookies() {
		meReq.AddCookie(cookie)
	}
	meRR := httptest.NewRecorder()
	router.ServeHTTP(meRR, meReq)
	if meRR.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", meRR.Code, meRR.Body.String())
	}

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", bytes.NewBufferString(`{"name":"laptop","ttl_hours":24}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginRR.Result().Cookies() {
		tokenReq.AddCookie(cookie)
	}
	tokenRR := httptest.NewRecorder()
	router.ServeHTTP(tokenRR, tokenReq)
	if tokenRR.Code != http.StatusCreated {
		t.Fatalf("token status = %d body=%s", tokenRR.Code, tokenRR.Body.String())
	}
	var tokenResp map[string]string
	if err := json.Unmarshal(tokenRR.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenResp["token"] == "" {
		t.Fatal("token response should include raw token once")
	}
}

func TestCreateSessionRequiresOnlineDevice(t *testing.T) {
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(context.Background(), store.Device{
		ID: "dev-1", Name: "box", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: "hash", Authorized: true,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.CreateUser(context.Background(), store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`)))
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/sessions", bytes.NewBufferString(`{"shell_path":"/bin/bash","working_directory":"/home/dev","cols":80,"rows":24}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range loginRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run REST API tests and verify they fail**

Run:

```bash
cd server && go test ./internal/httpapi -v
```

Expected: FAIL with compile errors containing `undefined: NewRouter` and `undefined: Deps`.

- [ ] **Step 3: Implement config, services, REST router, and server entrypoint**

Create `server/internal/config/config.go` with:

```go
package config

import (
	"os"
	"time"
)

type Config struct {
	Addr           string
	DatabasePath   string
	SessionSecret  []byte
	AdminUsername  string
	AdminPassword  string
	SessionDuration time.Duration
}

func FromEnv() Config {
	addr := getenv("VIBE_ADDR", ":8080")
	dbPath := getenv("VIBE_DB", "data/vibe-terminal.db")
	secret := []byte(getenv("VIBE_SESSION_SECRET", "dev-session-secret-32-bytes-long"))
	return Config{
		Addr:            addr,
		DatabasePath:    dbPath,
		SessionSecret:   secret,
		AdminUsername:   os.Getenv("VIBE_ADMIN_USER"),
		AdminPassword:   os.Getenv("VIBE_ADMIN_PASSWORD"),
		SessionDuration: 24 * time.Hour,
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
```

Create `server/internal/devices/service.go` with a `Presence` map protected by `sync.RWMutex`:

```go
package devices

import "sync"

type Presence struct {
	mu     sync.RWMutex
	online map[string]bool
}

func NewPresence() *Presence {
	return &Presence{online: map[string]bool{}}
}

func (p *Presence) Set(deviceID string, online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online[deviceID] = online
}

func (p *Presence) Online(deviceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.online[deviceID]
}
```

Create `server/internal/terminal/service.go` with:

```go
package terminal

import "errors"

var ErrDeviceOffline = errors.New("device offline")

type CreateRequest struct {
	ShellPath        string `json:"shell_path"`
	WorkingDirectory string `json:"working_directory"`
	Cols             int    `json:"cols"`
	Rows             int    `json:"rows"`
}

func NormalizeCreateRequest(req CreateRequest) CreateRequest {
	if req.ShellPath == "" {
		req.ShellPath = "/bin/bash"
	}
	if req.WorkingDirectory == "" {
		req.WorkingDirectory = "$HOME"
	}
	if req.Cols <= 0 {
		req.Cols = 80
	}
	if req.Rows <= 0 {
		req.Rows = 24
	}
	return req
}
```

Create `server/internal/audit/audit.go` with:

```go
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/store"
)

type Writer struct {
	Store *store.DB
}

func (w Writer) Log(ctx context.Context, event store.AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	_, err := w.Store.CreateAuditEvent(ctx, event)
	return err
}
```

Create `server/internal/httpapi/router.go` with these endpoints:

- `POST /api/login`: decodes `username` and `password`, verifies against `store.GetUserByUsername`, sets session cookie, logs `login`.
- `POST /api/logout`: clears cookie.
- `GET /api/me`: returns `{"id":"...","username":"..."}`.
- `POST /api/agent-tokens`: requires session, creates a random 32-byte base64url token, stores only SHA-256 hash, returns raw token once.
- `GET /api/agent-tokens`: requires session, returns token metadata without `token_hash`.
- `GET /api/devices`: requires session, returns devices plus in-memory `online`.
- `POST /api/devices/{device_id}/sessions`: requires session; if device is offline, returns 409; otherwise creates `terminal_sessions` row with status `starting`.
- `GET /api/devices/{device_id}/sessions`: requires session, returns sessions.
- `POST /api/sessions/{session_id}/close`: requires session, marks session `closed`.
- `GET /api/sessions/{session_id}/output`: requires session, returns output chunk metadata.

Use `http.ServeMux`, explicit path parsing with `strings.TrimPrefix`, and helper functions `writeJSON`, `readJSON`, `requireUser`, `writeError`. Use `github.com/google/uuid` for IDs and `crypto/sha256` plus `base64.RawURLEncoding` for token hashes.

Create `server/cmd/server/main.go`:

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/config"
	"github.com/djy/vibe-terminal/server/internal/httpapi"
	"github.com/djy/vibe-terminal/server/internal/store"
)

func main() {
	ctx := context.Background()
	cfg := config.FromEnv()
	db, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("migrate database: %v", err)
	}
	router := httpapi.NewRouter(httpapi.Deps{
		Store:    db,
		Sessions: auth.NewSessionManager(cfg.SessionSecret, cfg.SessionDuration),
	})
	log.Printf("vibe-terminal server listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, router); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
```

- [ ] **Step 4: Run REST API tests and verify they pass**

Run:

```bash
cd server && go test ./internal/httpapi ./internal/devices ./internal/terminal ./internal/audit -v
```

Expected: PASS for login, session cookie, token creation, and offline-device conflict tests.

- [ ] **Step 5: Commit**

```bash
git add server/cmd server/internal/config server/internal/devices server/internal/terminal server/internal/audit server/internal/httpapi
git commit -m "feat: add server rest api"
```

## Task 5: Server WebSocket Hub, Agent Routing, And Output Indexing

**Files:**
- Modify: `server/go.mod`
- Create: `server/internal/ws/hub_test.go`
- Create: `server/internal/ws/hub.go`
- Modify: `server/internal/httpapi/router.go`

- [ ] **Step 1: Write failing hub tests**

Apply this patch:

```diff
*** Begin Patch
*** Update File: server/go.mod
@@
 require (
+	github.com/coder/websocket v1.8.12
 	github.com/google/uuid v1.6.0
 	golang.org/x/crypto v0.28.0
 	modernc.org/sqlite v1.33.1
 )
*** Add File: server/internal/ws/hub_test.go
+package ws
+
+import (
+	"testing"
+
+	"github.com/djy/vibe-terminal/server/internal/protocol"
+)
+
+func TestHubRoutesWebInputToAgent(t *testing.T) {
+	hub := NewHub()
+	agent := NewMemoryPeer("agent-dev-1")
+	web := NewMemoryPeer("web-1")
+	hub.AttachAgent("dev-1", agent)
+	hub.SubscribeWeb("sess-1", web)
+	hub.BindSession("sess-1", "dev-1")
+
+	err := hub.FromWeb(protocol.Stdin{SessionID: "sess-1", Data: "ls\n"})
+	if err != nil {
+		t.Fatalf("route stdin: %v", err)
+	}
+	got := agent.Pop()
+	if got.Type != protocol.TypeStdin || got.SessionID != "sess-1" {
+		t.Fatalf("unexpected agent message: %#v", got)
+	}
+}
+
+func TestHubBroadcastsAgentOutputToSubscribedWeb(t *testing.T) {
+	hub := NewHub()
+	agent := NewMemoryPeer("agent-dev-1")
+	web := NewMemoryPeer("web-1")
+	hub.AttachAgent("dev-1", agent)
+	hub.SubscribeWeb("sess-1", web)
+	hub.BindSession("sess-1", "dev-1")
+
+	err := hub.FromAgent("dev-1", protocol.Stdout{SessionID: "sess-1", Seq: 1, Data: "ok\r\n"})
+	if err != nil {
+		t.Fatalf("route stdout: %v", err)
+	}
+	got := web.Pop()
+	if got.Type != protocol.TypeStdout || got.SessionID != "sess-1" {
+		t.Fatalf("unexpected web message: %#v", got)
+	}
+}
*** End Patch
```

- [ ] **Step 2: Run hub tests and verify they fail**

Run:

```bash
cd server && go test ./internal/ws -v
```

Expected: FAIL with compile errors containing `undefined: NewHub` and `undefined: NewMemoryPeer`.

- [ ] **Step 3: Implement the in-memory hub**

Create `server/internal/ws/hub.go` with:

```go
package ws

import (
	"errors"
	"sync"

	"github.com/djy/vibe-terminal/server/internal/protocol"
)

var ErrNoAgent = errors.New("agent not connected")
var ErrNoSessionRoute = errors.New("session route not found")

type Outbound struct {
	Type      string
	SessionID string
	Payload   any
}

type Peer interface {
	Send(Outbound) error
}

type MemoryPeer struct {
	ID       string
	mu       sync.Mutex
	Messages []Outbound
}

func NewMemoryPeer(id string) *MemoryPeer {
	return &MemoryPeer{ID: id}
}

func (p *MemoryPeer) Send(msg Outbound) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Messages = append(p.Messages, msg)
	return nil
}

func (p *MemoryPeer) Pop() Outbound {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.Messages) == 0 {
		return Outbound{}
	}
	msg := p.Messages[0]
	p.Messages = p.Messages[1:]
	return msg
}

type Hub struct {
	mu             sync.RWMutex
	agents         map[string]Peer
	sessionDevices map[string]string
	webSubscribers map[string]map[Peer]struct{}
}

func NewHub() *Hub {
	return &Hub{
		agents:         map[string]Peer{},
		sessionDevices: map[string]string{},
		webSubscribers: map[string]map[Peer]struct{}{},
	}
}

func (h *Hub) AttachAgent(deviceID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[deviceID] = peer
}

func (h *Hub) DetachAgent(deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.agents, deviceID)
}

func (h *Hub) BindSession(sessionID string, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionDevices[sessionID] = deviceID
}

func (h *Hub) SubscribeWeb(sessionID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.webSubscribers[sessionID] == nil {
		h.webSubscribers[sessionID] = map[Peer]struct{}{}
	}
	h.webSubscribers[sessionID][peer] = struct{}{}
}

func (h *Hub) UnsubscribeWeb(sessionID string, peer Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.webSubscribers[sessionID], peer)
}

func (h *Hub) FromWeb(msg any) error {
	sessionID := sessionIDOf(msg)
	h.mu.RLock()
	deviceID, ok := h.sessionDevices[sessionID]
	if !ok {
		h.mu.RUnlock()
		return ErrNoSessionRoute
	}
	agent, ok := h.agents[deviceID]
	h.mu.RUnlock()
	if !ok {
		return ErrNoAgent
	}
	return agent.Send(Outbound{Type: typeOf(msg), SessionID: sessionID, Payload: msg})
}

func (h *Hub) FromAgent(deviceID string, msg any) error {
	sessionID := sessionIDOf(msg)
	h.mu.RLock()
	subs := make([]Peer, 0, len(h.webSubscribers[sessionID]))
	for peer := range h.webSubscribers[sessionID] {
		subs = append(subs, peer)
	}
	h.mu.RUnlock()
	for _, peer := range subs {
		if err := peer.Send(Outbound{Type: typeOf(msg), SessionID: sessionID, Payload: msg}); err != nil {
			return err
		}
	}
	return nil
}

func sessionIDOf(msg any) string {
	switch m := msg.(type) {
	case protocol.Stdin:
		return m.SessionID
	case protocol.Resize:
		return m.SessionID
	case protocol.Stdout:
		return m.SessionID
	case protocol.SessionState:
		return m.SessionID
	case protocol.SessionStarted:
		return m.SessionID
	case protocol.SessionExit:
		return m.SessionID
	case protocol.CloseSession:
		return m.SessionID
	default:
		return ""
	}
}

func typeOf(msg any) string {
	switch msg.(type) {
	case protocol.Stdin:
		return protocol.TypeStdin
	case protocol.Resize:
		return protocol.TypeResize
	case protocol.Stdout:
		return protocol.TypeStdout
	case protocol.SessionState:
		return protocol.TypeSessionState
	case protocol.SessionStarted:
		return protocol.TypeSessionStarted
	case protocol.SessionExit:
		return protocol.TypeSessionExit
	case protocol.CloseSession:
		return protocol.TypeCloseSession
	default:
		return protocol.TypeError
	}
}
```

- [ ] **Step 4: Add HTTP WebSocket endpoints**

Modify `server/internal/httpapi/router.go` so `Deps` includes `Hub *ws.Hub` and `Presence *devices.Presence`. When nil, construct defaults inside `NewRouter`.

Add:

- `GET /ws/agent`: accepts WebSocket, decodes `agent_hello`, verifies device credential hash with the store, calls `Presence.Set(deviceID, true)`, `Hub.AttachAgent(deviceID, peer)`, handles `sync_sessions`, `stdout`, `session_started`, and `session_exit`.
- `GET /ws/web`: requires session cookie, accepts WebSocket, handles `subscribe_session`, `stdin`, and `resize`.

For this task, the WebSocket peer can be implemented as an adapter that writes `protocol.Envelope` values to `github.com/coder/websocket.Conn`. Keep read loops context-bound and call `Presence.Set(deviceID, false)` on disconnect.

- [ ] **Step 5: Run hub and API tests**

Run:

```bash
cd server && go test ./internal/ws ./internal/httpapi -v
```

Expected: PASS for hub tests and REST API tests.

- [ ] **Step 6: Commit**

```bash
git add server/go.mod server/go.sum server/internal/ws server/internal/httpapi/router.go
git commit -m "feat: add server websocket hub"
```

## Task 6: Workspace Manager, Audit Output Files, And Server Docker Image

**Files:**
- Create: `server/internal/workspace/workspace_test.go`
- Create: `server/internal/workspace/workspace.go`
- Modify: `server/internal/terminal/service.go`
- Create: `server/Dockerfile`

- [ ] **Step 1: Write failing workspace tests**

Create `server/internal/workspace/workspace_test.go`:

```go
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
```

- [ ] **Step 2: Run workspace tests and verify they fail**

Run:

```bash
cd server && go test ./internal/workspace -v
```

Expected: FAIL with compile errors containing `undefined: NewFakeClient` and `undefined: Manager`.

- [ ] **Step 3: Implement workspace interface and fake client**

Create `server/internal/workspace/workspace.go`:

```go
package workspace

import (
	"context"
	"strings"
)

type Client interface {
	Exists(ctx context.Context, name string) (bool, error)
	Create(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
}

type ContainerSpec struct {
	Name       string
	Image      string
	DataVolume string
	Labels     map[string]string
}

type Manager struct {
	Client      Client
	Image       string
	DataVolume  string
	ContainerID string
}

func (m Manager) Ensure(ctx context.Context, userID string) error {
	name := m.ContainerID
	if name == "" {
		name = "vibe-workspace-" + sanitize(userID)
	}
	exists, err := m.Client.Exists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		if err := m.Client.Create(ctx, ContainerSpec{
			Name:       name,
			Image:      m.Image,
			DataVolume: m.DataVolume,
			Labels: map[string]string{
				"vibe-terminal.user_id": userID,
			},
		}); err != nil {
			return err
		}
	}
	return m.Client.Start(ctx, name)
}

func sanitize(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, ".", "-")
	return value
}

type FakeClient struct {
	Existing map[string]bool
	Created  map[string]bool
	Started  map[string]bool
}

func NewFakeClient() *FakeClient {
	return &FakeClient{
		Existing: map[string]bool{},
		Created:  map[string]bool{},
		Started:  map[string]bool{},
	}
}

func (c *FakeClient) Exists(ctx context.Context, name string) (bool, error) {
	return c.Existing[name] || c.Created[name], nil
}

func (c *FakeClient) Create(ctx context.Context, spec ContainerSpec) error {
	c.Created[spec.Name] = true
	return nil
}

func (c *FakeClient) Start(ctx context.Context, name string) error {
	c.Started[name] = true
	return nil
}
```

- [ ] **Step 4: Add output chunk writer boundary**

Modify `server/internal/terminal/service.go` to add:

```go
type OutputWriter interface {
	WriteChunk(sessionID string, startSeq int64, endSeq int64, data []byte) (path string, bytesWritten int64, err error)
}

type FileOutputWriter struct {
	Root string
}
```

Implement `FileOutputWriter.WriteChunk` so it creates `Root/sessions/<session_id>/` and writes a file named `<start_seq>-<end_seq>.log`. Return the relative path `sessions/<session_id>/<start_seq>-<end_seq>.log`.

- [ ] **Step 5: Add server Dockerfile**

Create `server/Dockerfile`:

```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./server/
WORKDIR /src/server
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /out/vibe-server ./cmd/server

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/vibe-server /app/vibe-server
EXPOSE 8080
ENTRYPOINT ["/app/vibe-server"]
```

- [ ] **Step 6: Run server tests**

Run:

```bash
cd server && go test ./... -v
```

Expected: PASS for all server packages.

- [ ] **Step 7: Commit**

```bash
git add server/internal/workspace server/internal/terminal/service.go server/Dockerfile
git commit -m "feat: add workspace manager"
```

## Task 7: Rust Agent Protocol, Config, Registry, And Buffer

**Files:**
- Create: `agent/Cargo.toml`
- Create: `agent/src/main.rs`
- Create: `agent/src/config.rs`
- Create: `agent/src/protocol.rs`
- Create: `agent/src/registry.rs`
- Create: `agent/src/buffer.rs`

- [ ] **Step 1: Create Rust package and failing core tests**

Apply this patch:

```diff
*** Begin Patch
*** Add File: agent/Cargo.toml
+[package]
+name = "vibe-agent"
+version = "0.1.0"
+edition = "2021"
+
+[dependencies]
+anyhow = "1"
+base64 = "0.22"
+clap = { version = "4", features = ["derive"] }
+dirs = "5"
+serde = { version = "1", features = ["derive"] }
+serde_json = "1"
+thiserror = "1"
+tokio = { version = "1", features = ["macros", "rt-multi-thread", "signal", "time", "process", "fs", "io-util"] }
+tokio-tungstenite = { version = "0.24", features = ["rustls-tls-webpki-roots"] }
+tracing = "0.1"
+tracing-subscriber = "0.3"
+uuid = { version = "1", features = ["v4", "serde"] }
+
+[dev-dependencies]
+tempfile = "3"
*** Add File: agent/src/protocol.rs
+use serde::{Deserialize, Serialize};
+
+pub const PROTOCOL_VERSION: &str = "v1";
+
+#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
+pub struct Envelope<T> {
+    #[serde(rename = "type")]
+    pub message_type: String,
+    #[serde(default, skip_serializing_if = "Option::is_none")]
+    pub session_id: Option<String>,
+    pub payload: T,
+}
+
+#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
+pub struct StartSession {
+    pub session_id: String,
+    pub shell_path: String,
+    pub working_directory: String,
+    pub cols: u16,
+    pub rows: u16,
+}
+
+#[cfg(test)]
+mod tests {
+    use super::*;
+
+    #[test]
+    fn start_session_round_trip() {
+        let message = Envelope {
+            message_type: "start_session".to_string(),
+            session_id: Some("sess-1".to_string()),
+            payload: StartSession {
+                session_id: "sess-1".to_string(),
+                shell_path: "/bin/bash".to_string(),
+                working_directory: "/home/dev".to_string(),
+                cols: 120,
+                rows: 32,
+            },
+        };
+        let json = serde_json::to_string(&message).expect("serialize");
+        let decoded: Envelope<StartSession> = serde_json::from_str(&json).expect("decode");
+        assert_eq!(decoded.payload.session_id, "sess-1");
+        assert_eq!(decoded.payload.cols, 120);
+    }
+}
*** Add File: agent/src/buffer.rs
+use std::collections::VecDeque;
+
+#[derive(Debug, Clone, PartialEq)]
+pub struct OutputFrame {
+    pub seq: i64,
+    pub data: String,
+}
+
+#[derive(Debug)]
+pub struct OutputBuffer {
+    max_frames: usize,
+    next_seq: i64,
+    frames: VecDeque<OutputFrame>,
+}
+
+#[cfg(test)]
+mod tests {
+    use super::*;
+
+    #[test]
+    fn output_buffer_evicts_old_frames() {
+        let mut buffer = OutputBuffer::new(2);
+        buffer.push("one".to_string());
+        buffer.push("two".to_string());
+        buffer.push("three".to_string());
+        let frames = buffer.frames_since(0);
+        assert_eq!(frames.len(), 2);
+        assert_eq!(frames[0].seq, 2);
+        assert_eq!(frames[1].data, "three");
+    }
+}
*** Add File: agent/src/main.rs
+mod buffer;
+mod config;
+mod protocol;
+mod registry;
+
+use clap::{Parser, Subcommand};
+
+#[derive(Parser, Debug)]
+#[command(name = "vibe-agent")]
+struct Cli {
+    #[command(subcommand)]
+    command: Command,
+}
+
+#[derive(Subcommand, Debug)]
+enum Command {
+    Run,
+    Register {
+        #[arg(long)]
+        server: String,
+        #[arg(long)]
+        token: String,
+    },
+}
+
+fn main() {
+    let cli = Cli::parse();
+    println!("{:?}", cli.command);
+}
*** End Patch
```

- [ ] **Step 2: Run agent tests and verify they fail**

Run:

```bash
cd agent && cargo test
```

Expected: FAIL with compile errors containing `file not found for module config`, `file not found for module registry`, and `no function or associated item named new found for struct OutputBuffer`.

- [ ] **Step 3: Implement config, registry, and buffer**

Create `agent/src/config.rs`:

```rust
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AgentConfig {
    pub server_url: String,
    pub device_id: String,
    pub credential: String,
    pub device_name: String,
}

pub fn default_config_path() -> Result<PathBuf> {
    let base = dirs::config_dir().context("config directory not found")?;
    Ok(base.join("vibe-terminal").join("agent.json"))
}

pub fn load(path: &Path) -> Result<AgentConfig> {
    let data = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
    serde_json::from_str(&data).with_context(|| format!("decode {}", path.display()))
}

pub fn save(path: &Path, config: &AgentConfig) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).with_context(|| format!("create {}", parent.display()))?;
    }
    let data = serde_json::to_string_pretty(config)?;
    fs::write(path, data).with_context(|| format!("write {}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn save_and_load_config() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("agent.json");
        let config = AgentConfig {
            server_url: "https://example.com".into(),
            device_id: "dev-1".into(),
            credential: "secret".into(),
            device_name: "laptop".into(),
        };
        save(&path, &config).expect("save");
        assert_eq!(load(&path).expect("load"), config);
    }
}
```

Create `agent/src/registry.rs`:

```rust
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionRecord {
    pub session_id: String,
    pub title: String,
    pub shell_path: String,
    pub working_directory: String,
    pub status: String,
    pub agent_pid: u32,
    pub last_output_seq: i64,
}

#[derive(Debug, Default, Clone, Serialize, Deserialize)]
pub struct SessionRegistry {
    sessions: BTreeMap<String, SessionRecord>,
}

impl SessionRegistry {
    pub fn upsert(&mut self, record: SessionRecord) {
        self.sessions.insert(record.session_id.clone(), record);
    }

    pub fn mark_lost_after_restart(&mut self) {
        for record in self.sessions.values_mut() {
            if record.status == "running" || record.status == "starting" {
                record.status = "lost".to_string();
            }
        }
    }

    pub fn list(&self) -> Vec<SessionRecord> {
        self.sessions.values().cloned().collect()
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).with_context(|| format!("create {}", parent.display()))?;
        }
        fs::write(path, serde_json::to_string_pretty(self)?)
            .with_context(|| format!("write {}", path.display()))
    }

    pub fn load(path: &Path) -> Result<Self> {
        if !path.exists() {
            return Ok(Self::default());
        }
        let data = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
        serde_json::from_str(&data).with_context(|| format!("decode {}", path.display()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mark_running_sessions_lost_after_restart() {
        let mut registry = SessionRegistry::default();
        registry.upsert(SessionRecord {
            session_id: "sess-1".into(),
            title: "bash".into(),
            shell_path: "/bin/bash".into(),
            working_directory: "/home/dev".into(),
            status: "running".into(),
            agent_pid: 123,
            last_output_seq: 9,
        });
        registry.mark_lost_after_restart();
        assert_eq!(registry.list()[0].status, "lost");
    }
}
```

Replace `agent/src/buffer.rs` with:

```rust
use std::collections::VecDeque;

#[derive(Debug, Clone, PartialEq)]
pub struct OutputFrame {
    pub seq: i64,
    pub data: String,
}

#[derive(Debug)]
pub struct OutputBuffer {
    max_frames: usize,
    next_seq: i64,
    frames: VecDeque<OutputFrame>,
}

impl OutputBuffer {
    pub fn new(max_frames: usize) -> Self {
        Self {
            max_frames,
            next_seq: 1,
            frames: VecDeque::new(),
        }
    }

    pub fn push(&mut self, data: String) -> OutputFrame {
        let frame = OutputFrame {
            seq: self.next_seq,
            data,
        };
        self.next_seq += 1;
        self.frames.push_back(frame.clone());
        while self.frames.len() > self.max_frames {
            self.frames.pop_front();
        }
        frame
    }

    pub fn frames_since(&self, seq: i64) -> Vec<OutputFrame> {
        self.frames
            .iter()
            .filter(|frame| frame.seq > seq)
            .cloned()
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn output_buffer_evicts_old_frames() {
        let mut buffer = OutputBuffer::new(2);
        buffer.push("one".to_string());
        buffer.push("two".to_string());
        buffer.push("three".to_string());
        let frames = buffer.frames_since(0);
        assert_eq!(frames.len(), 2);
        assert_eq!(frames[0].seq, 2);
        assert_eq!(frames[1].data, "three");
    }
}
```

- [ ] **Step 4: Run agent core tests**

Run:

```bash
cd agent && cargo test protocol::tests buffer::tests registry::tests config::tests
```

Expected: PASS for protocol, buffer, registry, and config tests.

- [ ] **Step 5: Commit**

```bash
git add agent/Cargo.toml agent/Cargo.lock agent/src
git commit -m "feat: add agent core state"
```

## Task 8: Rust Agent PTY Manager And WebSocket Client

**Files:**
- Modify: `agent/Cargo.toml`
- Create: `agent/src/pty_manager.rs`
- Create: `agent/src/client.rs`
- Modify: `agent/src/main.rs`
- Create: `agent/tests/pty_smoke.rs`

- [ ] **Step 1: Write failing PTY integration test**

Apply this patch:

```diff
*** Begin Patch
*** Update File: agent/Cargo.toml
@@
 uuid = { version = "1", features = ["v4", "serde"] }
+portable-pty = "0.8"
*** Add File: agent/tests/pty_smoke.rs
+use std::time::Duration;
+
+use vibe_agent::pty_manager::{PtyManager, StartRequest};
+
+#[test]
+fn pty_runs_shell_command() {
+    let mut manager = PtyManager::new();
+    let session = manager
+        .start(StartRequest {
+            session_id: "sess-1".into(),
+            shell_path: "/bin/sh".into(),
+            working_directory: "/tmp".into(),
+            cols: 80,
+            rows: 24,
+        })
+        .expect("start pty");
+    manager.write(&session.session_id, "printf hello\\nexit\\n").expect("write");
+    std::thread::sleep(Duration::from_millis(300));
+    let output = manager.read_available(&session.session_id).expect("read");
+    assert!(output.contains("hello"), "output was {output:?}");
+}
*** End Patch
```

Also expose a library by creating `agent/src/lib.rs`:

```rust
pub mod buffer;
pub mod client;
pub mod config;
pub mod protocol;
pub mod pty_manager;
pub mod registry;
```

- [ ] **Step 2: Run PTY test and verify it fails**

Run:

```bash
cd agent && cargo test --test pty_smoke
```

Expected: FAIL with compile errors containing `could not find pty_manager in vibe_agent` or `undefined PtyManager`.

- [ ] **Step 3: Implement PTY manager**

Create `agent/src/pty_manager.rs`:

```rust
use anyhow::{anyhow, Context, Result};
use portable_pty::{CommandBuilder, NativePtySystem, PtySize, PtySystem};
use std::collections::BTreeMap;
use std::io::{Read, Write};
use std::thread;
use std::time::Duration;

#[derive(Debug, Clone)]
pub struct StartRequest {
    pub session_id: String,
    pub shell_path: String,
    pub working_directory: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone)]
pub struct StartedSession {
    pub session_id: String,
    pub pid: u32,
}

struct ManagedSession {
    writer: Box<dyn Write + Send>,
    reader: Box<dyn Read + Send>,
    child: Box<dyn portable_pty::Child + Send + Sync>,
}

pub struct PtyManager {
    sessions: BTreeMap<String, ManagedSession>,
}

impl PtyManager {
    pub fn new() -> Self {
        Self {
            sessions: BTreeMap::new(),
        }
    }

    pub fn start(&mut self, req: StartRequest) -> Result<StartedSession> {
        let pty_system = NativePtySystem::default();
        let pair = pty_system
            .openpty(PtySize {
                rows: req.rows,
                cols: req.cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .context("open pty")?;
        let mut command = CommandBuilder::new(req.shell_path.clone());
        command.cwd(req.working_directory.clone());
        let child = pair.slave.spawn_command(command).context("spawn shell")?;
        let pid = child.process_id().unwrap_or(0);
        let reader = pair.master.try_clone_reader().context("clone pty reader")?;
        let writer = pair.master.take_writer().context("take pty writer")?;
        self.sessions.insert(
            req.session_id.clone(),
            ManagedSession {
                writer,
                reader,
                child,
            },
        );
        Ok(StartedSession {
            session_id: req.session_id,
            pid,
        })
    }

    pub fn write(&mut self, session_id: &str, data: &str) -> Result<()> {
        let session = self
            .sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow!("session not found: {session_id}"))?;
        session.writer.write_all(data.as_bytes()).context("write pty")
    }

    pub fn read_available(&mut self, session_id: &str) -> Result<String> {
        let session = self
            .sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow!("session not found: {session_id}"))?;
        thread::sleep(Duration::from_millis(50));
        let mut buf = [0_u8; 8192];
        let n = session.reader.read(&mut buf).unwrap_or(0);
        Ok(String::from_utf8_lossy(&buf[..n]).to_string())
    }

    pub fn close(&mut self, session_id: &str) -> Result<()> {
        if let Some(mut session) = self.sessions.remove(session_id) {
            let _ = session.child.kill();
        }
        Ok(())
    }
}

impl Default for PtyManager {
    fn default() -> Self {
        Self::new()
    }
}
```

- [ ] **Step 4: Implement client command loop boundary**

Create `agent/src/client.rs` with:

```rust
use anyhow::Result;

use crate::config::AgentConfig;
use crate::protocol::PROTOCOL_VERSION;
use crate::registry::SessionRegistry;

#[derive(Debug, Clone)]
pub struct ClientState {
    pub config: AgentConfig,
    pub registry: SessionRegistry,
}

impl ClientState {
    pub fn agent_hello_json(&self) -> Result<String> {
        let payload = serde_json::json!({
            "device_id": self.config.device_id,
            "credential": self.config.credential,
            "platform": std::env::consts::OS,
            "agent_version": env!("CARGO_PKG_VERSION"),
            "protocol_version": PROTOCOL_VERSION,
            "sessions": self.registry.list()
        });
        Ok(serde_json::json!({
            "type": "agent_hello",
            "payload": payload
        })
        .to_string())
    }
}
```

Modify `agent/src/main.rs` to use the library modules from `lib.rs`, load config on `run`, and save config on `register`. The `register` command writes a config with a generated `device_id`, the supplied server URL, the supplied token as the temporary credential, and the local hostname from `HOSTNAME` or `COMPUTERNAME`.

- [ ] **Step 5: Run agent tests**

Run:

```bash
cd agent && cargo test
```

Expected: PASS for core tests and PTY smoke test on Linux, macOS, or WSL with `/bin/sh`.

- [ ] **Step 6: Commit**

```bash
git add agent/Cargo.toml agent/Cargo.lock agent/src agent/tests
git commit -m "feat: add agent pty manager"
```

## Task 9: React Web App With Login, Device List, And Terminal Tabs

**Files:**
- Create: `web/package.json`
- Create: `web/index.html`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/src/main.tsx`
- Create: `web/src/api.ts`
- Create: `web/src/ws.ts`
- Create: `web/src/App.tsx`
- Create: `web/src/components/LoginView.tsx`
- Create: `web/src/components/DeviceList.tsx`
- Create: `web/src/components/TerminalTabs.tsx`
- Create: `web/src/components/TerminalPane.tsx`
- Create: `web/src/styles.css`
- Create: `web/src/test/App.test.tsx`
- Create: `web/src/test/ws.test.ts`

- [ ] **Step 1: Write failing web tests**

Apply this patch:

```diff
*** Begin Patch
*** Add File: web/package.json
+{
+  "name": "vibe-terminal-web",
+  "version": "0.1.0",
+  "private": true,
+  "type": "module",
+  "scripts": {
+    "dev": "vite",
+    "build": "tsc && vite build",
+    "test": "vitest"
+  },
+  "dependencies": {
+    "@vitejs/plugin-react": "^4.3.2",
+    "lucide-react": "^0.468.0",
+    "react": "^18.3.1",
+    "react-dom": "^18.3.1",
+    "xterm": "^5.3.0",
+    "xterm-addon-fit": "^0.8.0"
+  },
+  "devDependencies": {
+    "@testing-library/jest-dom": "^6.6.3",
+    "@testing-library/react": "^16.0.1",
+    "@testing-library/user-event": "^14.5.2",
+    "@types/react": "^18.3.12",
+    "@types/react-dom": "^18.3.1",
+    "jsdom": "^25.0.1",
+    "typescript": "^5.6.3",
+    "vite": "^5.4.11",
+    "vitest": "^2.1.4"
+  }
+}
*** Add File: web/vite.config.ts
+import { defineConfig } from 'vite';
+import react from '@vitejs/plugin-react';
+
+export default defineConfig({
+  plugins: [react()],
+  test: {
+    environment: 'jsdom',
+    setupFiles: ['./src/test/setup.ts'],
+  },
+});
*** Add File: web/tsconfig.json
+{
+  "compilerOptions": {
+    "target": "ES2020",
+    "useDefineForClassFields": true,
+    "lib": ["DOM", "DOM.Iterable", "ES2020"],
+    "allowJs": false,
+    "skipLibCheck": true,
+    "esModuleInterop": true,
+    "allowSyntheticDefaultImports": true,
+    "strict": true,
+    "forceConsistentCasingInFileNames": true,
+    "module": "ESNext",
+    "moduleResolution": "Node",
+    "resolveJsonModule": true,
+    "isolatedModules": true,
+    "noEmit": true,
+    "jsx": "react-jsx"
+  },
+  "include": ["src"],
+  "references": []
+}
*** Add File: web/src/test/setup.ts
+import '@testing-library/jest-dom/vitest';
*** Add File: web/src/test/App.test.tsx
+import { render, screen } from '@testing-library/react';
+import userEvent from '@testing-library/user-event';
+import { describe, expect, it, vi } from 'vitest';
+import { AppView } from '../App';
+
+describe('AppView', () => {
+  it('shows login when there is no user', () => {
+    render(<AppView user={null} devices={[]} sessions={{}} onLogin={vi.fn()} onCreateSession={vi.fn()} />);
+    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument();
+  });
+
+  it('opens multiple terminal tabs for an online device', async () => {
+    const createSession = vi.fn()
+      .mockResolvedValueOnce({ id: 'sess-1', title: 'bash', status: 'running' })
+      .mockResolvedValueOnce({ id: 'sess-2', title: 'bash', status: 'running' });
+    render(
+      <AppView
+        user={{ id: 'user-1', username: 'admin' }}
+        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
+        sessions={{}}
+        onLogin={vi.fn()}
+        onCreateSession={createSession}
+      />
+    );
+    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
+    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
+    expect(await screen.findByRole('tab', { name: /sess-1/i })).toBeInTheDocument();
+    expect(await screen.findByRole('tab', { name: /sess-2/i })).toBeInTheDocument();
+  });
+});
*** Add File: web/src/test/ws.test.ts
+import { describe, expect, it } from 'vitest';
+import { encodeStdin } from '../ws';
+
+describe('encodeStdin', () => {
+  it('creates a protocol envelope', () => {
+    expect(encodeStdin('sess-1', 'ls\\n')).toEqual(JSON.stringify({
+      type: 'stdin',
+      session_id: 'sess-1',
+      payload: { session_id: 'sess-1', data: 'ls\\n' },
+    }));
+  });
+});
*** End Patch
```

- [ ] **Step 2: Run web tests and verify they fail**

Run:

```bash
cd web && npm install && npm test -- --run
```

Expected: FAIL with module errors for `../App` and `../ws`.

- [ ] **Step 3: Implement API client and WebSocket helpers**

Create `web/src/api.ts`:

```ts
export type User = { id: string; username: string };
export type Device = { id: string; name: string; platform: string; online: boolean };
export type Session = { id: string; title: string; status: string };

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    credentials: 'include',
  });
  if (!res.ok) {
    throw new Error(`${res.status} ${await res.text()}`);
  }
  return res.json() as Promise<T>;
}

export function login(username: string, password: string): Promise<User> {
  return request<User>('/api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
}

export function me(): Promise<User> {
  return request<User>('/api/me');
}

export function listDevices(): Promise<Device[]> {
  return request<Device[]>('/api/devices');
}

export function createSession(deviceId: string): Promise<Session> {
  return request<Session>(`/api/devices/${deviceId}/sessions`, {
    method: 'POST',
    body: JSON.stringify({ shell_path: '/bin/bash', working_directory: '$HOME', cols: 120, rows: 32 }),
  });
}
```

Create `web/src/ws.ts`:

```ts
export type TerminalEvent =
  | { type: 'stdout'; session_id: string; payload: { seq: number; data: string } }
  | { type: 'session_state'; session_id: string; payload: { status: string; message?: string } }
  | { type: 'error'; payload: { code: string; message: string } };

export function encodeStdin(sessionId: string, data: string): string {
  return JSON.stringify({
    type: 'stdin',
    session_id: sessionId,
    payload: { session_id: sessionId, data },
  });
}

export function encodeResize(sessionId: string, cols: number, rows: number): string {
  return JSON.stringify({
    type: 'resize',
    session_id: sessionId,
    payload: { session_id: sessionId, cols, rows },
  });
}

export function encodeSubscribe(sessionId: string): string {
  return JSON.stringify({
    type: 'subscribe_session',
    session_id: sessionId,
    payload: { session_id: sessionId },
  });
}
```

- [ ] **Step 4: Implement React views**

Create `web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>vibe-terminal</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

Create `web/src/main.tsx`:

```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import './styles.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
```

Create `web/src/components/LoginView.tsx`:

```tsx
import { FormEvent, useState } from 'react';

export function LoginView({ onLogin }: { onLogin: (username: string, password: string) => Promise<void> }) {
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await onLogin(username, password);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    }
  }

  return (
    <main className="login">
      <form onSubmit={submit} className="loginForm">
        <h1>vibe-terminal</h1>
        <label>
          Username
          <input value={username} onChange={(event) => setUsername(event.target.value)} />
        </label>
        <label>
          Password
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit">Login</button>
      </form>
    </main>
  );
}
```

Create `web/src/components/DeviceList.tsx`:

```tsx
import type { Device } from '../api';

export function DeviceList({ devices, onCreateSession }: { devices: Device[]; onCreateSession: (deviceId: string) => Promise<void> }) {
  return (
    <aside className="devices">
      <h2>Devices</h2>
      {devices.map((device) => (
        <section key={device.id} className="deviceRow">
          <div>
            <strong>{device.name}</strong>
            <span>{device.platform}</span>
          </div>
          <span className={device.online ? 'online' : 'offline'}>{device.online ? 'online' : 'offline'}</span>
          <button disabled={!device.online} onClick={() => onCreateSession(device.id)}>
            New terminal
          </button>
        </section>
      ))}
    </aside>
  );
}
```

Create `web/src/components/TerminalTabs.tsx`:

```tsx
import { useState } from 'react';
import type { Session } from '../api';
import { TerminalPane } from './TerminalPane';

export function TerminalTabs({ sessions }: { sessions: Session[] }) {
  const [active, setActive] = useState<string | null>(sessions[0]?.id ?? null);
  const activeSession = sessions.find((session) => session.id === active) ?? sessions[0];
  if (sessions.length === 0) {
    return <main className="empty">No terminal session open</main>;
  }
  return (
    <main className="terminalArea">
      <div role="tablist" className="tabs">
        {sessions.map((session) => (
          <button
            role="tab"
            aria-selected={session.id === activeSession.id}
            key={session.id}
            onClick={() => setActive(session.id)}
          >
            {session.id} · {session.status}
          </button>
        ))}
      </div>
      <TerminalPane sessionId={activeSession.id} readOnly={activeSession.status !== 'running'} />
    </main>
  );
}
```

Create `web/src/components/TerminalPane.tsx`:

```tsx
import { useEffect, useRef } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';

export function TerminalPane({ sessionId, readOnly }: { sessionId: string; readOnly: boolean }) {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    const terminal = new Terminal({ cursorBlink: !readOnly, disableStdin: readOnly });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(ref.current);
    fit.fit();
    terminal.writeln(`connected to ${sessionId}`);
    return () => terminal.dispose();
  }, [sessionId, readOnly]);

  return <div className="terminalPane" ref={ref} />;
}
```

Create `web/src/App.tsx`:

```tsx
import { useEffect, useMemo, useState } from 'react';
import type { Device, Session, User } from './api';
import * as api from './api';
import { DeviceList } from './components/DeviceList';
import { LoginView } from './components/LoginView';
import { TerminalTabs } from './components/TerminalTabs';

type SessionsByDevice = Record<string, Session[]>;

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [sessions, setSessions] = useState<SessionsByDevice>({});

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null));
  }, []);

  useEffect(() => {
    if (!user) return;
    api.listDevices().then(setDevices).catch(() => setDevices([]));
  }, [user]);

  async function handleLogin(username: string, password: string) {
    const loggedIn = await api.login(username, password);
    setUser(loggedIn);
    setDevices(await api.listDevices());
  }

  async function handleCreateSession(deviceId: string) {
    const session = await api.createSession(deviceId);
    setSessions((current) => ({
      ...current,
      [deviceId]: [...(current[deviceId] ?? []), session],
    }));
  }

  return <AppView user={user} devices={devices} sessions={sessions} onLogin={handleLogin} onCreateSession={handleCreateSession} />;
}

export function AppView({
  user,
  devices,
  sessions,
  onLogin,
  onCreateSession,
}: {
  user: User | null;
  devices: Device[];
  sessions: SessionsByDevice;
  onLogin: (username: string, password: string) => Promise<void>;
  onCreateSession: (deviceId: string) => Promise<Session | void>;
}) {
  const allSessions = useMemo(() => Object.values(sessions).flat(), [sessions]);
  if (!user) return <LoginView onLogin={onLogin} />;
  return (
    <div className="shell">
      <DeviceList devices={devices} onCreateSession={async (deviceId) => { await onCreateSession(deviceId); }} />
      <TerminalTabs sessions={allSessions} />
    </div>
  );
}
```

Create `web/src/styles.css` with a restrained operations layout:

```css
* {
  box-sizing: border-box;
}

body {
  margin: 0;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: #101114;
  color: #f4f5f7;
}

button,
input {
  font: inherit;
}

.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
}

.loginForm {
  width: min(360px, calc(100vw - 32px));
  display: grid;
  gap: 14px;
  padding: 24px;
  border: 1px solid #30343a;
  border-radius: 8px;
  background: #181b20;
}

.loginForm h1 {
  margin: 0 0 8px;
  font-size: 24px;
}

.loginForm label {
  display: grid;
  gap: 6px;
  color: #b9c0cc;
}

.loginForm input {
  min-height: 38px;
  border-radius: 6px;
  border: 1px solid #3d444f;
  background: #0f1115;
  color: #f4f5f7;
  padding: 0 10px;
}

.loginForm button,
.deviceRow button,
.tabs button {
  min-height: 34px;
  border-radius: 6px;
  border: 1px solid #46515f;
  background: #253244;
  color: #f4f5f7;
  padding: 0 12px;
}

.shell {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
}

.devices {
  border-right: 1px solid #282d35;
  padding: 16px;
  background: #15181d;
}

.devices h2 {
  margin: 0 0 12px;
  font-size: 16px;
}

.deviceRow {
  display: grid;
  gap: 8px;
  padding: 12px 0;
  border-bottom: 1px solid #282d35;
}

.deviceRow div {
  display: grid;
  gap: 4px;
}

.deviceRow span {
  color: #a8b0bd;
}

.online {
  color: #67d391;
}

.offline,
.error {
  color: #ff7b72;
}

.terminalArea {
  min-width: 0;
  display: grid;
  grid-template-rows: 42px minmax(0, 1fr);
}

.tabs {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 6px;
  border-bottom: 1px solid #282d35;
  overflow-x: auto;
}

.tabs button[aria-selected="true"] {
  background: #37516f;
}

.terminalPane {
  min-height: 0;
  padding: 8px;
}

.empty {
  display: grid;
  place-items: center;
  color: #a8b0bd;
}
```

- [ ] **Step 5: Run web tests and build**

Run:

```bash
cd web && npm test -- --run && npm run build
```

Expected: PASS for `AppView` and `encodeStdin`; build exits successfully.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat: add web terminal ui"
```

## Task 10: Deployment, Service Templates, And Final Smoke Path

**Files:**
- Create: `docker-compose.yml`
- Create: `deploy/Caddyfile.example`
- Create: `deploy/systemd/vibe-agent.service`
- Create: `deploy/launchd/com.vibe-terminal.agent.plist`
- Create: `deploy/scripts/vibe-agent-wsl.sh`
- Create: `docs/deployment.md`
- Modify: `README.md`

- [ ] **Step 1: Write deployment files**

Create `docker-compose.yml`:

```yaml
services:
  server:
    build:
      context: .
      dockerfile: server/Dockerfile
    environment:
      VIBE_ADDR: ":8080"
      VIBE_DB: "/data/vibe-terminal.db"
      VIBE_SESSION_SECRET: "${VIBE_SESSION_SECRET}"
      VIBE_ADMIN_USER: "${VIBE_ADMIN_USER}"
      VIBE_ADMIN_PASSWORD: "${VIBE_ADMIN_PASSWORD}"
    volumes:
      - vibe-data:/data
      - vibe-workspace-data:/workspace-data
      - /var/run/docker.sock:/var/run/docker.sock
    ports:
      - "8080:8080"
    restart: unless-stopped

volumes:
  vibe-data:
  vibe-workspace-data:
```

Create `deploy/Caddyfile.example`:

```caddyfile
terminal.example.com {
  encode gzip
  reverse_proxy 127.0.0.1:8080
}
```

Create `deploy/systemd/vibe-agent.service`:

```ini
[Unit]
Description=vibe-terminal agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vibe-agent run
Restart=always
RestartSec=5
Environment=RUST_LOG=info

[Install]
WantedBy=default.target
```

Create `deploy/launchd/com.vibe-terminal.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string>com.vibe-terminal.agent</string>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/vibe-agent</string>
      <string>run</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/vibe-agent.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/vibe-agent.err.log</string>
  </dict>
</plist>
```

Create `deploy/scripts/vibe-agent-wsl.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
exec "${VIBE_AGENT_BIN:-$HOME/.local/bin/vibe-agent}" run
```

Create `docs/deployment.md`:

````markdown
# vibe-terminal Deployment

## Server

Create an environment file on the VPS:

```bash
export VIBE_SESSION_SECRET="$(openssl rand -base64 32)"
export VIBE_ADMIN_USER="admin"
export VIBE_ADMIN_PASSWORD="replace-with-a-long-password"
```

Start the server:

```bash
docker compose up -d --build
```

Use Caddy or Nginx in front of port `8080` and expose only HTTPS.

## Agent

Register a controlled machine:

```bash
vibe-agent register --server https://terminal.example.com --token <token-from-web>
vibe-agent run
```

Linux can install `deploy/systemd/vibe-agent.service`. macOS can install `deploy/launchd/com.vibe-terminal.agent.plist`. WSL can run `deploy/scripts/vibe-agent-wsl.sh`.

## Smoke Test

1. Open the web UI.
2. Log in as the administrator.
3. Create an agent registration token.
4. Register and run one agent on Linux, macOS, or WSL.
5. Confirm the device appears online.
6. Open two terminal tabs.
7. Refresh the browser and confirm the tabs can reconnect.
8. Restart the server container and confirm the agent syncs running sessions.
````

- [ ] **Step 2: Update README with exact local workflow**

Append to `README.md`:

````markdown

## Development workflow

Server:

```bash
cd server
go test ./...
go run ./cmd/server
```

Agent:

```bash
cd agent
cargo test
cargo run -- register --server http://localhost:8080 --token dev-token
cargo run -- run
```

Web:

```bash
cd web
npm install
npm test -- --run
npm run dev
```

Full checks:

```bash
make test
```
````

- [ ] **Step 3: Run final verification commands**

Run:

```bash
cd server && go test ./...
```

Expected: PASS for all server packages.

Run:

```bash
cd agent && cargo test
```

Expected: PASS for all agent unit tests and PTY smoke tests.

Run:

```bash
cd web && npm test -- --run && npm run build
```

Expected: PASS for all web tests and a successful Vite production build.

Run:

```bash
docker compose config >/dev/null
```

Expected: command exits with status 0.

- [ ] **Step 4: Commit**

```bash
chmod +x deploy/scripts/vibe-agent-wsl.sh
git add docker-compose.yml deploy docs/deployment.md README.md
git commit -m "chore: add deployment assets"
```

## Final Verification Checklist

- [ ] `cd server && go test ./...` passes.
- [ ] `cd agent && cargo test` passes on Linux, macOS, or WSL.
- [ ] `cd web && npm test -- --run && npm run build` passes.
- [ ] `docker compose config >/dev/null` passes.
- [ ] Web login works with a seeded administrator account.
- [ ] Agent registration stores a device credential and does not reuse the registration token.
- [ ] Device appears online after `/ws/agent` connects.
- [ ] Web can create two sessions for one online device.
- [ ] Browser refresh restores session list and recent output.
- [ ] Server restart followed by agent reconnect syncs session state.
- [ ] Agent restart marks old running sessions as `lost`.
- [ ] Workspace manager failure is surfaced as a UI warning while real-time terminal routing keeps running.

## Plan Self-Review

- Spec coverage: the tasks cover protocol, server SQLite, auth, agent registration boundary, device and session REST APIs, WebSocket routing, workspace lifecycle, Rust PTY ownership, React multi-tab UI, Docker Compose deployment, service templates, and smoke verification.
- Scope boundaries: mobile app, multi-user RBAC, native Windows shells, Kubernetes, E2EE, command approval, server-side command execution, file browser, and full IDE features are excluded from this plan.
- Type consistency: session statuses are `starting`, `running`, `exited`, `lost`, and `closed` across the store, protocol, agent registry, and web UI.
- Risk callout: the WebSocket HTTP handlers and full agent registration exchange are the highest-risk implementation sections. Keep them behind tests that use fake peers before adding browser or live agent traffic.
