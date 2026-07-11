package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/audit"
	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/devices"
	"github.com/djy/vibe-terminal/server/internal/files"
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/terminal"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

type FsService interface {
	List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error)
	Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error
	Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error
	HandleAgentResponse(env protocol.Envelope) bool
}

type AuditWriter interface {
	Log(ctx context.Context, event store.AuditEvent) error
}

type Deps struct {
	Store             *store.DB
	Sessions          *auth.SessionManager
	TwoFactor         *auth.TwoFactorManager
	PasswordLimiter   *auth.FailureLimiter
	TwoFactorLimiter  *auth.FailureLimiter
	ManagementLimiter *auth.FailureLimiter
	Now               func() time.Time
	Presence          *devices.Presence
	Audit             AuditWriter
	Hub               *wshub.Hub
	Output            terminal.OutputStore
	StaticFiles       http.FileSystem
	Files             FsService
	FsMaxUploadSize   int64
}

type router struct {
	store                      *store.DB
	sessions                   *auth.SessionManager
	twoFactor                  *auth.TwoFactorManager
	passwordLimiter            *auth.FailureLimiter
	twoFactorLimiter           *auth.FailureLimiter
	managementLimiter          *auth.FailureLimiter
	managementAuthMu           sync.Mutex
	beforePendingTwoFactorSave func()
	now                        func() time.Time
	presence                   *devices.Presence
	audit                      AuditWriter
	hub                        *wshub.Hub
	output                     terminal.OutputStore
	static                     http.FileSystem
	files                      FsService
	fsMaxUpload                int64
	mux                        *http.ServeMux
}

func NewRouter(deps Deps) http.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.PasswordLimiter == nil {
		deps.PasswordLimiter = auth.NewFailureLimiter(5, 10*time.Minute, 15*time.Minute, 5000, deps.Now)
	}
	if deps.TwoFactorLimiter == nil {
		deps.TwoFactorLimiter = auth.NewFailureLimiter(5, 5*time.Minute, 15*time.Minute, 5000, deps.Now)
	}
	if deps.ManagementLimiter == nil {
		deps.ManagementLimiter = auth.NewFailureLimiter(5, 10*time.Minute, 15*time.Minute, 5000, deps.Now)
	}
	if deps.Presence == nil {
		deps.Presence = devices.NewPresence()
	}
	if deps.Audit == nil {
		deps.Audit = audit.Writer{Store: deps.Store}
	}
	if deps.Hub == nil {
		deps.Hub = wshub.NewHub()
	}
	if deps.Files == nil {
		deps.Files = files.NewService(deps.Hub, deps.Presence)
	}
	if deps.FsMaxUploadSize <= 0 {
		deps.FsMaxUploadSize = 512 << 20
	}
	r := &router{
		store:             deps.Store,
		sessions:          deps.Sessions,
		twoFactor:         deps.TwoFactor,
		passwordLimiter:   deps.PasswordLimiter,
		twoFactorLimiter:  deps.TwoFactorLimiter,
		managementLimiter: deps.ManagementLimiter,
		now:               deps.Now,
		presence:          deps.Presence,
		audit:             deps.Audit,
		hub:               deps.Hub,
		output:            deps.Output,
		static:            deps.StaticFiles,
		files:             deps.Files,
		fsMaxUpload:       deps.FsMaxUploadSize,
		mux:               http.NewServeMux(),
	}
	r.routes()
	return r
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *router) routes() {
	r.mux.HandleFunc("POST /api/login", r.handleLogin)
	r.mux.HandleFunc("POST /api/login/2fa", r.handleLoginTwoFactor)
	r.mux.HandleFunc("GET /api/security/2fa", r.handleTwoFactorStatus)
	r.mux.HandleFunc("POST /api/security/2fa/setup", r.handleTwoFactorSetup)
	r.mux.HandleFunc("POST /api/security/2fa/enable", r.handleTwoFactorEnable)
	r.mux.HandleFunc("POST /api/security/2fa/recovery-codes", r.handleTwoFactorRecoveryCodes)
	r.mux.HandleFunc("POST /api/security/2fa/disable", r.handleTwoFactorDisable)
	r.mux.HandleFunc("POST /api/logout", r.handleLogout)
	r.mux.HandleFunc("GET /api/me", r.handleMe)
	r.mux.HandleFunc("POST /api/agent-tokens", r.handleCreateAgentToken)
	r.mux.HandleFunc("GET /api/agent-tokens", r.handleListAgentTokens)
	r.mux.HandleFunc("DELETE /api/agent-tokens/", r.handleRevokeAgentToken)
	r.mux.HandleFunc("GET /api/snippets", r.handleListSnippets)
	r.mux.HandleFunc("POST /api/snippets", r.handleCreateSnippet)
	r.mux.HandleFunc("PUT /api/snippets/", r.handleUpdateSnippet)
	r.mux.HandleFunc("DELETE /api/snippets/", r.handleDeleteSnippet)
	r.mux.HandleFunc("POST /api/agents/register", r.handleAgentRegister)
	r.mux.HandleFunc("GET /api/devices", r.handleListDevices)
	r.mux.HandleFunc("/api/devices/", r.handleDeviceRoutes)
	r.mux.HandleFunc("/api/sessions/", r.handleSessionRoutes)
	r.mux.HandleFunc("GET /ws/agent", r.handleAgentWebSocket)
	r.mux.HandleFunc("GET /ws/web", r.handleWebWebSocket)
	if r.static != nil {
		r.mux.Handle("/", http.FileServer(r.static))
	}
}

