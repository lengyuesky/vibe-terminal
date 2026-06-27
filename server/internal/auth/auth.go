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
