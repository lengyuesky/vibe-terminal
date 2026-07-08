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

func newSnippetRouter(t *testing.T) (http.Handler, []*http.Cookie) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginRR.Code)
	}
	return router, loginRR.Result().Cookies()
}

func doSnippetRequest(t *testing.T, router http.Handler, cookies []*http.Cookie, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestSnippetCRUDFlow(t *testing.T) {
	router, cookies := newSnippetRouter(t)

	rr := doSnippetRequest(t, router, cookies, http.MethodGet, "/api/snippets", "")
	if rr.Code != http.StatusOK || rr.Body.String() != "[]\n" {
		t.Fatalf("empty list status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"disk","command":"df -h"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created["id"] == "" || created["command"] != "df -h" {
		t.Fatalf("created = %#v", created)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodPut, "/api/snippets/"+created["id"], `{"name":"disk usage","command":"df -h /"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodGet, "/api/snippets", "")
	var list []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0]["name"] != "disk usage" {
		t.Fatalf("list = %#v", list)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodDelete, "/api/snippets/"+created["id"], "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rr.Code)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodDelete, "/api/snippets/"+created["id"], "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d", rr.Code)
	}
}

func TestSnippetValidationAndAuth(t *testing.T) {
	router, cookies := newSnippetRouter(t)

	rr := doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"  ","command":"ls"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank name status=%d", rr.Code)
	}
	rr = doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"ls","command":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank command status=%d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/snippets", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", rr.Code)
	}
}
