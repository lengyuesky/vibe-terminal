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
