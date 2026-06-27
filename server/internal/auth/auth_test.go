package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Fatal("password should match")
	}
	if CheckPassword(hash, "wrong") {
		t.Fatal("wrong password should not match")
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	manager := NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	rr := httptest.NewRecorder()
	if err := manager.Set(rr, "user-1"); err != nil {
		t.Fatalf("set session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, cookie := range rr.Result().Cookies() {
		req.AddCookie(cookie)
	}
	userID, err := manager.Get(req)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userID = %q", userID)
	}
}
