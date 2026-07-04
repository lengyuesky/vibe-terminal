package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/audit"
	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/devices"
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/terminal"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

type Deps struct {
	Store       *store.DB
	Sessions    *auth.SessionManager
	Presence    *devices.Presence
	Audit       audit.Writer
	Hub         *wshub.Hub
	Output      terminal.OutputStore
	StaticFiles http.FileSystem
}

type router struct {
	store    *store.DB
	sessions *auth.SessionManager
	presence *devices.Presence
	audit    audit.Writer
	hub      *wshub.Hub
	output   terminal.OutputStore
	static   http.FileSystem
	mux      *http.ServeMux
}

func NewRouter(deps Deps) http.Handler {
	if deps.Presence == nil {
		deps.Presence = devices.NewPresence()
	}
	if deps.Audit.Store == nil {
		deps.Audit = audit.Writer{Store: deps.Store}
	}
	if deps.Hub == nil {
		deps.Hub = wshub.NewHub()
	}
	r := &router{
		store:    deps.Store,
		sessions: deps.Sessions,
		presence: deps.Presence,
		audit:    deps.Audit,
		hub:      deps.Hub,
		output:   deps.Output,
		static:   deps.StaticFiles,
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
	r.mux.HandleFunc("DELETE /api/agent-tokens/", r.handleRevokeAgentToken)
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
	if suffix != "sessions" {
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
