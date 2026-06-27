package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/audit"
	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/devices"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/terminal"
)

type Deps struct {
	Store    *store.DB
	Sessions *auth.SessionManager
	Presence *devices.Presence
	Audit    audit.Writer
}

type router struct {
	store    *store.DB
	sessions *auth.SessionManager
	presence *devices.Presence
	audit    audit.Writer
	mux      *http.ServeMux
}

func NewRouter(deps Deps) http.Handler {
	if deps.Presence == nil {
		deps.Presence = devices.NewPresence()
	}
	if deps.Audit.Store == nil {
		deps.Audit = audit.Writer{Store: deps.Store}
	}
	r := &router{
		store:    deps.Store,
		sessions: deps.Sessions,
		presence: deps.Presence,
		audit:    deps.Audit,
		mux:      http.NewServeMux(),
	}
	r.routes()
	return r
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *router) routes() {
	r.mux.HandleFunc("POST /api/login", r.handleLogin)
	r.mux.HandleFunc("POST /api/logout", r.handleLogout)
	r.mux.HandleFunc("GET /api/me", r.handleMe)
	r.mux.HandleFunc("POST /api/agent-tokens", r.handleCreateAgentToken)
	r.mux.HandleFunc("GET /api/agent-tokens", r.handleListAgentTokens)
	r.mux.HandleFunc("POST /api/agents/register", r.handleAgentRegister)
	r.mux.HandleFunc("GET /api/devices", r.handleListDevices)
	r.mux.HandleFunc("/api/devices/", r.handleDeviceRoutes)
	r.mux.HandleFunc("/api/sessions/", r.handleSessionRoutes)
}

func (r *router) handleLogin(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	user, err := r.store.GetUserByUsername(req.Context(), body.Username)
	if err != nil || !auth.CheckPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	if err := r.sessions.Set(w, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to create session")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		EventType: "login",
		Summary:   "administrator logged in",
	})
	writeJSON(w, http.StatusOK, userResponse(user))
}

func (r *router) handleLogout(w http.ResponseWriter, req *http.Request) {
	r.sessions.Clear(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (r *router) handleMe(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, userResponse(user))
}

func (r *router) handleCreateAgentToken(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	if body.Name == "" {
		body.Name = "agent"
	}
	if body.TTLHours <= 0 {
		body.TTLHours = 24
	}
	rawToken, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to create token")
		return
	}
	token, err := r.store.CreateAgentToken(req.Context(), store.CreateAgentTokenParams{
		ID:        uuid.NewString(),
		Name:      body.Name,
		TokenHash: hashSecret(rawToken),
		ExpiresAt: time.Now().UTC().Add(time.Duration(body.TTLHours) * time.Hour),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to save token")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		EventType: "agent_token_created",
		Summary:   "agent registration token created",
	})
	writeJSON(w, http.StatusCreated, map[string]string{
		"id":         token.ID,
		"name":       token.Name,
		"expires_at": token.ExpiresAt.Format(time.RFC3339),
		"token":      rawToken,
	})
}

func (r *router) handleListAgentTokens(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	tokens, err := r.store.ListAgentTokens(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to list tokens")
		return
	}
	type tokenResponse struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
		UsedAt    string `json:"used_at,omitempty"`
		RevokedAt string `json:"revoked_at,omitempty"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		resp := tokenResponse{
			ID:        token.ID,
			Name:      token.Name,
			ExpiresAt: token.ExpiresAt.Format(time.RFC3339),
			CreatedAt: token.CreatedAt.Format(time.RFC3339),
		}
		if token.UsedAt.Valid {
			resp.UsedAt = token.UsedAt.Time.Format(time.RFC3339)
		}
		if token.RevokedAt.Valid {
			resp.RevokedAt = token.RevokedAt.Time.Format(time.RFC3339)
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleAgentRegister(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Token        string `json:"token"`
		Name         string `json:"name"`
		Platform     string `json:"platform"`
		AgentVersion string `json:"agent_version"`
		Fingerprint  string `json:"fingerprint"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	if body.Token == "" || body.Platform == "" || body.Fingerprint == "" {
		writeError(w, http.StatusBadRequest, "invalid_registration", "token, platform, and fingerprint are required")
		return
	}
	if body.Name == "" {
		body.Name = body.Platform + "-agent"
	}
	if body.AgentVersion == "" {
		body.AgentVersion = "unknown"
	}
	if _, err := r.store.UseAgentTokenByHash(req.Context(), hashSecret(body.Token), time.Now().UTC()); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "registration token is invalid")
		return
	}
	credential, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_error", "failed to create credential")
		return
	}
	device, err := r.store.CreateDevice(req.Context(), store.Device{
		ID:             uuid.NewString(),
		Name:           body.Name,
		Platform:       body.Platform,
		AgentVersion:   body.AgentVersion,
		Fingerprint:    body.Fingerprint,
		CredentialHash: hashSecret(credential),
		Authorized:     true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "device_error", "failed to create device")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		DeviceID:  device.ID,
		EventType: "device_registered",
		Summary:   "agent registered a device",
	})
	writeJSON(w, http.StatusCreated, map[string]string{
		"device_id":  device.ID,
		"credential": credential,
	})
}

