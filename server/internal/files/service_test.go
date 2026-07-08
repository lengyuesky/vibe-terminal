package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/protocol"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

type stubCaps bool

func (s stubCaps) HasCapability(string, string) bool { return bool(s) }

func newTestService(t *testing.T, respond func(out wshub.Outbound) (string, any)) (*Service, func()) {
	t.Helper()
	hub := wshub.NewHub()
	peer := wshub.NewMemoryPeer("agent-dev-1")
	hub.AttachAgent("dev-1", peer)
	svc := NewService(hub, stubCaps(true))
	svc.timeout = 2 * time.Second
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			out := peer.Pop()
			if out.Type == "" {
				time.Sleep(time.Millisecond)
				continue
			}
			replyType, replyPayload := respond(out)
			if replyType == "" {
				continue
			}
			raw, err := json.Marshal(replyPayload)
			if err != nil {
				panic(err)
			}
			svc.HandleAgentResponse(protocol.Envelope{Type: replyType, RequestID: out.RequestID, Payload: raw})
		}
	}()
	return svc, func() { close(stop); <-done }
}

func TestListRoundTrip(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		if out.Type != protocol.TypeFsList {
			t.Errorf("unexpected type %q", out.Type)
		}
		return protocol.TypeFsListResult, protocol.FsListResult{
			Path:    "/home/dev",
			Entries: []protocol.FsEntry{{Name: "notes.txt", Size: 12}},
		}
	})
	defer stop()
	result, err := svc.List(context.Background(), "dev-1", "~")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Path != "/home/dev" || len(result.Entries) != 1 || result.Entries[0].Name != "notes.txt" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestListPropagatesAgentError(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		return protocol.TypeFsError, protocol.FsError{Code: "not_found", Message: "no such dir"}
	})
	defer stop()
	_, err := svc.List(context.Background(), "dev-1", "/missing")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != "not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadStreamsUntilEOF(t *testing.T) {
	content := []byte("hello world!")
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		req := out.Payload.(protocol.FsRead)
		end := req.Offset + int64(req.Length)
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		chunk := content[req.Offset:end]
		return protocol.TypeFsReadResult, protocol.FsReadResult{
			Data:     base64.StdEncoding.EncodeToString(chunk),
			EOF:      len(chunk) < req.Length,
			FileSize: int64(len(content)),
		}
	})
	defer stop()
	svc.chunkSize = 5
	var buf bytes.Buffer
	var gotSize int64
	err := svc.Download(context.Background(), "dev-1", "/tmp/hello.txt", func(size int64) { gotSize = size }, &buf)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != "hello world!" || gotSize != 12 {
		t.Fatalf("data=%q size=%d", buf.String(), gotSize)
	}
}

func TestUploadSendsOpenChunksClose(t *testing.T) {
	var received bytes.Buffer
	var sawOpen, sawClose bool
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		switch out.Type {
		case protocol.TypeFsWriteOpen:
			req := out.Payload.(protocol.FsWriteOpen)
			sawOpen = true
			if req.Path != "/tmp/up.bin" || req.Overwrite {
				t.Errorf("unexpected open: %#v", req)
			}
			return protocol.TypeFsWriteOpened, protocol.FsWriteOpened{UploadID: req.UploadID}
		case protocol.TypeFsWriteChunk:
			req := out.Payload.(protocol.FsWriteChunk)
			data, err := base64.StdEncoding.DecodeString(req.Data)
			if err != nil {
				t.Errorf("chunk decode: %v", err)
			}
			received.Write(data)
			return protocol.TypeFsWriteAck, protocol.FsWriteAck{UploadID: req.UploadID, Offset: req.Offset + int64(len(data))}
		case protocol.TypeFsWriteClose:
			req := out.Payload.(protocol.FsWriteClose)
			sawClose = true
			if req.TotalSize != 12 {
				t.Errorf("total = %d", req.TotalSize)
			}
			return protocol.TypeFsWriteResult, protocol.FsWriteResult{UploadID: req.UploadID}
		}
		return "", nil
	})
	defer stop()
	svc.chunkSize = 5
	err := svc.Upload(context.Background(), "dev-1", "/tmp/up.bin", 12, false, strings.NewReader("hello world!"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !sawOpen || !sawClose || received.String() != "hello world!" {
		t.Fatalf("open=%v close=%v data=%q", sawOpen, sawClose, received.String())
	}
}

func TestTimeoutWhenAgentSilent(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) { return "", nil })
	defer stop()
	svc.timeout = 50 * time.Millisecond
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeTimeout {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsupportedAgentFailsFast(t *testing.T) {
	hub := wshub.NewHub()
	svc := NewService(hub, stubCaps(false))
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeAgentUnsupported {
		t.Fatalf("err = %v", err)
	}
}

func TestOfflineAgent(t *testing.T) {
	hub := wshub.NewHub()
	svc := NewService(hub, stubCaps(true))
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeAgentOffline {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		return protocol.TypeFsListResult, protocol.FsListResult{Path: "/tmp"}
	})
	defer stop()
	svc.maxPerDevice = 1
	if err := svc.acquire("dev-1"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeBusy {
		t.Fatalf("err = %v", err)
	}
	svc.release("dev-1")
	if _, err := svc.List(context.Background(), "dev-1", "/tmp"); err != nil {
		t.Fatalf("after release: %v", err)
	}
}