func (r *router) handleLogin(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readLoginJSON(w, req, &body) {
		return
	}
	ip := requestIP(req)
	limitKeys := passwordLoginLimitKeys(body.Username, ip)
	if allowed, retryAfter := allowLoginAttempt(r.passwordLimiter, limitKeys); !allowed {
		writeRateLimit(w, retryAfter)
		return
	}
	user, err := r.store.GetUserByUsername(req.Context(), body.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	if errors.Is(err, store.ErrNotFound) || !auth.CheckPassword(user.PasswordHash, body.Password) {
		if blocked, retryAfter := recordLoginFailure(r.passwordLimiter, limitKeys); blocked {
			r.auditLoginRateLimit(req.Context(), user.ID, "password", ip)
			writeRateLimit(w, retryAfter)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	clearLoginFailures(r.passwordLimiter, limitKeys)

	setting, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		r.completeLogin(w, req, user, "password")
		return
	}
	if err != nil || r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	challenge, err := r.twoFactor.IssueLoginChallenge(user.ID, setting.ConfigurationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	issuedChallenge, err := r.twoFactor.VerifyLoginChallenge(challenge)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if err := r.store.CreateLoginChallenge(req.Context(), store.LoginChallenge{
		JTI:             issuedChallenge.JTI,
		UserID:          issuedChallenge.UserID,
		ConfigurationID: issuedChallenge.ConfigurationID,
		ExpiresAt:       issuedChallenge.ExpiresAt,
		CreatedAt:       issuedChallenge.IssuedAt,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"two_factor_required": true,
		"challenge_token":     challenge,
		"expires_in":          300,
	})
}

func (r *router) completeLogin(w http.ResponseWriter, req *http.Request, user store.User, method string) {
	event, err := loginAuditEvent(user.ID, method, r.now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to create session")
		return
	}
	if err := r.audit.Log(req.Context(), event); err != nil {
		writeError(w, http.StatusInternalServerError, "audit_unavailable", "failed to record login audit")
		return
	}
	r.finishLogin(w, user)
}

func loginAuditEvent(userID, method string, now time.Time) (store.AuditEvent, error) {
	metadataJSON, err := json.Marshal(map[string]string{"method": method})
	if err != nil {
		return store.AuditEvent{}, err
	}
	return store.AuditEvent{
		ID:           uuid.NewString(),
		UserID:       userID,
		EventType:    "login",
		Summary:      "administrator logged in",
		MetadataJSON: string(metadataJSON),
		CreatedAt:    now,
	}, nil
}

func (r *router) finishLogin(w http.ResponseWriter, user store.User) {
	if err := r.sessions.Set(w, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to create session")
		return
	}
	writeJSON(w, http.StatusOK, userResponse(user))
}

func (r *router) auditLoginRateLimit(ctx context.Context, userID, stage, sourceIP string) {
	metadataJSON, err := json.Marshal(map[string]string{
		"stage":     stage,
		"source_ip": sourceIP,
	})
	if err != nil {
		return
	}
	_ = r.audit.Log(ctx, store.AuditEvent{
		UserID:       userID,
		EventType:    "login_rate_limited",
		Summary:      "login attempts rate limited",
		MetadataJSON: string(metadataJSON),
	})
}

func (r *router) auditManagementRateLimit(ctx context.Context, userID, stage, sourceIP string) {
	metadataJSON, err := json.Marshal(map[string]string{"stage": stage, "source_ip": sourceIP})
	if err != nil {
		return
	}
	_ = r.audit.Log(ctx, store.AuditEvent{
		UserID: userID, EventType: "management_reauthentication_rate_limited",
		Summary: "management reauthentication attempts rate limited", MetadataJSON: string(metadataJSON),
	})
}

func requestIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func writeRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many login attempts")
}

func passwordLoginLimitKeys(username, sourceIP string) []string {
	account := strings.ToLower(strings.TrimSpace(username))
	return []string{
		"account|" + account,
		"source|" + sourceIP,
		"account_source|" + account + "|" + sourceIP,
	}
}

func allowLoginAttempt(limiter *auth.FailureLimiter, keys []string) (bool, time.Duration) {
	var longestRetry time.Duration
	allowed := true
	for _, key := range keys {
		keyAllowed, retryAfter := limiter.Allow(key)
		if !keyAllowed {
			allowed = false
			if retryAfter > longestRetry {
				longestRetry = retryAfter
			}
		}
	}
	return allowed, longestRetry
}

func recordLoginFailure(limiter *auth.FailureLimiter, keys []string) (bool, time.Duration) {
	var longestRetry time.Duration
	blocked := false
	for _, key := range keys {
		keyBlocked, retryAfter := limiter.RecordFailure(key)
		if keyBlocked {
			blocked = true
			if retryAfter > longestRetry {
				longestRetry = retryAfter
			}
		}
	}
	return blocked, longestRetry
}

func clearLoginFailures(limiter *auth.FailureLimiter, keys []string) {
	for _, key := range keys {
		limiter.Success(key)
	}
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
	resp := agentTokenToResponse(token)
	writeJSON(w, http.StatusCreated, struct {
		agentTokenResponse
		Token string `json:"token"`
	}{
		agentTokenResponse: resp,
		Token:              rawToken,
	})
}

type agentTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
	UsedAt    string `json:"used_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func agentTokenToResponse(token store.AgentToken) agentTokenResponse {
	resp := agentTokenResponse{
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
	return resp
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
	out := make([]agentTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, agentTokenToResponse(token))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleRevokeAgentToken(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/agent-tokens/")
	if strings.HasSuffix(id, "/permanent") {
		r.handleDeleteRevokedAgentToken(w, req, user, strings.TrimSuffix(id, "/permanent"))
		return
	}
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	token, err := r.store.RevokeAgentToken(req.Context(), id, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to revoke token")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		EventType: "agent_token_revoked",
		Summary:   "agent registration token revoked",
	})
	writeJSON(w, http.StatusOK, agentTokenToResponse(token))
}

func (r *router) handleDeleteRevokedAgentToken(w http.ResponseWriter, req *http.Request, user store.User, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	err := r.store.DeleteRevokedAgentToken(req.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "token_active", "agent token must be revoked before permanent deletion")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to delete token")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		EventType: "agent_token_deleted",
		Summary:   "agent registration token permanently deleted",
	})
	w.WriteHeader(http.StatusNoContent)
}

type snippetBody struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func (b *snippetBody) normalize() bool {
	b.Name = strings.TrimSpace(b.Name)
	return b.Name != "" && strings.TrimSpace(b.Command) != ""
}

func snippetResponse(snippet store.CommandSnippet) map[string]string {
	return map[string]string{
		"id":         snippet.ID,
		"name":       snippet.Name,
		"command":    snippet.Command,
		"created_at": snippet.CreatedAt.Format(time.RFC3339),
		"updated_at": snippet.UpdatedAt.Format(time.RFC3339),
	}
}

func snippetIDFromPath(path string) string {
	id := strings.TrimPrefix(path, "/api/snippets/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func (r *router) handleListSnippets(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	snippets, err := r.store.ListCommandSnippets(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snippet_error", "failed to list snippets")
		return
	}
	out := make([]map[string]string, 0, len(snippets))
	for _, snippet := range snippets {
		out = append(out, snippetResponse(snippet))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleCreateSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	var body snippetBody
	if !readJSON(w, req, &body) {
		return
	}
	if !body.normalize() {
		writeError(w, http.StatusBadRequest, "invalid_snippet", "name and command are required")
		return
	}
	snippet, err := r.store.CreateCommandSnippet(req.Context(), store.CommandSnippet{
		ID:      uuid.NewString(),
		Name:    body.Name,
		Command: body.Command,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snippet_error", "failed to create snippet")
		return
	}
	writeJSON(w, http.StatusCreated, snippetResponse(snippet))
}

func (r *router) handleUpdateSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	id := snippetIDFromPath(req.URL.Path)
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "snippet not found")
		return
	}
	var body snippetBody
	if !readJSON(w, req, &body) {
		return
	}
	if !body.normalize() {
		writeError(w, http.StatusBadRequest, "invalid_snippet", "name and command are required")
		return
	}
	snippet, err := r.store.UpdateCommandSnippet(req.Context(), id, body.Name, body.Command)
	if err != nil {
		writeStoreError(w, err, "snippet")
		return
	}
	writeJSON(w, http.StatusOK, snippetResponse(snippet))
}

func (r *router) handleDeleteSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	id := snippetIDFromPath(req.URL.Path)
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "snippet not found")
		return
	}
	if err := r.store.DeleteCommandSnippet(req.Context(), id); err != nil {
		writeStoreError(w, err, "snippet")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	out := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		out = append(out, deviceResponse(device, r.presence.Online(device.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleDeviceRoutes(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/devices/")
	deviceID, suffix, ok := strings.Cut(rest, "/")
	if deviceID == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if !ok || suffix == "" {
		if req.Method == http.MethodPatch {
			r.handleRenameDevice(w, req, deviceID)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch {
	case suffix == "sessions" && req.Method == http.MethodPost:
		r.handleCreateSession(w, req, deviceID)
	case suffix == "sessions" && req.Method == http.MethodGet:
		r.handleListSessions(w, req, deviceID)
	case suffix == "sessions":
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	case suffix == "fs" && req.Method == http.MethodGet:
		r.handleFsList(w, req, deviceID)
	case suffix == "fs/file" && req.Method == http.MethodGet:
		r.handleFsDownload(w, req, deviceID)
	case suffix == "fs/file" && req.Method == http.MethodPost:
		r.handleFsUpload(w, req, deviceID)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (r *router) handleRenameDevice(w http.ResponseWriter, req *http.Request, deviceID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}
	if err := r.store.UpdateDeviceName(req.Context(), deviceID, name); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	device, err := r.store.GetDevice(req.Context(), deviceID)
	if err != nil {
		writeStoreError(w, err, "device")
		return
	}
	writeJSON(w, http.StatusOK, deviceResponse(device, r.presence.Online(device.ID)))
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
	r.hub.BindSession(session.ID, deviceID)
	if err := r.hub.FromWeb(protocol.StartSession{
		SessionID:        session.ID,
		ShellPath:        body.ShellPath,
		WorkingDirectory: body.WorkingDirectory,
		Cols:             body.Cols,
		Rows:             body.Rows,
	}); err != nil {
		_ = r.store.UpdateTerminalSessionStatus(req.Context(), session.ID, store.SessionLost, 0, 0)
		writeError(w, http.StatusConflict, "agent_unavailable", "agent is not ready for this session")
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

func (r *router) handleFsList(w http.ResponseWriter, req *http.Request, deviceID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	result, err := r.files.List(req.Context(), deviceID, path)
	if err != nil {
		writeFsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *router) handleFsDownload(w http.ResponseWriter, req *http.Request, deviceID string) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	filename := path[strings.LastIndex(path, "/")+1:]
	var fileSize int64
	wroteHeader := false
	err := r.files.Download(req.Context(), deviceID, path, func(size int64) {
		fileSize = size
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		wroteHeader = true
	}, w)
	if err != nil {
		if !wroteHeader {
			writeFsError(w, err)
		}
		// 流已开始时中断：连接直接断开，客户端按 Content-Length 检测到截断。
		return
	}
	metadata, _ := json.Marshal(map[string]any{"path": path, "bytes": fileSize})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:       user.ID,
		DeviceID:     deviceID,
		EventType:    "file_download",
		Summary:      "file downloaded from device",
		MetadataJSON: string(metadata),
	})
}

func (r *router) handleFsUpload(w http.ResponseWriter, req *http.Request, deviceID string) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	overwrite := req.URL.Query().Get("overwrite") == "true"
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	if req.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if req.ContentLength > r.fsMaxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds upload size limit")
		return
	}
	body := http.MaxBytesReader(w, req.Body, r.fsMaxUpload)
	defer body.Close()
	if err := r.files.Upload(req.Context(), deviceID, path, req.ContentLength, overwrite, body); err != nil {
		writeFsError(w, err)
		return
	}
	metadata, _ := json.Marshal(map[string]any{"path": path, "bytes": req.ContentLength})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:       user.ID,
		DeviceID:     deviceID,
		EventType:    "file_upload",
		Summary:      "file uploaded to device",
		MetadataJSON: string(metadata),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"path": path, "size": req.ContentLength})
}

func writeFsError(w http.ResponseWriter, err error) {
	var opErr *files.OpError
	if !errors.As(err, &opErr) {
		writeError(w, http.StatusInternalServerError, "fs_error", "file operation failed")
		return
	}
	status := http.StatusInternalServerError
	switch opErr.Code {
	case files.CodeAgentOffline, files.CodeAgentUnsupported:
		status = http.StatusServiceUnavailable
	case "not_found":
		status = http.StatusNotFound
	case "permission_denied":
		status = http.StatusForbidden
	case "not_a_file", "not_a_directory", "invalid_path", "invalid_request":
		status = http.StatusBadRequest
	case "already_exists":
		status = http.StatusConflict
	case files.CodeTimeout:
		status = http.StatusGatewayTimeout
	case files.CodeBusy:
		status = http.StatusTooManyRequests
	}
	writeError(w, status, opErr.Code, opErr.Message)
}

func (r *router) handleSessionRoutes(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/sessions/")
	sessionID, suffix, _ := strings.Cut(rest, "/")
	if sessionID == "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch {
	case req.Method == http.MethodPatch && suffix == "":
		r.handleRenameSession(w, req, sessionID)
	case req.Method == http.MethodPost && suffix == "close":
		r.handleCloseSession(w, req, sessionID)
	case req.Method == http.MethodGet && suffix == "output":
		r.handleSessionOutput(w, req, sessionID)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (r *router) handleRenameSession(w http.ResponseWriter, req *http.Request, sessionID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "invalid_title", "title is required")
		return
	}
	if err := r.store.UpdateTerminalSessionTitle(req.Context(), sessionID, title); err != nil {
		writeStoreError(w, err, "session")
		return
	}
	session, err := r.store.GetTerminalSession(req.Context(), sessionID)
	if err != nil {
		writeStoreError(w, err, "session")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(session))
}

func (r *router) handleCloseSession(w http.ResponseWriter, req *http.Request, sessionID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	if err := r.store.UpdateTerminalSessionStatus(req.Context(), sessionID, store.SessionClosed, 0, 0); err != nil {
		writeStoreError(w, err, "session")
		return
	}
	_ = r.hub.FromWeb(protocol.CloseSession{SessionID: sessionID})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (r *router) handleAgentWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	// fs_read_result 分块 base64 后约 350KB，超过默认 32KB 读上限会断开整个 agent 连接
	conn.SetReadLimit(1 << 20)

	ctx := req.Context()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	env, err := protocol.DecodeEnvelope(data)
	if err != nil || env.Type != protocol.TypeAgentHello {
		conn.Close(websocket.StatusPolicyViolation, "agent_hello required")
		return
	}
	hello, err := decodePayload[protocol.AgentHello](env)
	if err != nil || hello.ProtocolVersion != protocol.ProtocolVersion {
		conn.Close(websocket.StatusPolicyViolation, "unsupported protocol")
		return
	}
	device, err := r.store.GetDevice(ctx, hello.DeviceID)
	if err != nil || !device.Authorized || device.CredentialHash != hashSecret(hello.Credential) {
		conn.Close(websocket.StatusPolicyViolation, "invalid device credential")
		return
	}

	peer := &socketPeer{conn: conn}
	r.presence.Set(device.ID, true)
	r.presence.SetCapabilities(device.ID, hello.Capabilities)
	_ = r.store.TouchDevice(ctx, device.ID, time.Now().UTC())
	r.hub.AttachAgent(device.ID, peer)
	r.syncAgentSessions(ctx, device.ID, hello.Sessions)
	defer func() {
		r.markDisconnectedSessionsLost(context.Background(), device.ID)
		r.presence.Set(device.ID, false)
		r.hub.DetachAgent(device.ID)
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			_ = peer.Send(wshub.Outbound{Type: protocol.TypeError, Payload: protocol.ErrorMessage{Code: "invalid_envelope", Message: "invalid protocol envelope"}})
			continue
		}
		r.handleAgentEnvelope(ctx, device.ID, env, peer)
	}
}

func (r *router) handleWebWebSocket(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	// 终端大段粘贴会产生超过默认 32KB 读上限的单条 stdin 消息
	conn.SetReadLimit(1 << 20)

	ctx := req.Context()
	peer := &socketPeer{conn: conn}
	defer r.hub.UnsubscribePeer(peer)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		env, err := protocol.DecodeEnvelope(data)
		if err != nil {
			_ = peer.Send(wshub.Outbound{Type: protocol.TypeError, Payload: protocol.ErrorMessage{Code: "invalid_envelope", Message: "invalid protocol envelope"}})
			continue
		}
		switch env.Type {
		case protocol.TypeSubscribeSession:
			msg, err := decodePayload[protocol.SubscribeSession](env)
			if err != nil {
				_ = peer.Send(errorOutbound("invalid_payload", "invalid subscribe payload"))
				continue
			}
			session, err := r.store.GetTerminalSession(ctx, msg.SessionID)
			if err != nil {
				_ = peer.Send(errorOutbound("session_not_found", "session not found"))
				continue
			}
			r.hub.BindSession(session.ID, session.DeviceID)
			r.hub.SubscribeWeb(session.ID, peer)
			_ = peer.Send(wshub.Outbound{
				Type:      protocol.TypeSessionState,
				SessionID: session.ID,
				Payload:   protocol.SessionState{SessionID: session.ID, Status: session.Status},
			})
		case protocol.TypeStdin:
			msg, err := decodePayload[protocol.Stdin](env)
			if err != nil {
				_ = peer.Send(errorOutbound("invalid_payload", "invalid stdin payload"))
				continue
			}
			if err := r.hub.FromWeb(msg); err != nil {
				_ = peer.Send(errorOutbound("agent_unavailable", err.Error()))
			}
		case protocol.TypeResize:
			msg, err := decodePayload[protocol.Resize](env)
			if err != nil {
				_ = peer.Send(errorOutbound("invalid_payload", "invalid resize payload"))
				continue
			}
			if err := r.hub.FromWeb(msg); err != nil {
				_ = peer.Send(errorOutbound("agent_unavailable", err.Error()))
			}
		default:
			_ = peer.Send(errorOutbound("unsupported_message", "unsupported web message"))
		}
	}
}

func (r *router) handleAgentEnvelope(ctx context.Context, deviceID string, env protocol.Envelope, peer wshub.Peer) {
	switch env.Type {
	case protocol.TypeHeartbeat:
		_ = r.store.TouchDevice(ctx, deviceID, time.Now().UTC())
	case protocol.TypeSyncSessions:
		msg, err := decodePayload[protocol.SyncSessions](env)
		if err != nil {
			_ = peer.Send(errorOutbound("invalid_payload", "invalid sync payload"))
			return
		}
		r.syncAgentSessions(ctx, deviceID, msg.Sessions)
	case protocol.TypeSessionStarted:
		msg, err := decodePayload[protocol.SessionStarted](env)
		if err != nil {
			_ = peer.Send(errorOutbound("invalid_payload", "invalid session_started payload"))
			return
		}
		if !r.agentOwnsSession(ctx, deviceID, msg.SessionID) {
			_ = peer.Send(errorOutbound("session_forbidden", "session does not belong to this device"))
			return
		}
		_ = r.store.UpdateTerminalSessionStatus(ctx, msg.SessionID, store.SessionRunning, msg.AgentPID, msg.LastOutputSeq)
		r.hub.BindSession(msg.SessionID, deviceID)
		_ = r.hub.FromAgent(deviceID, msg)
	case protocol.TypeStdout:
		msg, err := decodePayload[protocol.Stdout](env)
		if err != nil {
			_ = peer.Send(errorOutbound("invalid_payload", "invalid stdout payload"))
			return
		}
		if !r.agentOwnsSession(ctx, deviceID, msg.SessionID) {
			_ = peer.Send(errorOutbound("session_forbidden", "session does not belong to this device"))
			return
		}
		if session, err := r.store.GetTerminalSession(ctx, msg.SessionID); err == nil {
			status := session.Status
			if status == store.SessionStarting {
				status = store.SessionRunning
			}
			_ = r.store.UpdateTerminalSessionStatus(ctx, msg.SessionID, status, session.AgentPID, msg.Seq)
		}
		r.persistOutputChunk(ctx, msg)
		_ = r.hub.FromAgent(deviceID, msg)
	case protocol.TypeSessionExit:
		msg, err := decodePayload[protocol.SessionExit](env)
		if err != nil {
			_ = peer.Send(errorOutbound("invalid_payload", "invalid session_exit payload"))
			return
		}
		if !r.agentOwnsSession(ctx, deviceID, msg.SessionID) {
			_ = peer.Send(errorOutbound("session_forbidden", "session does not belong to this device"))
			return
		}
		agentPID := 0
		lastSeq := int64(0)
		status := store.SessionExited
		if session, err := r.store.GetTerminalSession(ctx, msg.SessionID); err == nil {
			agentPID = session.AgentPID
			lastSeq = session.LastOutputSeq
			if session.Status == store.SessionClosed {
				status = store.SessionClosed
			}
		}
		_ = r.store.UpdateTerminalSessionStatus(ctx, msg.SessionID, status, agentPID, lastSeq)
		_ = r.hub.FromAgent(deviceID, protocol.SessionState{
			SessionID: msg.SessionID,
			Status:    status,
			Message:   msg.Message,
		})
	case protocol.TypeFsListResult, protocol.TypeFsReadResult, protocol.TypeFsWriteOpened,
		protocol.TypeFsWriteAck, protocol.TypeFsWriteResult, protocol.TypeFsError:
		// 迟到的响应（请求已超时删除）由 HandleAgentResponse 返回 false，直接丢弃。
		_ = r.files.HandleAgentResponse(env)
	case protocol.TypeError:
		_ = r.audit.Log(ctx, store.AuditEvent{
			DeviceID:  deviceID,
			EventType: "agent_error",
			Summary:   "agent reported an error",
		})
	default:
		_ = peer.Send(errorOutbound("unsupported_message", "unsupported agent message"))
	}
}

func (r *router) persistOutputChunk(ctx context.Context, msg protocol.Stdout) {
	if r.output == nil {
		return
	}
	path, size, err := r.output.WriteChunk(msg.SessionID, msg.Seq, msg.Seq, []byte(msg.Data))
	if err != nil {
		_ = r.audit.Log(ctx, store.AuditEvent{
			SessionID: msg.SessionID,
			EventType: "output_write_failed",
			Summary:   "terminal output chunk failed to write",
		})
		return
	}
	_, _ = r.store.CreateOutputChunk(ctx, store.OutputChunk{
		ID:          uuid.NewString(),
		SessionID:   msg.SessionID,
		StartSeq:    msg.Seq,
		EndSeq:      msg.Seq,
		StoragePath: path,
		ByteSize:    size,
	})
}

func (r *router) agentOwnsSession(ctx context.Context, deviceID string, sessionID string) bool {
	session, err := r.store.GetTerminalSession(ctx, sessionID)
	return err == nil && session.DeviceID == deviceID
}

func (r *router) syncAgentSessions(ctx context.Context, deviceID string, sessions []protocol.SessionSummary) {
	seen := map[string]struct{}{}
	for _, summary := range sessions {
		seen[summary.SessionID] = struct{}{}
		existing, err := r.store.GetTerminalSession(ctx, summary.SessionID)
		if errors.Is(err, store.ErrNotFound) {
			_, _ = r.store.CreateTerminalSession(ctx, store.TerminalSession{
				ID:               summary.SessionID,
				DeviceID:         deviceID,
				Title:            valueOr(summary.Title, "shell"),
				ShellPath:        valueOr(summary.ShellPath, "/bin/bash"),
				WorkingDirectory: valueOr(summary.WorkingDirectory, "$HOME"),
				Status:           valueOr(summary.Status, store.SessionRunning),
				AgentPID:         summary.AgentPID,
				LastOutputSeq:    summary.LastOutputSeq,
			})
			r.hub.BindSession(summary.SessionID, deviceID)
			continue
		}
		if err != nil || existing.DeviceID != deviceID {
			continue
		}
		if existing.Status == store.SessionClosed {
			continue
		}
		r.hub.BindSession(summary.SessionID, deviceID)
		_ = r.store.UpdateTerminalSessionStatus(ctx, summary.SessionID, valueOr(summary.Status, store.SessionRunning), summary.AgentPID, summary.LastOutputSeq)
	}
	existingSessions, err := r.store.ListTerminalSessionsForDevice(ctx, deviceID)
	if err != nil {
		return
	}
	for _, session := range existingSessions {
		if _, ok := seen[session.ID]; ok {
			continue
		}
		if session.Status != store.SessionRunning && session.Status != store.SessionStarting {
			continue
		}
		_ = r.store.UpdateTerminalSessionStatus(ctx, session.ID, store.SessionLost, session.AgentPID, session.LastOutputSeq)
	}
}

func (r *router) markDisconnectedSessionsLost(ctx context.Context, deviceID string) {
	sessions, err := r.store.ListTerminalSessionsForDevice(ctx, deviceID)
	if err != nil {
		return
	}
	for _, session := range sessions {
		if session.Status != store.SessionRunning && session.Status != store.SessionStarting {
			continue
		}
		_ = r.store.UpdateTerminalSessionStatus(ctx, session.ID, store.SessionLost, session.AgentPID, session.LastOutputSeq)
	}
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
		data := ""
		if r.output != nil {
			raw, err := r.output.ReadChunk(chunk.StoragePath)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "output_error", "failed to read output")
				return
			}
			data = string(raw)
		}
		out = append(out, map[string]any{
			"id":           chunk.ID,
			"session_id":   chunk.SessionID,
			"start_seq":    chunk.StartSeq,
			"end_seq":      chunk.EndSeq,
			"storage_path": chunk.StoragePath,
			"byte_size":    chunk.ByteSize,
			"data":         data,
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
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "login required")
		return store.User{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
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

const maxLoginJSONBytes int64 = 16 << 10

func readLoginJSON(w http.ResponseWriter, req *http.Request, dest any) bool {
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return false
	}
	limitedBody := http.MaxBytesReader(w, req.Body, maxLoginJSONBytes)
	defer limitedBody.Close()
	body, err := io.ReadAll(limitedBody)
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the login size limit")
		return false
	}
	if err != nil || validateNoDuplicateJSONFields(body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON without duplicate fields")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func validateNoDuplicateJSONFields(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	_, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("请求体必须只包含一个 JSON 值")
	}
	return err
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON 对象字段名无效")
			}
			if _, exists := seen[key]; exists {
				return errors.New("JSON 对象包含重复字段")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("JSON 分隔符无效")
	}
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

func deviceResponse(device store.Device, online bool) map[string]any {
	return map[string]any{
		"id":            device.ID,
		"name":          device.Name,
		"platform":      device.Platform,
		"agent_version": device.AgentVersion,
		"online":        online,
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

type socketPeer struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (p *socketPeer) Send(msg wshub.Outbound) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	var err error
	if msg.RequestID != "" {
		data, err = protocol.EncodeEnvelopeWithRequest(msg.Type, msg.RequestID, msg.Payload)
	} else {
		data, err = protocol.EncodeEnvelope(msg.Type, msg.Payload)
	}
	if err != nil {
		return err
	}
	return p.conn.Write(ctx, websocket.MessageText, data)
}

func decodePayload[T any](env protocol.Envelope) (T, error) {
	var payload T
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func errorOutbound(code string, message string) wshub.Outbound {
	return wshub.Outbound{
		Type:    protocol.TypeError,
		Payload: protocol.ErrorMessage{Code: code, Message: message},
	}
}

func valueOr(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
