package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/files"
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

type stubFiles struct {
	listResult   protocol.FsListResult
	err          error
	downloadData []byte
	downloadSize int64
	uploaded     bytes.Buffer
	uploadPath   string
	uploadSize   int64
	overwrite    bool
	handled      []protocol.Envelope
}

func (s *stubFiles) List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error) {
	return s.listResult, s.err
}

func (s *stubFiles) Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error {
	if s.err != nil {
		return s.err
	}
	onSize(s.downloadSize)
	_, err := w.Write(s.downloadData)
	return err
}

func (s *stubFiles) Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error {
	if s.err != nil {
		return s.err
	}
	s.uploadPath = path
	s.uploadSize = size
	s.overwrite = overwrite
	_, err := io.Copy(&s.uploaded, r)
	return err
}

func (s *stubFiles) HandleAgentResponse(env protocol.Envelope) bool {
	s.handled = append(s.handled, env)
	return true
}

func newFsTestRouter(t *testing.T, stub *stubFiles) (http.Handler, []*http.Cookie) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := db.CreateDevice(ctx, store.Device{ID: "dev-1", Name: "laptop", Platform: "linux", AgentVersion: "0.1.0", Fingerprint: "fp", CredentialHash: "h", Authorized: true}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	router := NewRouter(Deps{
		Store:           db,
		Sessions:        auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		Files:           stub,
		FsMaxUploadSize: 1024,
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginRR.Code)
	}
	return router, loginRR.Result().Cookies()
}

func TestFsListReturnsEntries(t *testing.T) {
	stub := &stubFiles{listResult: protocol.FsListResult{Path: "/home/dev", Entries: []protocol.FsEntry{{Name: "a.txt", Size: 3}}}}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=~", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var result protocol.FsListResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Path != "/home/dev" || len(result.Entries) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestFsListRequiresAuth(t *testing.T) {
	router, _ := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=/tmp", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsListMissingPath(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsErrorMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{files.CodeAgentOffline, http.StatusServiceUnavailable},
		{files.CodeAgentUnsupported, http.StatusServiceUnavailable},
		{"not_found", http.StatusNotFound},
		{"permission_denied", http.StatusForbidden},
		{"not_a_directory", http.StatusBadRequest},
		{"already_exists", http.StatusConflict},
		{files.CodeTimeout, http.StatusGatewayTimeout},
		{files.CodeBusy, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		stub := &stubFiles{err: &files.OpError{Code: tc.code, Message: tc.code}}
		router, cookies := newFsTestRouter(t, stub)
		req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=/tmp", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("code %s: status = %d, want %d", tc.code, rr.Code, tc.want)
		}
	}
}

func TestFsDownloadSetsHeadersAndBody(t *testing.T) {
	stub := &stubFiles{downloadData: []byte("hello"), downloadSize: 5}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fhello.txt", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello" {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("Content-Length"); got != "5" {
		t.Fatalf("content-length = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename=hello.txt` {
		t.Fatalf("content-disposition = %q", got)
	}
}

func TestFsUploadStreamsBody(t *testing.T) {
	stub := &stubFiles{}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fup.bin&overwrite=true", bytes.NewBufferString("payload"))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.uploaded.String() != "payload" || stub.uploadPath != "/tmp/up.bin" || !stub.overwrite || stub.uploadSize != 7 {
		t.Fatalf("stub = %#v", stub)
	}
}

func TestFsUploadRejectsOversize(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	big := bytes.NewBuffer(make([]byte, 2048))
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fbig.bin", big)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsUploadRequiresContentLength(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fx", bytes.NewBufferString("x"))
	req.ContentLength = -1
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusLengthRequired {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsAgentEnvelopeRoutesToFilesService(t *testing.T) {
	stub := &stubFiles{}
	db := testutil.NewStore(t)
	r := &router{store: db, files: stub}
	env := protocol.Envelope{Type: protocol.TypeFsListResult, RequestID: "req-9", Payload: []byte(`{}`)}
	r.handleAgentEnvelope(context.Background(), "dev-1", env, nil)
	if len(stub.handled) != 1 || stub.handled[0].RequestID != "req-9" {
		t.Fatalf("handled = %#v", stub.handled)
	}
}
