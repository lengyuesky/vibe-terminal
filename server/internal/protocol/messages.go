package protocol

import (
	"encoding/json"
	"errors"
)

const ProtocolVersion = "v1"

const (
	TypeAgentHello       = "agent_hello"
	TypeHeartbeat        = "heartbeat"
	TypeSyncSessions     = "sync_sessions"
	TypeStartSession     = "start_session"
	TypeSessionStarted   = "session_started"
	TypeStdin            = "stdin"
	TypeResize           = "resize"
	TypeStdout           = "stdout"
	TypeSessionExit      = "session_exit"
	TypeCloseSession     = "close_session"
	TypeSubscribeSession = "subscribe_session"
	TypeSessionState     = "session_state"
	TypeError            = "error"
)

type Envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func DecodeEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	if env.Type == "" {
		return Envelope{}, errors.New("protocol envelope missing type")
	}
	return env, nil
}

func EncodeEnvelope(messageType string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	env := Envelope{Type: messageType, Payload: raw}
	if sessionPayload, ok := payload.(interface{ SessionIdentifier() string }); ok {
		env.SessionID = sessionPayload.SessionIdentifier()
	}
	return json.Marshal(env)
}

type SessionSummary struct {
	SessionID        string `json:"session_id"`
	Title            string `json:"title"`
	ShellPath        string `json:"shell_path"`
	WorkingDirectory string `json:"working_directory"`
	Status           string `json:"status"`
	AgentPID         int    `json:"agent_pid"`
	LastOutputSeq    int64  `json:"last_output_seq"`
}

type AgentHello struct {
	DeviceID        string           `json:"device_id"`
	Credential      string           `json:"credential"`
	Platform        string           `json:"platform"`
	AgentVersion    string           `json:"agent_version"`
	ProtocolVersion string           `json:"protocol_version"`
	Sessions        []SessionSummary `json:"sessions"`
}

type Heartbeat struct {
	DeviceID string `json:"device_id"`
}

type SyncSessions struct {
	DeviceID string           `json:"device_id"`
	Sessions []SessionSummary `json:"sessions"`
}

type StartSession struct {
	SessionID        string `json:"session_id"`
	ShellPath        string `json:"shell_path"`
	WorkingDirectory string `json:"working_directory"`
	Cols             int    `json:"cols"`
	Rows             int    `json:"rows"`
}

func (m StartSession) SessionIdentifier() string { return m.SessionID }

type SessionStarted struct {
	SessionID     string `json:"session_id"`
	AgentPID      int    `json:"agent_pid"`
	Title         string `json:"title"`
	LastOutputSeq int64  `json:"last_output_seq"`
}

func (m SessionStarted) SessionIdentifier() string { return m.SessionID }

type Stdin struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

func (m Stdin) SessionIdentifier() string { return m.SessionID }

type Resize struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

func (m Resize) SessionIdentifier() string { return m.SessionID }

type Stdout struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
	Data      string `json:"data"`
}

func (m Stdout) SessionIdentifier() string { return m.SessionID }

type SessionExit struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
	Message   string `json:"message"`
}

func (m SessionExit) SessionIdentifier() string { return m.SessionID }

type CloseSession struct {
	SessionID string `json:"session_id"`
}

func (m CloseSession) SessionIdentifier() string { return m.SessionID }

type SubscribeSession struct {
	SessionID string `json:"session_id"`
}

func (m SubscribeSession) SessionIdentifier() string { return m.SessionID }

type SessionState struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

func (m SessionState) SessionIdentifier() string { return m.SessionID }

type ErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
