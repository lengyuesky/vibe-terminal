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
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

type fakeOutputStore struct {
	chunks map[string][]byte
}

func (s fakeOutputStore) WriteChunk(sessionID string, startSeq int64, endSeq int64, data []byte) (string, int64, error) {
	return "", int64(len(data)), nil
}

func (s fakeOutputStore) ReadChunk(storagePath string) ([]byte, error) {
	return s.chunks[storagePath], nil
}

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

	registerReq := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"token":"`+tokenResp["token"]+`","name":"laptop","platform":"linux","agent_version":"0.1.0","fingerprint":"fp-1"}`))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRR := httptest.NewRecorder()
	router.ServeHTTP(registerRR, registerReq)
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
	var registerResp map[string]string
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registerResp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if registerResp["device_id"] == "" || registerResp["credential"] == "" {
		t.Fatalf("register response missing credentials: %#v", registerResp)
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"token":"`+tokenResp["token"]+`","name":"laptop","platform":"linux","agent_version":"0.1.0","fingerprint":"fp-1"}`))
	reuseRR := httptest.NewRecorder()
	router.ServeHTTP(reuseRR, reuseReq)
	if reuseRR.Code != http.StatusUnauthorized {
		t.Fatalf("reused token status = %d body=%s", reuseRR.Code, reuseRR.Body.String())
	}
}