func (r *router) handleListDevices(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	devices, err := r.store.ListDevices(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "device_error", "failed to list devices")
		return
	}
	type deviceResponse struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Platform     string `json:"platform"`
		AgentVersion string `json:"agent_version"`
		Online       bool   `json:"online"`
	}
	out := make([]deviceResponse, 0, len(devices))
	for _, device := range devices {
		out = append(out, deviceResponse{
			ID:           device.ID,
			Name:         device.Name,
			Platform:     device.Platform,
			AgentVersion: device.AgentVersion,
			Online:       r.presence.Online(device.ID),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleDeviceRoutes(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/devices/")
	deviceID, suffix, ok := strings.Cut(rest, "/")
	if !ok || suffix != "sessions" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch req.Method {
	case http.MethodPost:
		r.handleCreateSession(w, req, deviceID)
	case http.MethodGet:
		r.handleListSessions(w, req, deviceID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (r *router) handleCreateSession(w http.ResponseWriter, req *http.Request, deviceID string) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	if !r.presence.Online(deviceID) {
		writeError(w, http.StatusConflict, "device_offline", "device is offline")
		return
	}
	var body terminal.CreateRequest
	if !readJSON(w, req, &body) {
		return
	}
	body = terminal.NormalizeCreateRequest(body)
	session, err := r.store.CreateTerminalSession(req.Context(), store.TerminalSession{
		ID:               uuid.NewString(),
		DeviceID:         deviceID,
		Title:            "shell",
		ShellPath:        body.ShellPath,
		WorkingDirectory: body.WorkingDirectory,
		Status:           store.SessionStarting,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to create session")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		DeviceID:  deviceID,
		SessionID: session.ID,
		EventType: "session_created",
		Summary:   "terminal session created",
	})
	writeJSON(w, http.StatusCreated, sessionResponse(session))
}

func (r *router) handleListSessions(w http.ResponseWriter, req *http.Request, deviceID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	sessions, err := r.store.ListTerminalSessionsForDevice(req.Context(), deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to list sessions")
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionResponse(session))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleSessionRoutes(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/sessions/")
	sessionID, suffix, ok := strings.Cut(rest, "/")
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch {
	case req.Method == http.MethodPost && suffix == "close":
		r.handleCloseSession(w, req, sessionID)
	case req.Method == http.MethodGet && suffix == "output":
		r.handleSessionOutput(w, req, sessionID)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (r *router) handleCloseSession(w http.ResponseWriter, req *http.Request, sessionID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	if err := r.store.UpdateTerminalSessionStatus(req.Context(), sessionID, store.SessionClosed, 0, 0); err != nil {
		writeStoreError(w, err, "session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (r *router) handleSessionOutput(w http.ResponseWriter, req *http.Request, sessionID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	chunks, err := r.store.ListOutputChunks(req.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "output_error", "failed to list output")
		return
	}
	out := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, map[string]any{
			"id":           chunk.ID,
			"session_id":   chunk.SessionID,
			"start_seq":    chunk.StartSeq,
			"end_seq":      chunk.EndSeq,
			"storage_path": chunk.StoragePath,
			"byte_size":    chunk.ByteSize,
			"created_at":   chunk.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) requireUser(w http.ResponseWriter, req *http.Request) (store.User, bool) {
	userID, err := r.sessions.Get(req)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
		return store.User{}, false
	}
	user, err := r.store.GetUserByID(req.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
		return store.User{}, false
	}
	return user, true
}

func readJSON(w http.ResponseWriter, req *http.Request, dest any) bool {
	defer req.Body.Close()
	if err := json.NewDecoder(req.Body).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]string{
		"code":    code,
		"message": message,
	})
}

func writeStoreError(w http.ResponseWriter, err error, resource string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, resource+"_not_found", resource+" not found")
		return
	}
	writeError(w, http.StatusInternalServerError, resource+"_error", "failed to access "+resource)
}

func userResponse(user store.User) map[string]string {
	return map[string]string{
		"id":       user.ID,
		"username": user.Username,
	}
}

func sessionResponse(session store.TerminalSession) map[string]any {
	return map[string]any{
		"id":                session.ID,
		"device_id":         session.DeviceID,
		"title":             session.Title,
		"shell_path":        session.ShellPath,
		"working_directory": session.WorkingDirectory,
		"status":            session.Status,
		"agent_pid":         session.AgentPID,
		"last_output_seq":   session.LastOutputSeq,
	}
}

func randomToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
