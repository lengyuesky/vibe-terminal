package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

type chanFsStub struct {
	handled chan protocol.Envelope
}

func (s *chanFsStub) List(context.Context, string, string) (protocol.FsListResult, error) {
	return protocol.FsListResult{}, nil
}

func (s *chanFsStub) Download(context.Context, string, string, func(int64), io.Writer) error {
	return nil
}

func (s *chanFsStub) Upload(context.Context, string, string, int64, bool, io.Reader) error {
	return nil
}

func (s *chanFsStub) HandleAgentResponse(env protocol.Envelope) bool {
	s.handled <- env
	return true
}

// 覆盖真实 websocket 读上限：>32KB 的 fs 响应不得断开 agent 连接。
func TestAgentWebSocketAcceptsLargeFsMessage(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	if _, err := db.CreateDevice(ctx, store.Device{
		ID: "dev-1", Name: "laptop", Platform: "linux", AgentVersion: "0.1.0",
		Fingerprint: "fp", CredentialHash: hashSecret("agent-secret"), Authorized: true,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	stub := &chanFsStub{handled: make(chan protocol.Envelope, 2)}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		Files:    stub,
	})
	server := httptest.NewServer(router)
	defer server.Close()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent"
	conn, _, err := websocket.Dial(dialCtx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	hello, err := protocol.EncodeEnvelope(protocol.TypeAgentHello, protocol.AgentHello{
		DeviceID:        "dev-1",
		Credential:      "agent-secret",
		Platform:        "linux",
		AgentVersion:    "0.1.0",
		ProtocolVersion: protocol.ProtocolVersion,
		Sessions:        []protocol.SessionSummary{},
	})
	if err != nil {
		t.Fatalf("encode hello: %v", err)
	}
	if err := conn.Write(dialCtx, websocket.MessageText, hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	// 256KB 分块 base64 后约 350KB，远超 coder/websocket 默认 32768 字节读上限。
	bigPayload, err := json.Marshal(protocol.FsReadResult{
		Data:     strings.Repeat("A", 350*1024),
		EOF:      true,
		FileSize: 256 * 1024,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	bigEnv, err := json.Marshal(protocol.Envelope{Type: protocol.TypeFsReadResult, RequestID: "req-big", Payload: bigPayload})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if len(bigEnv) <= 32*1024 {
		t.Fatalf("test message too small: %d bytes", len(bigEnv))
	}
	if err := conn.Write(dialCtx, websocket.MessageText, bigEnv); err != nil {
		t.Fatalf("write big message: %v", err)
	}
	select {
	case env := <-stub.handled:
		if env.RequestID != "req-big" {
			t.Fatalf("request id = %q", env.RequestID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("large fs message was not processed")
	}

	// 大消息之后连接仍然可用。
	smallEnv, err := json.Marshal(protocol.Envelope{Type: protocol.TypeFsReadResult, RequestID: "req-small", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatalf("marshal small envelope: %v", err)
	}
	if err := conn.Write(dialCtx, websocket.MessageText, smallEnv); err != nil {
		t.Fatalf("write small message: %v", err)
	}
	select {
	case env := <-stub.handled:
		if env.RequestID != "req-small" {
			t.Fatalf("request id = %q", env.RequestID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connection unusable after large message")
	}
}