func TestAgentTokenRevokeFlow(t *testing.T) {
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
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})

	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)
	cookies := loginRR.Result().Cookies()

	createReq := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", bytes.NewBufferString(`{"name":"desk","ttl_hours":24}`))
	createReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		createReq.AddCookie(cookie)
	}
	createRR := httptest.NewRecorder()
	handler.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRR.Code, createRR.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(createRR.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	deleteActiveReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created["id"]+"/permanent", nil)
	for _, cookie := range cookies {
		deleteActiveReq.AddCookie(cookie)
	}
	deleteActiveRR := httptest.NewRecorder()
	handler.ServeHTTP(deleteActiveRR, deleteActiveReq)
	if deleteActiveRR.Code != http.StatusConflict {
		t.Fatalf("delete active token status = %d body=%s", deleteActiveRR.Code, deleteActiveRR.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created["id"], nil)
	for _, cookie := range cookies {
		revokeReq.AddCookie(cookie)
	}
	revokeRR := httptest.NewRecorder()
	handler.ServeHTTP(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body=%s", revokeRR.Code, revokeRR.Body.String())
	}
	var revoked map[string]string
	if err := json.Unmarshal(revokeRR.Body.Bytes(), &revoked); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if revoked["revoked_at"] == "" {
		t.Fatalf("revoked response missing revoked_at: %#v", revoked)
	}
	if revoked["token"] != "" {
		t.Fatalf("revoke response must not include raw token: %#v", revoked)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created["id"]+"/permanent", nil)
	for _, cookie := range cookies {
		deleteReq.AddCookie(cookie)
	}
	deleteRR := httptest.NewRecorder()
	handler.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("delete revoked token status = %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/agent-tokens", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listRR := httptest.NewRecorder()
	handler.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listed []map[string]string
	if err := json.Unmarshal(listRR.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, token := range listed {
		if token["id"] == created["id"] {
			t.Fatalf("deleted token still listed: %#v", listed)
		}
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"token":"`+created["token"]+`","name":"desk","platform":"linux","agent_version":"0.1.0","fingerprint":"fp-revoked"}`))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, registerReq)
	if registerRR.Code != http.StatusUnauthorized {
		t.Fatalf("register with revoked token status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
}

func TestRevokeAgentTokenRequiresLoginAndHandlesMissingToken(t *testing.T) {
	db := testutil.NewStore(t)
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})

	unauthReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/missing", nil)
	unauthRR := httptest.NewRecorder()
	handler.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated revoke status = %d body=%s", unauthRR.Code, unauthRR.Body.String())
	}

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.CreateUser(context.Background(), store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)

	missingReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/missing", nil)
	for _, cookie := range loginRR.Result().Cookies() {
		missingReq.AddCookie(cookie)
	}
	missingRR := httptest.NewRecorder()
	handler.ServeHTTP(missingRR, missingReq)
	if missingRR.Code != http.StatusNotFound {
		t.Fatalf("missing revoke status = %d body=%s", missingRR.Code, missingRR.Body.String())
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
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(loginRR, loginReq)
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

func TestRenameDeviceUpdatesNameAndRejectsEmptyName(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
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
	_, err = db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)
	cookies := loginRR.Result().Cookies()

	emptyReq := httptest.NewRequest(http.MethodPatch, "/api/devices/dev-1", bytes.NewBufferString(`{"name":"   "}`))
	emptyReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		emptyReq.AddCookie(cookie)
	}
	emptyRR := httptest.NewRecorder()
	handler.ServeHTTP(emptyRR, emptyReq)
	if emptyRR.Code != http.StatusBadRequest {
		t.Fatalf("empty rename status = %d body=%s", emptyRR.Code, emptyRR.Body.String())
	}

	renameReq := httptest.NewRequest(http.MethodPatch, "/api/devices/dev-1", bytes.NewBufferString(`{"name":"office-laptop"}`))
	renameReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		renameReq.AddCookie(cookie)
	}
	renameRR := httptest.NewRecorder()
	handler.ServeHTTP(renameRR, renameReq)
	if renameRR.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", renameRR.Code, renameRR.Body.String())
	}
	var renamed map[string]any
	if err := json.Unmarshal(renameRR.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode rename response: %v", err)
	}
	if renamed["name"] != "office-laptop" || renamed["online"] != false {
		t.Fatalf("rename response = %#v", renamed)
	}
	device, err := db.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get renamed device: %v", err)
	}
	if device.Name != "office-laptop" {
		t.Fatalf("device name = %q, want office-laptop", device.Name)
	}
}

func TestSessionCloseIsHiddenFromListAndRenameUpdatesTitle(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
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
	_, err = db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = db.CreateTerminalSession(ctx, store.TerminalSession{
		ID: "sess-1", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash",
		WorkingDirectory: "/tmp", Status: store.SessionRunning,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})
	rt := handler.(*router)
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)
	cookies := loginRR.Result().Cookies()

	renameReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/sess-1", bytes.NewBufferString(`{"title":"project shell"}`))
	renameReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		renameReq.AddCookie(cookie)
	}
	renameRR := httptest.NewRecorder()
	handler.ServeHTTP(renameRR, renameReq)
	if renameRR.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", renameRR.Code, renameRR.Body.String())
	}
	session, err := db.GetTerminalSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Title != "project shell" {
		t.Fatalf("title = %q, want project shell", session.Title)
	}

	closeReq := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/close", nil)
	for _, cookie := range cookies {
		closeReq.AddCookie(cookie)
	}
	closeRR := httptest.NewRecorder()
	handler.ServeHTTP(closeRR, closeReq)
	if closeRR.Code != http.StatusOK {
		t.Fatalf("close status = %d body=%s", closeRR.Code, closeRR.Body.String())
	}
	exitPayload, err := json.Marshal(protocol.SessionExit{SessionID: "sess-1", ExitCode: 0, Message: "closed"})
	if err != nil {
		t.Fatalf("marshal session exit: %v", err)
	}
	rt.handleAgentEnvelope(ctx, "dev-1", protocol.Envelope{
		Type:      protocol.TypeSessionExit,
		SessionID: "sess-1",
		Payload:   exitPayload,
	}, wshub.NewMemoryPeer("agent"))
	closed, err := db.GetTerminalSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get closed session: %v", err)
	}
	if closed.Status != store.SessionClosed {
		t.Fatalf("closed session status after agent exit = %q, want closed", closed.Status)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/sessions", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listRR := httptest.NewRecorder()
	handler.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(listRR.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("closed sessions should be hidden, got %#v", listed)
	}
}

func TestSessionExitBroadcastsSessionStateToWebSubscribers(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
		ID: "dev-1", Name: "box", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: "hash", Authorized: true,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	_, err = db.CreateTerminalSession(ctx, store.TerminalSession{
		ID: "sess-1", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash",
		WorkingDirectory: "/tmp", Status: store.SessionRunning,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	hub := wshub.NewHub()
	web := wshub.NewMemoryPeer("web")
	hub.BindSession("sess-1", "dev-1")
	hub.SubscribeWeb("sess-1", web)
	r := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		Hub:      hub,
	}).(*router)
	exitPayload, err := json.Marshal(protocol.SessionExit{SessionID: "sess-1", ExitCode: 0, Message: "exited"})
	if err != nil {
		t.Fatalf("marshal session exit: %v", err)
	}

	r.handleAgentEnvelope(ctx, "dev-1", protocol.Envelope{
		Type:      protocol.TypeSessionExit,
		SessionID: "sess-1",
		Payload:   exitPayload,
	}, wshub.NewMemoryPeer("agent"))

	got := web.Pop()
	if got.Type != protocol.TypeSessionState {
		t.Fatalf("broadcast type = %q, want %q", got.Type, protocol.TypeSessionState)
	}
	state, ok := got.Payload.(protocol.SessionState)
	if !ok {
		t.Fatalf("broadcast payload = %#v, want protocol.SessionState", got.Payload)
	}
	if state.SessionID != "sess-1" || state.Status != store.SessionExited || state.Message != "exited" {
		t.Fatalf("broadcast state = %#v", state)
	}
}

func TestStdoutFromStartingSessionMarksSessionRunning(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
		ID: "dev-1", Name: "box", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: "hash", Authorized: true,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	_, err = db.CreateTerminalSession(ctx, store.TerminalSession{
		ID: "sess-1", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash",
		WorkingDirectory: "/tmp", Status: store.SessionStarting,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	r := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	}).(*router)
	stdoutPayload, err := json.Marshal(protocol.Stdout{SessionID: "sess-1", Seq: 3, Data: "prompt"})
	if err != nil {
		t.Fatalf("marshal stdout: %v", err)
	}

	r.handleAgentEnvelope(ctx, "dev-1", protocol.Envelope{
		Type:      protocol.TypeStdout,
		SessionID: "sess-1",
		Payload:   stdoutPayload,
	}, wshub.NewMemoryPeer("agent"))

	session, err := db.GetTerminalSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Status != store.SessionRunning || session.LastOutputSeq != 3 {
		t.Fatalf("session after stdout = %#v", session)
	}
}

func TestSessionOutputIncludesPersistedData(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
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
	_, err = db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = db.CreateTerminalSession(ctx, store.TerminalSession{
		ID: "sess-1", DeviceID: "dev-1", Title: "claude", ShellPath: "/bin/bash",
		WorkingDirectory: "/tmp", Status: store.SessionRunning,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = db.CreateOutputChunk(ctx, store.OutputChunk{
		ID: "chunk-1", SessionID: "sess-1", StartSeq: 1, EndSeq: 1,
		StoragePath: "sessions/sess-1/000000000001-000000000001.log", ByteSize: 14,
	})
	if err != nil {
		t.Fatalf("create output chunk: %v", err)
	}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		Output: fakeOutputStore{chunks: map[string][]byte{
			"sessions/sess-1/000000000001-000000000001.log": []byte("Claude prompt\r\n"),
		}},
	})
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(loginRR, loginReq)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/output", nil)
	for _, cookie := range loginRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("output status = %d body=%s", rr.Code, rr.Body.String())
	}
	var chunks []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &chunks); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(chunks) != 1 || chunks[0]["data"] != "Claude prompt\r\n" {
		t.Fatalf("output should include persisted data, got %#v", chunks)
	}
}

func TestSyncAgentSessionsMarksMissingRunningSessionsLost(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
		ID: "dev-1", Name: "box", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: "hash", Authorized: true,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	for _, session := range []store.TerminalSession{
		{ID: "kept", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionRunning, AgentPID: 100, LastOutputSeq: 7},
		{ID: "missing-running", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionRunning, AgentPID: 101, LastOutputSeq: 8},
		{ID: "missing-starting", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionStarting, AgentPID: 0, LastOutputSeq: 0},
		{ID: "closed", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionClosed, AgentPID: 0, LastOutputSeq: 0},
	} {
		if _, err := db.CreateTerminalSession(ctx, session); err != nil {
			t.Fatalf("create session %s: %v", session.ID, err)
		}
	}
	r := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	}).(*router)

	r.syncAgentSessions(ctx, "dev-1", []protocol.SessionSummary{
		{SessionID: "kept", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionRunning, AgentPID: 200, LastOutputSeq: 9},
		{SessionID: "closed", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionRunning, AgentPID: 201, LastOutputSeq: 10},
	})

	kept, err := db.GetTerminalSession(ctx, "kept")
	if err != nil {
		t.Fatalf("get kept: %v", err)
	}
	if kept.Status != store.SessionRunning || kept.AgentPID != 200 || kept.LastOutputSeq != 9 {
		t.Fatalf("kept session not synced: %#v", kept)
	}
	for _, id := range []string{"missing-running", "missing-starting"} {
		session, err := db.GetTerminalSession(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if session.Status != store.SessionLost {
			t.Fatalf("%s status = %q, want lost", id, session.Status)
		}
	}
	closed, err := db.GetTerminalSession(ctx, "closed")
	if err != nil {
		t.Fatalf("get closed: %v", err)
	}
	if closed.Status != store.SessionClosed {
		t.Fatalf("closed status = %q, want closed", closed.Status)
	}
}

func TestAgentDisconnectMarksRunningSessionsLost(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	_, err := db.CreateDevice(ctx, store.Device{
		ID: "dev-1", Name: "box", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: "hash", Authorized: true,
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	for _, session := range []store.TerminalSession{
		{ID: "running", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionRunning, AgentPID: 100, LastOutputSeq: 7},
		{ID: "starting", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionStarting, AgentPID: 0, LastOutputSeq: 0},
		{ID: "exited", DeviceID: "dev-1", Title: "shell", ShellPath: "/bin/bash", WorkingDirectory: "/tmp", Status: store.SessionExited, AgentPID: 101, LastOutputSeq: 8},
	} {
		if _, err := db.CreateTerminalSession(ctx, session); err != nil {
			t.Fatalf("create session %s: %v", session.ID, err)
		}
	}
	r := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	}).(*router)

	r.markDisconnectedSessionsLost(ctx, "dev-1")

	for _, id := range []string{"running", "starting"} {
		session, err := db.GetTerminalSession(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if session.Status != store.SessionLost {
			t.Fatalf("%s status = %q, want lost", id, session.Status)
		}
	}
	exited, err := db.GetTerminalSession(ctx, "exited")
	if err != nil {
		t.Fatalf("get exited: %v", err)
	}
	if exited.Status != store.SessionExited {
		t.Fatalf("exited status = %q, want exited", exited.Status)
	}
}
