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
	TypeFsList           = "fs_list"
	TypeFsListResult     = "fs_list_result"
	TypeFsRead           = "fs_read"
	TypeFsReadResult     = "fs_read_result"
	TypeFsWriteOpen      = "fs_write_open"
	TypeFsWriteOpened    = "fs_write_opened"
	TypeFsWriteChunk     = "fs_write_chunk"
	TypeFsWriteAck       = "fs_write_ack"
	TypeFsWriteClose     = "fs_write_close"
	TypeFsWriteResult    = "fs_write_result"
	TypeFsError          = "fs_error"
)

const CapabilityFs = "fs"

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

func EncodeEnvelopeWithRequest(messageType string, requestID string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	env := Envelope{Type: messageType, RequestID: requestID, Payload: raw}
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
	Capabilities    []string         `json:"capabilities,omitempty"`
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

type FsList struct {
	Path string `json:"path"`
}

type FsEntry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModifiedAt int64  `json:"modified_at"`
}

type FsListResult struct {
	Path    string    `json:"path"`
	Entries []FsEntry `json:"entries"`
}

type FsRead struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
}

type FsReadResult struct {
	Data     string `json:"data"`
	EOF      bool   `json:"eof"`
	FileSize int64  `json:"file_size"`
}

type FsWriteOpen struct {
	UploadID  string `json:"upload_id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Overwrite bool   `json:"overwrite"`
}

type FsWriteOpened struct {
	UploadID string `json:"upload_id"`
}

type FsWriteChunk struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
	Data     string `json:"data"`
}

type FsWriteAck struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
}

type FsWriteClose struct {
	UploadID  string `json:"upload_id"`
	TotalSize int64  `json:"total_size"`
}

type FsWriteResult struct {
	UploadID string `json:"upload_id"`
}

type FsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
