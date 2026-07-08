package files

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/protocol"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

const (
	CodeAgentUnsupported = "agent_unsupported"
	CodeAgentOffline     = "agent_offline"
	CodeTimeout          = "timeout"
	CodeBusy             = "busy"
	CodeAgentError       = "agent_error"
)

type OpError struct {
	Code    string
	Message string
}

func (e *OpError) Error() string { return e.Code + ": " + e.Message }

type AgentSender interface {
	ToDevice(deviceID string, msg wshub.Outbound) error
}

type CapabilityChecker interface {
	HasCapability(deviceID string, capability string) bool
}

type Service struct {
	sender       AgentSender
	caps         CapabilityChecker
	timeout      time.Duration
	chunkSize    int
	maxPerDevice int

	mu      sync.Mutex
	pending map[string]chan protocol.Envelope
	active  map[string]int
}

func NewService(sender AgentSender, caps CapabilityChecker) *Service {
	return &Service{
		sender:       sender,
		caps:         caps,
		timeout:      30 * time.Second,
		chunkSize:    256 * 1024,
		maxPerDevice: 4,
		pending:      map[string]chan protocol.Envelope{},
		active:       map[string]int{},
	}
}

// HandleAgentResponse 按 request_id 把 agent 响应交给等待中的调用，返回是否已消费。
func (s *Service) HandleAgentResponse(env protocol.Envelope) bool {
	if env.RequestID == "" {
		return false
	}
	s.mu.Lock()
	ch, ok := s.pending[env.RequestID]
	if ok {
		delete(s.pending, env.RequestID)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	ch <- env
	return true
}

func (s *Service) List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error) {
	if err := s.checkDevice(deviceID); err != nil {
		return protocol.FsListResult{}, err
	}
	if err := s.acquire(deviceID); err != nil {
		return protocol.FsListResult{}, err
	}
	defer s.release(deviceID)
	env, err := s.roundTrip(ctx, deviceID, protocol.TypeFsList, protocol.FsList{Path: path})
	if err != nil {
		return protocol.FsListResult{}, err
	}
	return decodeResult[protocol.FsListResult](env, protocol.TypeFsListResult)
}

func (s *Service) Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error {
	if err := s.checkDevice(deviceID); err != nil {
		return err
	}
	if err := s.acquire(deviceID); err != nil {
		return err
	}
	defer s.release(deviceID)
	offset := int64(0)
	for {
		env, err := s.roundTrip(ctx, deviceID, protocol.TypeFsRead, protocol.FsRead{Path: path, Offset: offset, Length: s.chunkSize})
		if err != nil {
			return err
		}
		result, err := decodeResult[protocol.FsReadResult](env, protocol.TypeFsReadResult)
		if err != nil {
			return err
		}
		if offset == 0 && onSize != nil {
			onSize(result.FileSize)
		}
		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			return &OpError{Code: CodeAgentError, Message: "invalid chunk encoding"}
		}
		if len(data) > 0 {
			if _, err := w.Write(data); err != nil {
				return err
			}
			offset += int64(len(data))
		}
		if result.EOF {
			return nil
		}
	}
}

func (s *Service) Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error {
	if err := s.checkDevice(deviceID); err != nil {
		return err
	}
	if err := s.acquire(deviceID); err != nil {
		return err
	}
	defer s.release(deviceID)
	uploadID := uuid.NewString()
	openEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteOpen, protocol.FsWriteOpen{UploadID: uploadID, Path: path, Size: size, Overwrite: overwrite})
	if err != nil {
		return err
	}
	if _, err := decodeResult[protocol.FsWriteOpened](openEnv, protocol.TypeFsWriteOpened); err != nil {
		return err
	}
	buf := make([]byte, s.chunkSize)
	offset := int64(0)
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			chunkEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteChunk, protocol.FsWriteChunk{
				UploadID: uploadID,
				Offset:   offset,
				Data:     base64.StdEncoding.EncodeToString(buf[:n]),
			})
			if err != nil {
				return err
			}
			if _, err := decodeResult[protocol.FsWriteAck](chunkEnv, protocol.TypeFsWriteAck); err != nil {
				return err
			}
			offset += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	closeEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteClose, protocol.FsWriteClose{UploadID: uploadID, TotalSize: offset})
	if err != nil {
		return err
	}
	_, err = decodeResult[protocol.FsWriteResult](closeEnv, protocol.TypeFsWriteResult)
	return err
}

func (s *Service) checkDevice(deviceID string) error {
	if !s.caps.HasCapability(deviceID, protocol.CapabilityFs) {
		return &OpError{Code: CodeAgentUnsupported, Message: "agent does not support file operations"}
	}
	return nil
}

func (s *Service) acquire(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[deviceID] >= s.maxPerDevice {
		return &OpError{Code: CodeBusy, Message: "too many concurrent file operations"}
	}
	s.active[deviceID]++
	return nil
}

func (s *Service) release(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[deviceID]--
	if s.active[deviceID] <= 0 {
		delete(s.active, deviceID)
	}
}

func (s *Service) roundTrip(ctx context.Context, deviceID string, messageType string, payload any) (protocol.Envelope, error) {
	requestID := uuid.NewString()
	ch := make(chan protocol.Envelope, 1)
	s.mu.Lock()
	s.pending[requestID] = ch
	s.mu.Unlock()
	cleanup := func() {
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
	}
	if err := s.sender.ToDevice(deviceID, wshub.Outbound{Type: messageType, RequestID: requestID, Payload: payload}); err != nil {
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeAgentOffline, Message: "agent not connected"}
	}
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case env := <-ch:
		return env, nil
	case <-timer.C:
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeTimeout, Message: "agent did not respond in time"}
	case <-ctx.Done():
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeTimeout, Message: "request cancelled"}
	}
}

func decodeResult[T any](env protocol.Envelope, wantType string) (T, error) {
	var result T
	if env.Type == protocol.TypeFsError {
		var fsErr protocol.FsError
		if err := json.Unmarshal(env.Payload, &fsErr); err != nil {
			return result, &OpError{Code: CodeAgentError, Message: "invalid agent error payload"}
		}
		return result, &OpError{Code: fsErr.Code, Message: fsErr.Message}
	}
	if env.Type != wantType {
		return result, &OpError{Code: CodeAgentError, Message: fmt.Sprintf("unexpected reply type %q", env.Type)}
	}
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return result, &OpError{Code: CodeAgentError, Message: "invalid agent payload"}
	}
	return result, nil
}
