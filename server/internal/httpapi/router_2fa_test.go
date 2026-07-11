package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

const (
	testLoginUserID          = "login-user"
	testLoginUsername        = "admin"
	testLoginPassword        = "secret"
	testLoginConfigurationID = "configuration-1"
	testLoginIP              = "203.0.113.10"
)

var testLoginRootKey = []byte("0123456789abcdef0123456789abcdef")

type loginTwoFactorFixture struct {
	t                  *testing.T
	db                 *store.DB
	handler            http.Handler
	manager            *auth.TwoFactorManager
	now                time.Time
	secret             string
	recoveryCode       string
	secondRecoveryCode string
	configurationID    string
}

type loginChallengeResponse struct {
	TwoFactorRequired bool   `json:"two_factor_required"`
	ChallengeToken    string `json:"challenge_token"`
	ExpiresIn         int    `json:"expires_in"`
}

type twoFactorStatusResponse struct {
	Enabled                bool `json:"enabled"`
	RecoveryCodesRemaining int  `json:"recovery_codes_remaining"`
}

type twoFactorSetupResponse struct {
	ManualKey  string    `json:"manual_key"`
	OtpauthURI string    `json:"otpauth_uri"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type recoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type failingAuditWriter struct {
	err error
}

type recordingFailingAuditWriter struct {
	err    error
	events []store.AuditEvent
}

type auditWriterFunc func(context.Context, store.AuditEvent) error

type countingReader struct {
	reader *strings.Reader
	read   int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += count
	return count, err
}

func (w failingAuditWriter) Log(context.Context, store.AuditEvent) error {
	return w.err
}

func (w *recordingFailingAuditWriter) Log(_ context.Context, event store.AuditEvent) error {
	w.events = append(w.events, event)
	return w.err
}

func (fn auditWriterFunc) Log(ctx context.Context, event store.AuditEvent) error {
	return fn(ctx, event)
}

func newLoginTwoFactorFixture(t *testing.T, enabled bool) *loginTwoFactorFixture {
	t.Helper()
	return newLoginTwoFactorFixtureWithStore(t, testutil.NewStore(t), enabled)
}

func newConcurrentLoginTwoFactorFixture(t *testing.T, enabled bool) *loginTwoFactorFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "login.db"))
	if err != nil {
		t.Fatalf("打开并发登录数据库失败：%v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("关闭并发登录数据库失败：%v", err)
		}
	})
	db.SQL.SetMaxOpenConns(2)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移并发登录数据库失败：%v", err)
	}
	return newLoginTwoFactorFixtureWithStore(t, db, enabled)
}

func newLoginTwoFactorFixtureWithStore(t *testing.T, db *store.DB, enabled bool) *loginTwoFactorFixture {
	t.Helper()
	ctx := context.Background()
	fixture := &loginTwoFactorFixture{
		t:                  t,
		db:                 db,
		now:                time.Unix(75, 0).UTC(),
		recoveryCode:       "ABCD-EFGH-IJKL",
		secondRecoveryCode: "MNOP-QRST-UVWX",
		configurationID:    testLoginConfigurationID,
	}
	passwordHash, err := auth.HashPassword(testLoginPassword)
	if err != nil {
		t.Fatalf("生成测试密码哈希失败：%v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{
		ID:           testLoginUserID,
		Username:     testLoginUsername,
		PasswordHash: passwordHash,
	}); err != nil {
		t.Fatalf("创建测试管理员失败：%v", err)
	}
	manager, err := auth.NewTwoFactorManager(testLoginRootKey, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	fixture.manager = manager
	if enabled {
		secret, _, ciphertext, err := manager.GenerateSetup("Vibe Terminal", testLoginUsername)
		if err != nil {
			t.Fatalf("生成测试双因素配置失败：%v", err)
		}
		fixture.secret = secret
		if err := db.SavePendingTwoFactor(ctx, store.UserTwoFactor{
			UserID:           testLoginUserID,
			ConfigurationID:  fixture.configurationID,
			SecretCiphertext: ciphertext,
			SetupExpiresAt:   sql.NullTime{Time: fixture.now.Add(10 * time.Minute), Valid: true},
			CreatedAt:        fixture.now,
			UpdatedAt:        fixture.now,
		}); err != nil {
			t.Fatalf("保存测试双因素配置失败：%v", err)
		}
		if err := db.EnableTwoFactor(ctx, testLoginUserID, fixture.configurationID, 1, []store.RecoveryCodeInput{{
			ID:   "recovery-1",
			Hash: manager.RecoveryCodeHash(testLoginUserID, fixture.recoveryCode),
		}, {
			ID:   "recovery-2",
			Hash: manager.RecoveryCodeHash(testLoginUserID, fixture.secondRecoveryCode),
		}}, fixture.now); err != nil {
			t.Fatalf("启用测试双因素配置失败：%v", err)
		}
	}

	fixture.handler = NewRouter(Deps{
		Store:     db,
		Sessions:  auth.NewSessionManager(testLoginRootKey, time.Hour),
		TwoFactor: manager,
		Now:       func() time.Time { return fixture.now },
	})
	return fixture
}

func (f *loginTwoFactorFixture) passwordLogin(username, password string) *httptest.ResponseRecorder {
	return f.passwordLoginFromIP(username, password, testLoginIP, "")
}

func (f *loginTwoFactorFixture) passwordLoginFromIP(username, password, remoteIP, forwardedIP string) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		f.t.Fatalf("编码密码登录请求失败：%v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if forwardedIP != "" {
		req.Header.Set("X-Forwarded-For", forwardedIP)
	}
	req.RemoteAddr = remoteIP + ":43210"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *loginTwoFactorFixture) beginTwoFactorLogin() loginChallengeResponse {
	f.t.Helper()
	rr := f.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code != http.StatusAccepted {
		f.t.Fatalf("密码阶段状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var response loginChallengeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		f.t.Fatalf("解析登录挑战响应失败：%v", err)
	}
	return response
}

func (f *loginTwoFactorFixture) secondFactor(challenge, code, contentType string) *httptest.ResponseRecorder {
	return f.secondFactorFromIP(challenge, code, contentType, testLoginIP)
}

func (f *loginTwoFactorFixture) secondFactorFromIP(challenge, code, contentType, remoteIP string) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(map[string]string{"challenge_token": challenge, "code": code})
	if err != nil {
		f.t.Fatalf("编码二因素登录请求失败：%v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login/2fa", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.RemoteAddr = remoteIP + ":43210"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *loginTwoFactorFixture) currentTOTP() string {
	f.t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(f.secret)
	if err != nil {
		f.t.Fatalf("解码测试 TOTP 密钥失败：%v", err)
	}
	counter := uint64(f.now.Unix() / auth.TOTPPeriodSeconds)
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

func (f *loginTwoFactorFixture) authenticatedCookie() *http.Cookie {
	f.t.Helper()
	rr := f.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code == http.StatusAccepted {
		var response loginChallengeResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			f.t.Fatalf("解析登录挑战响应失败：%v", err)
		}
		rr = f.secondFactor(response.ChallengeToken, f.currentTOTP(), "application/json")
	}
	if rr.Code != http.StatusOK {
		f.t.Fatalf("创建管理接口会话失败：状态码=%d，响应=%s", rr.Code, rr.Body.String())
	}
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == auth.CookieName {
			return cookie
		}
	}
	f.t.Fatal("登录成功响应缺少会话 Cookie")
	return nil
}

func (f *loginTwoFactorFixture) managementRequest(method, path, body, contentType string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return f.managementRequestFromIP(method, path, body, contentType, cookie, testLoginIP, "")
}

func (f *loginTwoFactorFixture) managementRequestFromIP(method, path, body, contentType string, cookie *http.Cookie, remoteIP, forwardedIP string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if forwardedIP != "" {
		req.Header.Set("X-Forwarded-For", forwardedIP)
	}
	req.RemoteAddr = remoteIP + ":43210"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *loginTwoFactorFixture) useManagementLimiter(limiter *auth.FailureLimiter) {
	f.t.Helper()
	f.handler = NewRouter(Deps{
		Store:             f.db,
		Sessions:          auth.NewSessionManager(testLoginRootKey, time.Hour),
		TwoFactor:         f.manager,
		ManagementLimiter: limiter,
		Now:               func() time.Time { return f.now },
	})
}

func (f *loginTwoFactorFixture) useAuditWriter(writer AuditWriter) {
	f.t.Helper()
	f.handler = NewRouter(Deps{
		Store:     f.db,
		Sessions:  auth.NewSessionManager(testLoginRootKey, time.Hour),
		TwoFactor: f.manager,
		Audit:     writer,
		Now:       func() time.Time { return f.now },
	})
}

func (f *loginTwoFactorFixture) beginManagementSetup(cookie *http.Cookie) twoFactorSetupResponse {
	f.t.Helper()
	rr := f.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if rr.Code != http.StatusOK {
		f.t.Fatalf("创建管理 setup 失败：状态码=%d，响应=%s", rr.Code, rr.Body.String())
	}
	var response twoFactorSetupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		f.t.Fatalf("解析管理 setup 响应失败：%v", err)
	}
	f.secret = response.ManualKey
	return response
}

func (f *loginTwoFactorFixture) router() *router {
	f.t.Helper()
	r, ok := f.handler.(*router)
	if !ok {
		f.t.Fatalf("测试处理器类型 = %T，期望 *router", f.handler)
	}
	return r
}

func synchronizeConcurrentSecondFactorNow(f *loginTwoFactorFixture) {
	f.t.Helper()
	firstPhase := make(chan struct{})
	secondPhase := make(chan struct{})
	var calls atomic.Int32
	f.router().now = func() time.Time {
		call := calls.Add(1)
		switch {
		case call <= 2:
			if call == 2 {
				close(firstPhase)
			}
			<-firstPhase
		case call <= 4:
			if call == 4 {
				close(secondPhase)
			}
			<-secondPhase
		}
		return f.now
	}
}

func responseErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	assertNoSessionCookie(t, rr)
	var response map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析错误响应失败：%v；响应=%s", err, rr.Body.String())
	}
	return response["code"]
}

func assertNoSessionCookie(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == auth.CookieName {
			t.Fatalf("失败响应不应包含会话 Cookie：%s", rr.Header().Get("Set-Cookie"))
		}
	}
}

func loginRateLimitAudits(t *testing.T, db *store.DB) []map[string]string {
	t.Helper()
	rows, err := db.SQL.QueryContext(context.Background(),
		`select metadata_json from audit_events where event_type = 'login_rate_limited' order by rowid`)
	if err != nil {
		t.Fatalf("查询登录限流审计失败：%v", err)
	}
	defer rows.Close()
	var audits []map[string]string
	for rows.Next() {
		var metadataJSON string
		if err := rows.Scan(&metadataJSON); err != nil {
			t.Fatalf("读取登录限流审计失败：%v", err)
		}
		var metadata map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			t.Fatalf("解析登录限流审计失败：%v", err)
		}
		audits = append(audits, metadata)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历登录限流审计失败：%v", err)
	}
	return audits
}

func assertLoginChallengeUnconsumed(t *testing.T, db *store.DB, jti string) {
	t.Helper()
	var consumedAt sql.NullTime
	if err := db.SQL.QueryRowContext(context.Background(),
		`select consumed_at from login_challenges where jti = ?`, jti).Scan(&consumedAt); err != nil {
		t.Fatalf("查询登录挑战消费状态失败：%v", err)
	}
	if consumedAt.Valid {
		t.Fatalf("登录挑战 %s 不应被消费：%v", jti, consumedAt.Time)
	}
}

func TestTwoFactorManagementStatusRequiresAuthentication(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)

	rr := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", nil)
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "unauthorized" {
		t.Fatalf("未认证状态查询响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestTwoFactorManagementStatusReportsDisabledAndEnabled(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		enabled       bool
		remainingCode int
	}{
		{name: "未启用", enabled: false, remainingCode: 0},
		{name: "已启用", enabled: true, remainingCode: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newLoginTwoFactorFixture(t, testCase.enabled)
			cookie := fixture.authenticatedCookie()

			rr := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
			if rr.Code != http.StatusOK {
				t.Fatalf("状态查询状态码 = %d，响应=%s", rr.Code, rr.Body.String())
			}
			var response twoFactorStatusResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatalf("解析状态查询响应失败：%v", err)
			}
			if response.Enabled != testCase.enabled || response.RecoveryCodesRemaining != testCase.remainingCode {
				t.Fatalf("状态查询响应 = %#v", response)
			}
		})
	}
}

func TestTwoFactorSetupRevalidatesPasswordAndSavesEncryptedPendingSecret(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()

	wrong := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"wrong"}`, "application/json", cookie)
	if wrong.Code != http.StatusUnauthorized || responseErrorCode(t, wrong) != "invalid_credentials" {
		t.Fatalf("错误当前密码响应 = %d %s", wrong.Code, wrong.Body.String())
	}
	if _, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("错误密码后待确认配置错误 = %v，期望 not found", err)
	}

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("创建待确认配置状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var response twoFactorSetupResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析待确认配置响应失败：%v", err)
	}
	if response.ManualKey == "" || !strings.HasPrefix(response.OtpauthURI, "otpauth://totp/") {
		t.Fatalf("待确认配置响应缺少密钥或 URI：%#v", response)
	}
	if !response.ExpiresAt.Equal(fixture.now.Add(10 * time.Minute)) {
		t.Fatalf("待确认配置到期时间 = %s", response.ExpiresAt)
	}
	pending, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now)
	if err != nil {
		t.Fatalf("读取待确认配置失败：%v", err)
	}
	if pending.ConfigurationID == "" || pending.SecretCiphertext == "" || pending.SecretCiphertext == response.ManualKey {
		t.Fatalf("待确认配置未安全保存：%#v", pending)
	}
	decrypted, err := fixture.manager.DecryptSecret(pending.SecretCiphertext)
	if err != nil || decrypted != response.ManualKey {
		t.Fatalf("解密待确认密钥 = %q, %v", decrypted, err)
	}
	if strings.Contains(rr.Body.String(), pending.SecretCiphertext) {
		t.Fatal("setup 响应泄露了密钥密文")
	}
}

func TestTwoFactorSetupUsesVibeTerminalIssuer(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	setup := fixture.beginManagementSetup(fixture.authenticatedCookie())
	parsed, err := url.Parse(setup.OtpauthURI)
	if err != nil {
		t.Fatalf("解析otpauth URI失败：%v", err)
	}
	if parsed.Query().Get("issuer") != "vibe-terminal" {
		t.Fatalf("issuer = %q，期望 vibe-terminal", parsed.Query().Get("issuer"))
	}
	if label, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/")); err != nil || label != "vibe-terminal:"+testLoginUsername {
		t.Fatalf("TOTP标签 = %q, %v", label, err)
	}
}

func TestTwoFactorSetupReplacesPendingButCannotOverwriteEnabledState(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	first := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if first.Code != http.StatusOK {
		t.Fatalf("首次 setup 状态码 = %d，响应=%s", first.Code, first.Body.String())
	}
	firstPending, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now)
	if err != nil {
		t.Fatalf("读取首次 pending 失败：%v", err)
	}
	second := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if second.Code != http.StatusOK {
		t.Fatalf("重复 pending setup 状态码 = %d，响应=%s", second.Code, second.Body.String())
	}
	secondPending, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now)
	if err != nil {
		t.Fatalf("读取替换 pending 失败：%v", err)
	}
	if secondPending.ConfigurationID == firstPending.ConfigurationID {
		t.Fatal("重复 setup 未替换配置 UUID")
	}

	enabledFixture := newLoginTwoFactorFixture(t, true)
	enabledCookie := enabledFixture.authenticatedCookie()
	rr := enabledFixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", enabledCookie)
	if rr.Code != http.StatusConflict || responseErrorCode(t, rr) != "two_factor_state_conflict" {
		t.Fatalf("已启用状态 setup 响应 = %d %s", rr.Code, rr.Body.String())
	}
	setting, err := enabledFixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID)
	if err != nil || setting.ConfigurationID != testLoginConfigurationID {
		t.Fatalf("已启用配置被覆盖：%#v, %v", setting, err)
	}
}

func TestConcurrentTwoFactorSetupAllowsOneWinner(t *testing.T) {
	fixture := newConcurrentLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	fixture.router().beforePendingTwoFactorSave = func() {
		arrived <- struct{}{}
		<-release
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			responses <- fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
				`{"password":"secret"}`, "application/json", cookie)
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("并发 setup 未同时到达保存屏障")
		}
	}
	close(release)

	var successes int
	var conflicts int
	for range 2 {
		rr := <-responses
		switch {
		case rr.Code == http.StatusOK:
			successes++
		case rr.Code == http.StatusConflict && responseErrorCode(t, rr) == "two_factor_state_conflict":
			conflicts++
		default:
			t.Fatalf("并发 setup 响应 = %d %s", rr.Code, rr.Body.String())
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("并发 setup：成功=%d，冲突=%d", successes, conflicts)
	}

	fixture.router().beforePendingTwoFactorSave = nil
	sequential := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if sequential.Code != http.StatusOK {
		t.Fatalf("并发完成后的顺序 setup = %d %s", sequential.Code, sequential.Body.String())
	}
}

func TestTwoFactorSetupUnavailableWithoutManager(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.handler = NewRouter(Deps{
		Store:    fixture.db,
		Sessions: auth.NewSessionManager(testLoginRootKey, time.Hour),
		Now:      func() time.Time { return fixture.now },
	})

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
		t.Fatalf("缺少二因素管理器响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestTwoFactorEnableRejectsInvalidCodeWithoutChangingPending(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.beginManagementSetup(cookie)
	pendingBefore, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now)
	if err != nil {
		t.Fatalf("读取启用前 pending 失败：%v", err)
	}

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"abcdef"}`, "application/json", cookie)
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "invalid_two_factor_code" {
		t.Fatalf("错误启用 TOTP 响应 = %d %s", rr.Code, rr.Body.String())
	}
	pendingAfter, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now)
	if err != nil || pendingAfter.ConfigurationID != pendingBefore.ConfigurationID {
		t.Fatalf("错误 TOTP 改变了 pending：%#v, %v", pendingAfter, err)
	}
}

func TestTwoFactorEnableReturnsRecoveryCodesOnceWithoutLeakingStoredSecrets(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.beginManagementSetup(cookie)

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("启用二因素状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var response recoveryCodesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析启用响应失败：%v", err)
	}
	if len(response.RecoveryCodes) != 10 {
		t.Fatalf("启用返回恢复码数量 = %d", len(response.RecoveryCodes))
	}
	setting, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID)
	if err != nil {
		t.Fatalf("读取已启用配置失败：%v", err)
	}
	if setting.SecretCiphertext == "" || strings.Contains(rr.Body.String(), setting.SecretCiphertext) {
		t.Fatal("启用响应泄露或未保存密钥密文")
	}
	rows, err := fixture.db.SQL.QueryContext(context.Background(),
		`select code_hash from two_factor_recovery_codes where user_id = ?`, testLoginUserID)
	if err != nil {
		t.Fatalf("查询恢复码哈希失败：%v", err)
	}
	defer rows.Close()
	storedHashes := map[string]struct{}{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("读取恢复码哈希失败：%v", err)
		}
		storedHashes[hash] = struct{}{}
		if strings.Contains(rr.Body.String(), hash) {
			t.Fatal("启用响应泄露恢复码哈希")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历恢复码哈希失败：%v", err)
	}
	if len(storedHashes) != 10 {
		t.Fatalf("存储恢复码哈希数量 = %d", len(storedHashes))
	}
	for _, code := range response.RecoveryCodes {
		if _, leakedRaw := storedHashes[code]; leakedRaw {
			t.Fatalf("数据库保存了原始恢复码：%q", code)
		}
	}
	var auditCount int
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select count(*) from audit_events where event_type = 'two_factor_enabled' and user_id = ?`,
		testLoginUserID).Scan(&auditCount); err != nil {
		t.Fatalf("查询启用审计失败：%v", err)
	}
	if auditCount != 1 {
		t.Fatalf("启用审计数量 = %d", auditCount)
	}

	replay := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	if replay.Code != http.StatusConflict || responseErrorCode(t, replay) != "two_factor_setup_expired" {
		t.Fatalf("重复 enable 响应 = %d %s", replay.Code, replay.Body.String())
	}
}

func TestTwoFactorEnableRejectsExpiredOrMissingSetup(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.beginManagementSetup(cookie)
	fixture.now = fixture.now.Add(10 * time.Minute)

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	if rr.Code != http.StatusConflict || responseErrorCode(t, rr) != "two_factor_setup_expired" {
		t.Fatalf("过期 setup 启用响应 = %d %s", rr.Code, rr.Body.String())
	}

	missingFixture := newLoginTwoFactorFixture(t, false)
	missingCookie := missingFixture.authenticatedCookie()
	missing := missingFixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"000000"}`, "application/json", missingCookie)
	if missing.Code != http.StatusConflict || responseErrorCode(t, missing) != "two_factor_setup_expired" {
		t.Fatalf("不存在 setup 启用响应 = %d %s", missing.Code, missing.Body.String())
	}
}

func TestTwoFactorRecoveryCodeRotationRequiresPasswordAndCurrentTOTP(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	cookie := fixture.authenticatedCookie()
	fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)

	wrongPassword := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
		`{"password":"wrong","code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	if wrongPassword.Code != http.StatusUnauthorized || responseErrorCode(t, wrongPassword) != "invalid_credentials" {
		t.Fatalf("轮换错误密码响应 = %d %s", wrongPassword.Code, wrongPassword.Body.String())
	}
	wrongTOTP := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
		`{"password":"secret","code":"abcdef"}`, "application/json", cookie)
	if wrongTOTP.Code != http.StatusUnauthorized || responseErrorCode(t, wrongTOTP) != "invalid_two_factor_code" {
		t.Fatalf("轮换错误 TOTP 响应 = %d %s", wrongTOTP.Code, wrongTOTP.Body.String())
	}
	for _, oldCode := range []string{fixture.recoveryCode, fixture.secondRecoveryCode} {
		hash := fixture.manager.RecoveryCodeHash(testLoginUserID, oldCode)
		var count int
		if err := fixture.db.SQL.QueryRowContext(context.Background(),
			`select count(*) from two_factor_recovery_codes where user_id = ? and code_hash = ? and used_at is null`,
			testLoginUserID, hash).Scan(&count); err != nil {
			t.Fatalf("查询错误凭据后的旧恢复码失败：%v", err)
		}
		if count != 1 {
			t.Fatalf("错误凭据使旧恢复码 %q 失效", oldCode)
		}
	}
}

func TestRecoveryCodeRotationMapsStoreErrorsPrecisely(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		statusCode int
		errorCode  string
	}{
		{name: "配置变化", err: store.ErrConflict, statusCode: http.StatusConflict, errorCode: "two_factor_state_conflict"},
		{name: "计数器重放", err: store.ErrInvalidSecondFactor, statusCode: http.StatusUnauthorized, errorCode: "invalid_two_factor_code"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			if !writeRecoveryCodeRotationStoreError(rr, testCase.err) {
				t.Fatalf("错误 %v 未被处理", testCase.err)
			}
			if rr.Code != testCase.statusCode || responseErrorCode(t, rr) != testCase.errorCode {
				t.Fatalf("轮换错误映射 = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestTwoFactorRecoveryCodeRotationInvalidatesOldCodesAndRejectsTOTPReplay(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	cookie := fixture.authenticatedCookie()
	fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)
	code := fixture.currentTOTP()

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
		`{"password":"secret","code":"`+code+`"}`, "application/json", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("轮换恢复码状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var response recoveryCodesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析轮换响应失败：%v", err)
	}
	if len(response.RecoveryCodes) != 10 {
		t.Fatalf("轮换返回恢复码数量 = %d", len(response.RecoveryCodes))
	}
	for _, rawCode := range response.RecoveryCodes {
		var rawCount int
		if err := fixture.db.SQL.QueryRowContext(context.Background(),
			`select count(*) from two_factor_recovery_codes where user_id = ? and code_hash = ?`,
			testLoginUserID, rawCode).Scan(&rawCount); err != nil {
			t.Fatalf("查询原始恢复码存储失败：%v", err)
		}
		if rawCount != 0 {
			t.Fatalf("数据库保存了原始恢复码：%q", rawCode)
		}
		var hashCount int
		if err := fixture.db.SQL.QueryRowContext(context.Background(),
			`select count(*) from two_factor_recovery_codes where user_id = ? and code_hash = ?`,
			testLoginUserID, fixture.manager.RecoveryCodeHash(testLoginUserID, rawCode)).Scan(&hashCount); err != nil {
			t.Fatalf("查询轮换恢复码哈希失败：%v", err)
		}
		if hashCount != 1 {
			t.Fatalf("轮换恢复码哈希数量 = %d", hashCount)
		}
	}
	for _, oldCode := range []string{fixture.recoveryCode, fixture.secondRecoveryCode} {
		oldHash := fixture.manager.RecoveryCodeHash(testLoginUserID, oldCode)
		var count int
		if err := fixture.db.SQL.QueryRowContext(context.Background(),
			`select count(*) from two_factor_recovery_codes where user_id = ? and code_hash = ?`,
			testLoginUserID, oldHash).Scan(&count); err != nil {
			t.Fatalf("查询旧恢复码失败：%v", err)
		}
		if count != 0 {
			t.Fatalf("旧恢复码 %q 仍存在", oldCode)
		}
	}
	var auditCount int
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select count(*) from audit_events where event_type = 'two_factor_recovery_codes_regenerated' and user_id = ?`,
		testLoginUserID).Scan(&auditCount); err != nil {
		t.Fatalf("查询恢复码轮换审计失败：%v", err)
	}
	if auditCount != 1 {
		t.Fatalf("恢复码轮换审计数量 = %d", auditCount)
	}

	replay := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
		`{"password":"secret","code":"`+code+`"}`, "application/json", cookie)
	if replay.Code != http.StatusUnauthorized || responseErrorCode(t, replay) != "invalid_two_factor_code" {
		t.Fatalf("轮换 TOTP 重放响应 = %d %s", replay.Code, replay.Body.String())
	}
	if strings.Contains(replay.Body.String(), "recovery_codes") {
		t.Fatal("TOTP 重放不应返回第二批恢复码")
	}

	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	oldLogin := fixture.secondFactor(challenge, fixture.recoveryCode, "application/json")
	if oldLogin.Code != http.StatusUnauthorized || responseErrorCode(t, oldLogin) != "invalid_two_factor_code" {
		t.Fatalf("轮换后旧恢复码登录响应 = %d %s", oldLogin.Code, oldLogin.Body.String())
	}
	newLogin := fixture.secondFactor(challenge, response.RecoveryCodes[0], "application/json")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("轮换后新恢复码登录状态码 = %d，响应=%s", newLogin.Code, newLogin.Body.String())
	}
}

func TestTwoFactorDisableRevalidatesPasswordAndRejectsMissingEnabledState(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	cookie := fixture.authenticatedCookie()

	wrong := fixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
		`{"password":"wrong"}`, "application/json", cookie)
	if wrong.Code != http.StatusUnauthorized || responseErrorCode(t, wrong) != "invalid_credentials" {
		t.Fatalf("关闭错误密码响应 = %d %s", wrong.Code, wrong.Body.String())
	}
	if _, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID); err != nil {
		t.Fatalf("错误密码关闭了二因素：%v", err)
	}

	disabledFixture := newLoginTwoFactorFixture(t, false)
	disabledCookie := disabledFixture.authenticatedCookie()
	missing := disabledFixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
		`{"password":"secret"}`, "application/json", disabledCookie)
	if missing.Code != http.StatusConflict || responseErrorCode(t, missing) != "two_factor_state_conflict" {
		t.Fatalf("未启用关闭响应 = %d %s", missing.Code, missing.Body.String())
	}
}

func TestTwoFactorDisableRemovesCodesReportsDisabledAndRestoresPasswordLogin(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	cookie := fixture.authenticatedCookie()

	rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
		`{"password":"secret"}`, "application/json", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("关闭二因素状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var response map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || !response["ok"] {
		t.Fatalf("关闭二因素响应 = %#v, %v", response, err)
	}
	if _, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("关闭后已启用配置错误 = %v", err)
	}
	remaining, err := fixture.db.CountRecoveryCodes(context.Background(), testLoginUserID)
	if err != nil || remaining != 0 {
		t.Fatalf("关闭后恢复码数量 = %d, %v", remaining, err)
	}
	status := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
	if status.Code != http.StatusOK {
		t.Fatalf("关闭后状态查询 = %d %s", status.Code, status.Body.String())
	}
	var statusResponse twoFactorStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &statusResponse); err != nil {
		t.Fatalf("解析关闭后状态失败：%v", err)
	}
	if statusResponse.Enabled || statusResponse.RecoveryCodesRemaining != 0 {
		t.Fatalf("关闭后状态 = %#v", statusResponse)
	}
	passwordLogin := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if passwordLogin.Code != http.StatusOK || len(passwordLogin.Result().Cookies()) == 0 {
		t.Fatalf("关闭后密码登录 = %d %s", passwordLogin.Code, passwordLogin.Body.String())
	}
	var auditCount int
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select count(*) from audit_events where event_type = 'two_factor_disabled' and user_id = ?`,
		testLoginUserID).Scan(&auditCount); err != nil {
		t.Fatalf("查询关闭二因素审计失败：%v", err)
	}
	if auditCount != 1 {
		t.Fatalf("关闭二因素审计数量 = %d", auditCount)
	}
}

func TestTwoFactorManagementRoutesAllRequireAuthentication(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/security/2fa"},
		{method: http.MethodPost, path: "/api/security/2fa/setup"},
		{method: http.MethodPost, path: "/api/security/2fa/enable"},
		{method: http.MethodPost, path: "/api/security/2fa/recovery-codes"},
		{method: http.MethodPost, path: "/api/security/2fa/disable"},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			rr := fixture.managementRequest(testCase.method, testCase.path, `{}`, "application/json", nil)
			if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "unauthorized" {
				t.Fatalf("未认证管理响应 = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestManagementReauthenticationFailuresAreRateLimitedByStage(t *testing.T) {
	testCases := []struct {
		name    string
		enabled bool
		prepare func(*loginTwoFactorFixture, *http.Cookie)
		path    string
		body    string
		stage   string
	}{
		{name: "setup密码", path: "/api/security/2fa/setup", body: `{"password":"wrong"}`, stage: "setup_password"},
		{name: "disable密码", enabled: true, path: "/api/security/2fa/disable", body: `{"password":"wrong"}`, stage: "disable_password"},
		{name: "regen密码", enabled: true, path: "/api/security/2fa/recovery-codes", body: `{"password":"wrong","code":"000000"}`, stage: "recovery_password"},
		{name: "regen TOTP", enabled: true, path: "/api/security/2fa/recovery-codes", body: `{"password":"secret","code":"abcdef"}`, stage: "recovery_totp"},
		{name: "enable TOTP", prepare: func(f *loginTwoFactorFixture, cookie *http.Cookie) { f.beginManagementSetup(cookie) }, path: "/api/security/2fa/enable", body: `{"code":"abcdef"}`, stage: "enable_totp"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newLoginTwoFactorFixture(t, testCase.enabled)
			cookie := fixture.authenticatedCookie()
			if testCase.prepare != nil {
				testCase.prepare(fixture, cookie)
			}
			fixture.useManagementLimiter(auth.NewFailureLimiter(1, 10*time.Minute, 15*time.Minute, 100, func() time.Time { return fixture.now }))
			rr := fixture.managementRequest(http.MethodPost, testCase.path, testCase.body, "application/json", cookie)
			if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" || responseErrorCode(t, rr) != "too_many_attempts" {
				t.Fatalf("管理限流响应 = %d %s", rr.Code, rr.Body.String())
			}
			var metadataJSON string
			if err := fixture.db.SQL.QueryRowContext(context.Background(),
				`select metadata_json from audit_events where event_type = 'management_reauthentication_rate_limited'`).Scan(&metadataJSON); err != nil {
				t.Fatalf("查询管理限流审计失败：%v", err)
			}
			var metadata map[string]string
			if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil || metadata["stage"] != testCase.stage {
				t.Fatalf("管理限流审计 = %s, %v", metadataJSON, err)
			}
			_ = fixture.managementRequest(http.MethodPost, testCase.path, testCase.body, "application/json", cookie)
			var auditCount int
			if err := fixture.db.SQL.QueryRowContext(context.Background(),
				`select count(*) from audit_events where event_type = 'management_reauthentication_rate_limited'`).Scan(&auditCount); err != nil || auditCount != 1 {
				t.Fatalf("重复阻断后的管理限流审计数量 = %d, %v", auditCount, err)
			}
		})
	}
}

func TestManagementReauthenticationSuccessClearsFailures(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.useManagementLimiter(auth.NewFailureLimiter(2, 10*time.Minute, 15*time.Minute, 100, func() time.Time { return fixture.now }))
	wrong := func() *httptest.ResponseRecorder {
		return fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup", `{"password":"wrong"}`, "application/json", cookie)
	}
	if rr := wrong(); rr.Code != http.StatusUnauthorized {
		t.Fatalf("首次错误密码 = %d %s", rr.Code, rr.Body.String())
	}
	if rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup", `{"password":"secret"}`, "application/json", cookie); rr.Code != http.StatusOK {
		t.Fatalf("成功setup = %d %s", rr.Code, rr.Body.String())
	}
	if rr := wrong(); rr.Code != http.StatusUnauthorized {
		t.Fatalf("成功后首次错误密码 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestManagementLimiterAggregatesUserAndDirectIP(t *testing.T) {
	now := time.Unix(100, 0)
	byUser := auth.NewFailureLimiter(2, time.Minute, time.Minute, 100, func() time.Time { return now })
	if blocked, _ := recordLoginFailure(byUser, managementLimitKeys("user-1", "192.0.2.1")); blocked {
		t.Fatal("用户首次失败不应限流")
	}
	if blocked, _ := recordLoginFailure(byUser, managementLimitKeys("user-1", "192.0.2.2")); !blocked {
		t.Fatal("轮换IP的同一用户应被用户维度限流")
	}
	byIP := auth.NewFailureLimiter(2, time.Minute, time.Minute, 100, func() time.Time { return now })
	if blocked, _ := recordLoginFailure(byIP, managementLimitKeys("user-1", "192.0.2.9")); blocked {
		t.Fatal("同IP首个用户失败不应限流")
	}
	if blocked, _ := recordLoginFailure(byIP, managementLimitKeys("user-2", "192.0.2.9")); !blocked {
		t.Fatal("同IP多个用户应被来源维度限流")
	}
}

func TestManagementLimiterIgnoresForwardedHeaders(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	fixture.useManagementLimiter(auth.NewFailureLimiter(2, 10*time.Minute, 15*time.Minute, 100, func() time.Time { return fixture.now }))
	for attempt, forwardedIP := range []string{"198.51.100.1", "198.51.100.2"} {
		rr := fixture.managementRequestFromIP(http.MethodPost, "/api/security/2fa/setup", `{"password":"wrong"}`,
			"application/json", cookie, "192.0.2.50", forwardedIP)
		if attempt == 0 && rr.Code != http.StatusUnauthorized {
			t.Fatalf("首次错误密码 = %d %s", rr.Code, rr.Body.String())
		}
		if attempt == 1 && rr.Code != http.StatusTooManyRequests {
			t.Fatalf("伪造转发头后第二次错误密码 = %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestManagementLimiterDoesNotCountStoreOrCryptoFailures(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		prepare func(*loginTwoFactorFixture)
		path    string
		body    string
	}{
		{
			name: "store", path: "/api/security/2fa/setup", body: `{"password":"secret"}`,
			prepare: func(f *loginTwoFactorFixture) { _ = f.db.Close() },
		},
		{
			name: "crypto", path: "/api/security/2fa/recovery-codes", body: `{"password":"secret","code":"000000"}`,
			prepare: func(f *loginTwoFactorFixture) {
				if _, err := f.db.SQL.ExecContext(context.Background(),
					`update user_two_factor set secret_ciphertext = 'invalid' where user_id = ?`, testLoginUserID); err != nil {
					t.Fatalf("破坏测试密文失败：%v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newLoginTwoFactorFixture(t, testCase.name == "crypto")
			cookie := fixture.authenticatedCookie()
			limiter := auth.NewFailureLimiter(1, 10*time.Minute, 15*time.Minute, 100, func() time.Time { return fixture.now })
			fixture.useManagementLimiter(limiter)
			testCase.prepare(fixture)
			rr := fixture.managementRequest(http.MethodPost, testCase.path, testCase.body, "application/json", cookie)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("故障响应 = %d %s", rr.Code, rr.Body.String())
			}
			for _, key := range managementLimitKeys(testLoginUserID, testLoginIP) {
				if allowed, _ := limiter.Allow(key); !allowed {
					t.Fatalf("%s故障不应计入管理限流：%s", testCase.name, key)
				}
			}
		})
	}
}

func TestRequireUserDistinguishesMissingUserFromStoreFailure(t *testing.T) {
	t.Run("会话用户不存在返回未认证", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, false)
		cookie := fixture.authenticatedCookie()
		if _, err := fixture.db.SQL.ExecContext(context.Background(), `delete from users where id = ?`, testLoginUserID); err != nil {
			t.Fatalf("删除会话用户失败：%v", err)
		}

		rr := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
		if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "unauthorized" {
			t.Fatalf("会话用户不存在响应 = %d %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("用户存储故障返回认证不可用", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, false)
		cookie := fixture.authenticatedCookie()
		if _, err := fixture.db.SQL.ExecContext(context.Background(), `drop table users`); err != nil {
			t.Fatalf("删除用户表失败：%v", err)
		}

		rr := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
		if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "authentication_unavailable" {
			t.Fatalf("用户存储故障响应 = %d %s", rr.Code, rr.Body.String())
		}
	})
}

func TestTwoFactorManagementPOSTsReuseStrictBoundedJSONReader(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	for _, testCase := range []struct {
		name string
		path string
		body string
	}{
		{name: "setup 未知字段", path: "/api/security/2fa/setup", body: `{"password":"secret","extra":true}`},
		{name: "enable 重复字段", path: "/api/security/2fa/enable", body: `{"code":"000000","code":"111111"}`},
		{name: "轮换尾随内容", path: "/api/security/2fa/recovery-codes", body: `{"password":"secret","code":"000000"} trailing`},
		{name: "disable 多值", path: "/api/security/2fa/disable", body: `{"password":"secret"}{"password":"secret"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rr := fixture.managementRequest(http.MethodPost, testCase.path, testCase.body, "application/json", cookie)
			if rr.Code != http.StatusBadRequest || responseErrorCode(t, rr) != "invalid_json" {
				t.Fatalf("严格 JSON 管理响应 = %d %s", rr.Code, rr.Body.String())
			}
		})
	}

	nonJSON := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "text/plain", cookie)
	if nonJSON.Code != http.StatusUnsupportedMediaType || responseErrorCode(t, nonJSON) != "unsupported_media_type" {
		t.Fatalf("管理非 JSON 响应 = %d %s", nonJSON.Code, nonJSON.Body.String())
	}

	const limit = 16 << 10
	reader := &countingReader{reader: strings.NewReader(`{"password":"secret","extra":"` + strings.Repeat("x", limit*2) + `"}`)}
	req := httptest.NewRequest(http.MethodPost, "/api/security/2fa/setup", reader)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge || responseErrorCode(t, rr) != "request_too_large" {
		t.Fatalf("超大管理请求响应 = %d %s", rr.Code, rr.Body.String())
	}
	if reader.read > limit+1 {
		t.Fatalf("超大管理请求读取 %d 字节，期望不超过 %d", reader.read, limit+1)
	}
}

func TestTwoFactorStatusAndSetupStoreFailuresReturnUnavailable(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()
	if _, err := fixture.db.SQL.ExecContext(context.Background(), `drop table user_two_factor`); err != nil {
		t.Fatalf("删除二因素表失败：%v", err)
	}

	status := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
	if status.Code != http.StatusInternalServerError || responseErrorCode(t, status) != "two_factor_unavailable" {
		t.Fatalf("状态存储故障响应 = %d %s", status.Code, status.Body.String())
	}
	setup := fixture.managementRequest(http.MethodPost, "/api/security/2fa/setup",
		`{"password":"secret"}`, "application/json", cookie)
	if setup.Code != http.StatusInternalServerError || responseErrorCode(t, setup) != "two_factor_unavailable" {
		t.Fatalf("setup 存储故障响应 = %d %s", setup.Code, setup.Body.String())
	}
	if strings.Contains(setup.Body.String(), "manual_key") || strings.Contains(setup.Body.String(), "otpauth_uri") {
		t.Fatal("setup 存储故障不应泄露新密钥")
	}
}

func TestTwoFactorMutationStoreFailuresPreserveCommittedState(t *testing.T) {
	t.Run("enable", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, false)
		cookie := fixture.authenticatedCookie()
		fixture.beginManagementSetup(cookie)
		if _, err := fixture.db.SQL.ExecContext(context.Background(), `
			create trigger fail_two_factor_enable before update on user_two_factor
			begin select raise(fail, 'enable failed'); end`); err != nil {
			t.Fatalf("创建 enable 失败触发器失败：%v", err)
		}
		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
			`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
		if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
			t.Fatalf("enable 存储故障响应 = %d %s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "recovery_codes") {
			t.Fatal("enable 失败不应返回未提交恢复码")
		}
		if _, err := fixture.db.GetPendingTwoFactor(context.Background(), testLoginUserID, fixture.now); err != nil {
			t.Fatalf("enable 失败未保留 pending：%v", err)
		}
		remaining, err := fixture.db.CountRecoveryCodes(context.Background(), testLoginUserID)
		if err != nil || remaining != 0 {
			t.Fatalf("enable 失败写入恢复码：%d, %v", remaining, err)
		}
	})

	t.Run("recovery-codes", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, true)
		cookie := fixture.authenticatedCookie()
		fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)
		if _, err := fixture.db.SQL.ExecContext(context.Background(), `
			create trigger fail_recovery_rotation before update on user_two_factor
			begin select raise(fail, 'rotation failed'); end`); err != nil {
			t.Fatalf("创建轮换失败触发器失败：%v", err)
		}
		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
			`{"password":"secret","code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
		if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
			t.Fatalf("轮换存储故障响应 = %d %s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "recovery_codes") {
			t.Fatal("轮换失败不应返回未提交恢复码")
		}
		remaining, err := fixture.db.CountRecoveryCodes(context.Background(), testLoginUserID)
		if err != nil || remaining != 2 {
			t.Fatalf("轮换失败未保留旧恢复码：%d, %v", remaining, err)
		}
	})

	t.Run("disable", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, true)
		cookie := fixture.authenticatedCookie()
		if _, err := fixture.db.SQL.ExecContext(context.Background(), `
			create trigger fail_two_factor_disable before delete on user_two_factor
			begin select raise(fail, 'disable failed'); end`); err != nil {
			t.Fatalf("创建 disable 失败触发器失败：%v", err)
		}
		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
			`{"password":"secret"}`, "application/json", cookie)
		if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
			t.Fatalf("disable 存储故障响应 = %d %s", rr.Code, rr.Body.String())
		}
		if _, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID); err != nil {
			t.Fatalf("disable 失败未保留启用配置：%v", err)
		}
		remaining, err := fixture.db.CountRecoveryCodes(context.Background(), testLoginUserID)
		if err != nil || remaining != 2 {
			t.Fatalf("disable 失败未回滚恢复码删除：%d, %v", remaining, err)
		}
	})
}

func TestTwoFactorAuditFailuresKeepCommittedManagementResultAuthoritative(t *testing.T) {
	t.Run("enable still returns the only recovery code copy", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, false)
		cookie := fixture.authenticatedCookie()
		fixture.beginManagementSetup(cookie)
		writer := &recordingFailingAuditWriter{err: errors.New("审计不可用")}
		fixture.useAuditWriter(writer)

		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
			`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
		if rr.Code != http.StatusOK {
			t.Fatalf("审计失败时 enable 响应 = %d %s", rr.Code, rr.Body.String())
		}
		var response recoveryCodesResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || len(response.RecoveryCodes) != 10 {
			t.Fatalf("审计失败时 enable 恢复码 = %#v, %v", response, err)
		}
		if len(writer.events) != 1 || writer.events[0].EventType != "two_factor_enabled" {
			t.Fatalf("enable 审计尝试 = %#v", writer.events)
		}
		if _, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID); err != nil {
			t.Fatalf("审计失败后 enable 未提交：%v", err)
		}
	})

	t.Run("recovery rotation still returns the only new code copy", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, true)
		cookie := fixture.authenticatedCookie()
		fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)
		writer := &recordingFailingAuditWriter{err: errors.New("审计不可用")}
		fixture.useAuditWriter(writer)

		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
			`{"password":"secret","code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
		if rr.Code != http.StatusOK {
			t.Fatalf("审计失败时轮换响应 = %d %s", rr.Code, rr.Body.String())
		}
		var response recoveryCodesResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || len(response.RecoveryCodes) != 10 {
			t.Fatalf("审计失败时轮换恢复码 = %#v, %v", response, err)
		}
		if len(writer.events) != 1 || writer.events[0].EventType != "two_factor_recovery_codes_regenerated" {
			t.Fatalf("轮换审计尝试 = %#v", writer.events)
		}
		remaining, err := fixture.db.CountRecoveryCodes(context.Background(), testLoginUserID)
		if err != nil || remaining != 10 {
			t.Fatalf("审计失败后轮换未提交：%d, %v", remaining, err)
		}
	})

	t.Run("disable remains successful after commit", func(t *testing.T) {
		fixture := newLoginTwoFactorFixture(t, true)
		cookie := fixture.authenticatedCookie()
		writer := &recordingFailingAuditWriter{err: errors.New("审计不可用")}
		fixture.useAuditWriter(writer)

		rr := fixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
			`{"password":"secret"}`, "application/json", cookie)
		if rr.Code != http.StatusOK {
			t.Fatalf("审计失败时 disable 响应 = %d %s", rr.Code, rr.Body.String())
		}
		if len(writer.events) != 1 || writer.events[0].EventType != "two_factor_disabled" {
			t.Fatalf("disable 审计尝试 = %#v", writer.events)
		}
		if _, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("审计失败后 disable 状态 = %v", err)
		}
	})
}

func TestCommittedTwoFactorAuditIgnoresRequestCancellation(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	req := httptest.NewRequest(http.MethodPost, "/api/security/2fa/enable", nil).WithContext(requestContext)

	var observedErr error
	var deadline time.Time
	r := &router{audit: auditWriterFunc(func(ctx context.Context, event store.AuditEvent) error {
		observedErr = ctx.Err()
		deadline, _ = ctx.Deadline()
		return nil
	})}
	r.auditCommittedTwoFactorChange(req, testLoginUserID, "two_factor_enabled", "two factor authentication enabled")

	if observedErr != nil {
		t.Fatalf("提交后审计继承了请求取消：%v", observedErr)
	}
	remaining := time.Until(deadline)
	if remaining < 1500*time.Millisecond || remaining > 2100*time.Millisecond {
		t.Fatalf("提交后审计截止时间剩余 %s，期望约 2 秒", remaining)
	}
}

func TestCommittedTwoFactorAuditTimesOutAndLogsFailure(t *testing.T) {
	var logBuffer bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logBuffer)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	var observedErr error
	r := &router{audit: auditWriterFunc(func(ctx context.Context, event store.AuditEvent) error {
		<-ctx.Done()
		observedErr = ctx.Err()
		return ctx.Err()
	})}
	req := httptest.NewRequest(http.MethodPost, "/api/security/2fa/disable", nil)
	startedAt := time.Now()
	r.auditCommittedTwoFactorChange(req, testLoginUserID, "two_factor_disabled", "two factor authentication disabled")
	elapsed := time.Since(startedAt)

	if !errors.Is(observedErr, context.DeadlineExceeded) {
		t.Fatalf("提交后审计超时错误 = %v", observedErr)
	}
	if elapsed < 1800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("提交后审计耗时 = %s，期望约 2 秒", elapsed)
	}
	if !strings.Contains(logBuffer.String(), "two_factor_disabled") || !strings.Contains(logBuffer.String(), context.DeadlineExceeded.Error()) {
		t.Fatalf("提交后审计失败日志 = %q", logBuffer.String())
	}
}

func TestTwoFactorCompleteManagementLifecycle(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	cookie := fixture.authenticatedCookie()

	initialStatus := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
	var status twoFactorStatusResponse
	if initialStatus.Code != http.StatusOK || json.Unmarshal(initialStatus.Body.Bytes(), &status) != nil || status.Enabled {
		t.Fatalf("初始二因素状态 = %d %s", initialStatus.Code, initialStatus.Body.String())
	}
	fixture.beginManagementSetup(cookie)
	enable := fixture.managementRequest(http.MethodPost, "/api/security/2fa/enable",
		`{"code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	var enabledCodes recoveryCodesResponse
	if enable.Code != http.StatusOK || json.Unmarshal(enable.Body.Bytes(), &enabledCodes) != nil || len(enabledCodes.RecoveryCodes) != 10 {
		t.Fatalf("完整流程 enable = %d %s", enable.Code, enable.Body.String())
	}
	enabledStatus := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
	status = twoFactorStatusResponse{}
	if enabledStatus.Code != http.StatusOK || json.Unmarshal(enabledStatus.Body.Bytes(), &status) != nil || !status.Enabled || status.RecoveryCodesRemaining != 10 {
		t.Fatalf("完整流程启用状态 = %d %s", enabledStatus.Code, enabledStatus.Body.String())
	}

	fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)
	rotation := fixture.managementRequest(http.MethodPost, "/api/security/2fa/recovery-codes",
		`{"password":"secret","code":"`+fixture.currentTOTP()+`"}`, "application/json", cookie)
	var rotatedCodes recoveryCodesResponse
	if rotation.Code != http.StatusOK || json.Unmarshal(rotation.Body.Bytes(), &rotatedCodes) != nil || len(rotatedCodes.RecoveryCodes) != 10 {
		t.Fatalf("完整流程恢复码轮换 = %d %s", rotation.Code, rotation.Body.String())
	}
	disable := fixture.managementRequest(http.MethodPost, "/api/security/2fa/disable",
		`{"password":"secret"}`, "application/json", cookie)
	if disable.Code != http.StatusOK {
		t.Fatalf("完整流程 disable = %d %s", disable.Code, disable.Body.String())
	}
	finalStatus := fixture.managementRequest(http.MethodGet, "/api/security/2fa", "", "", cookie)
	status = twoFactorStatusResponse{}
	if finalStatus.Code != http.StatusOK || json.Unmarshal(finalStatus.Body.Bytes(), &status) != nil || status.Enabled || status.RecoveryCodesRemaining != 0 {
		t.Fatalf("完整流程最终状态 = %d %s", finalStatus.Code, finalStatus.Body.String())
	}
	passwordLogin := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if passwordLogin.Code != http.StatusOK {
		t.Fatalf("完整流程关闭后密码登录 = %d %s", passwordLogin.Code, passwordLogin.Body.String())
	}
	for _, eventType := range []string{
		"two_factor_enabled",
		"two_factor_recovery_codes_regenerated",
		"two_factor_disabled",
	} {
		var count int
		if err := fixture.db.SQL.QueryRowContext(context.Background(),
			`select count(*) from audit_events where event_type = ? and user_id = ?`,
			eventType, testLoginUserID).Scan(&count); err != nil {
			t.Fatalf("查询完整流程审计 %s 失败：%v", eventType, err)
		}
		if count != 1 {
			t.Fatalf("完整流程审计 %s 数量 = %d", eventType, count)
		}
	}
}

func TestPasswordLoginWithEnabledTwoFactorReturnsChallengeWithoutSession(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	rr := fixture.passwordLogin(testLoginUsername, testLoginPassword)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("状态码 = %d，期望 %d；响应=%s", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("密码阶段不应设置会话 Cookie")
	}
	var response loginChallengeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if !response.TwoFactorRequired || response.ChallengeToken == "" || response.ExpiresIn != 300 {
		t.Fatalf("登录挑战响应不完整：%#v", response)
	}
}

func TestChallengePersistenceFailureDoesNotReturnChallenge(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	if _, err := fixture.db.SQL.ExecContext(context.Background(), `
		create trigger fail_login_challenge_insert
		before insert on login_challenges
		begin
			select raise(fail, 'challenge persistence failed');
		end`); err != nil {
		t.Fatalf("创建挑战持久化失败触发器失败：%v", err)
	}

	rr := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("挑战持久化失败状态码 = %d，期望 500；响应=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "challenge_token") {
		t.Fatalf("挑战持久化失败不应返回 challenge：%s", rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestTamperedLoginChallengeRequiresLoginRestart(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	tampered := challenge[:len(challenge)-1] + "x"

	rr := fixture.secondFactor(tampered, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "login_restart_required" {
		t.Fatalf("篡改挑战响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestNewLoginChallengeInvalidatesPreviousToken(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	oldChallenge := fixture.beginTwoFactorLogin().ChallengeToken
	newChallenge := fixture.beginTwoFactorLogin().ChallengeToken

	oldResponse := fixture.secondFactor(oldChallenge, fixture.currentTOTP(), "application/json")
	if oldResponse.Code != http.StatusUnauthorized || responseErrorCode(t, oldResponse) != "login_restart_required" {
		t.Fatalf("旧challenge响应 = %d %s", oldResponse.Code, oldResponse.Body.String())
	}
	newResponse := fixture.secondFactor(newChallenge, fixture.currentTOTP(), "application/json")
	if newResponse.Code != http.StatusOK {
		t.Fatalf("新challenge响应 = %d %s", newResponse.Code, newResponse.Body.String())
	}
}

func TestValidTOTPCompletesLoginAndSetsSession(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("完成二因素登录后应设置会话 Cookie")
	}
}

func TestTOTPReplayIsRejected(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	code := fixture.currentTOTP()
	if rr := fixture.secondFactor(challenge, code, "application/json"); rr.Code != http.StatusOK {
		t.Fatalf("首次 TOTP 状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}

	rr := fixture.secondFactor(challenge, code, "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "login_restart_required" {
		t.Fatalf("重放 TOTP 状态码 = %d，期望 %d；响应=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func TestConsumedChallengeRejectsNextTOTPStep(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json"); rr.Code != http.StatusOK {
		t.Fatalf("首次 TOTP 状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	fixture.now = fixture.now.Add(time.Duration(auth.TOTPPeriodSeconds) * time.Second)

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "login_restart_required" {
		t.Fatalf("已消费挑战的下一时间片 TOTP 响应 = %d %s", rr.Code, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestConsumedChallengeRejectsAnotherRecoveryCode(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if rr := fixture.secondFactor(challenge, fixture.recoveryCode, "application/json"); rr.Code != http.StatusOK {
		t.Fatalf("首次恢复码状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}

	rr := fixture.secondFactor(challenge, fixture.secondRecoveryCode, "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "login_restart_required" {
		t.Fatalf("已消费挑战的第二枚恢复码响应 = %d %s", rr.Code, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestConcurrentChallengeUseCreatesExactlyOneSession(t *testing.T) {
	fixture := newConcurrentLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	synchronizeConcurrentSecondFactorNow(fixture)
	start := make(chan struct{})
	type result struct {
		code string
		rr   *httptest.ResponseRecorder
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, code := range []string{fixture.recoveryCode, fixture.secondRecoveryCode} {
		code := code
		go func() {
			ready.Done()
			<-start
			results <- result{code: code, rr: fixture.secondFactor(challenge, code, "application/json")}
		}()
	}
	ready.Wait()
	close(start)

	var successes int
	var restarts int
	var loserCode string
	for range 2 {
		got := <-results
		rr := got.rr
		switch rr.Code {
		case http.StatusOK:
			successes++
			if len(rr.Result().Cookies()) == 0 {
				t.Fatal("成功响应缺少会话 Cookie")
			}
		case http.StatusUnauthorized:
			restarts++
			loserCode = got.code
			if responseErrorCode(t, rr) != "login_restart_required" {
				t.Fatalf("并发失败响应 = %s", rr.Body.String())
			}
			assertNoSessionCookie(t, rr)
		default:
			t.Fatalf("并发挑战响应状态码 = %d，响应=%s", rr.Code, rr.Body.String())
		}
	}
	if successes != 1 || restarts != 1 {
		t.Fatalf("并发挑战结果：成功=%d，重启=%d", successes, restarts)
	}
	fixture.router().now = func() time.Time { return fixture.now }
	newChallenge := fixture.beginTwoFactorLogin().ChallengeToken
	rr := fixture.secondFactor(newChallenge, loserCode, "application/json")
	if rr.Code != http.StatusOK {
		t.Fatalf("并发失败方恢复码未回滚：状态码=%d，响应=%s", rr.Code, rr.Body.String())
	}
}

func TestConcurrentTOTPAndRecoveryUseRollsBackLosingFactor(t *testing.T) {
	fixture := newConcurrentLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	synchronizeConcurrentSecondFactorNow(fixture)
	type factor struct {
		kind string
		code string
	}
	factors := []factor{
		{kind: "totp", code: fixture.currentTOTP()},
		{kind: "recovery", code: fixture.recoveryCode},
	}
	type result struct {
		factor factor
		rr     *httptest.ResponseRecorder
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, candidate := range factors {
		candidate := candidate
		go func() {
			ready.Done()
			<-start
			results <- result{factor: candidate, rr: fixture.secondFactor(challenge, candidate.code, "application/json")}
		}()
	}
	ready.Wait()
	close(start)

	var successes int
	var loser factor
	for range 2 {
		got := <-results
		switch got.rr.Code {
		case http.StatusOK:
			successes++
		case http.StatusUnauthorized:
			if responseErrorCode(t, got.rr) != "login_restart_required" {
				t.Fatalf("混合并发失败响应 = %s", got.rr.Body.String())
			}
			loser = got.factor
		default:
			t.Fatalf("混合并发状态码 = %d，响应=%s", got.rr.Code, got.rr.Body.String())
		}
	}
	if successes != 1 || loser.kind == "" {
		t.Fatalf("混合并发结果：成功=%d，失败方=%#v", successes, loser)
	}
	fixture.router().now = func() time.Time { return fixture.now }
	newChallenge := fixture.beginTwoFactorLogin().ChallengeToken
	rr := fixture.secondFactor(newChallenge, loser.code, "application/json")
	if rr.Code != http.StatusOK {
		t.Fatalf("混合并发失败方 %s 未回滚：状态码=%d，响应=%s", loser.kind, rr.Code, rr.Body.String())
	}
}

func TestChangedConfigurationRejectsOldChallenge(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if _, err := fixture.db.SQL.ExecContext(context.Background(),
		`update user_two_factor set configuration_id = ? where user_id = ?`,
		"configuration-2", testLoginUserID); err != nil {
		t.Fatalf("修改双因素配置标识失败：%v", err)
	}

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "login_restart_required" {
		t.Fatalf("旧挑战响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestLoginSecondFactorTransactionRestartsWhenConfigurationRotatesAfterRead(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	token := fixture.beginTwoFactorLogin().ChallengeToken
	challenge, err := fixture.manager.VerifyLoginChallengeAt(token, fixture.now)
	if err != nil {
		t.Fatalf("验证登录挑战失败：%v", err)
	}
	setting, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID)
	if err != nil {
		t.Fatalf("读取旧双因素配置失败：%v", err)
	}
	verified, err := fixture.router().verifyLoginCode(setting, fixture.currentTOTP(), fixture.now)
	if err != nil {
		t.Fatalf("验证 TOTP 失败：%v", err)
	}
	if _, err := fixture.db.SQL.ExecContext(context.Background(),
		`update user_two_factor set configuration_id = ? where user_id = ?`,
		"configuration-rotated", testLoginUserID); err != nil {
		t.Fatalf("轮换双因素配置失败：%v", err)
	}

	err = fixture.db.ConsumeLoginSecondFactor(context.Background(), store.ConsumeLoginSecondFactorParams{
		ChallengeJTI:    challenge.JTI,
		UserID:          challenge.UserID,
		ConfigurationID: challenge.ConfigurationID,
		TOTPCounter:     verified.totpCounter,
		Now:             fixture.now,
	})
	if !errors.Is(err, store.ErrLoginRestartRequired) {
		t.Fatalf("配置轮换后的校验错误 = %v，期望 login restart", err)
	}
	assertLoginChallengeUnconsumed(t, fixture.db, challenge.JTI)
}

func TestLoginSecondFactorTransactionRestartsWhenConfigurationDisabledAfterRead(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	token := fixture.beginTwoFactorLogin().ChallengeToken
	challenge, err := fixture.manager.VerifyLoginChallengeAt(token, fixture.now)
	if err != nil {
		t.Fatalf("验证登录挑战失败：%v", err)
	}
	setting, err := fixture.db.GetEnabledTwoFactor(context.Background(), testLoginUserID)
	if err != nil {
		t.Fatalf("读取旧双因素配置失败：%v", err)
	}
	verified, err := fixture.router().verifyLoginCode(setting, fixture.currentTOTP(), fixture.now)
	if err != nil {
		t.Fatalf("验证 TOTP 失败：%v", err)
	}
	if err := fixture.db.DisableTwoFactor(context.Background(), testLoginUserID); err != nil {
		t.Fatalf("禁用双因素配置失败：%v", err)
	}

	err = fixture.db.ConsumeLoginSecondFactor(context.Background(), store.ConsumeLoginSecondFactorParams{
		ChallengeJTI:    challenge.JTI,
		UserID:          challenge.UserID,
		ConfigurationID: challenge.ConfigurationID,
		TOTPCounter:     verified.totpCounter,
		Now:             fixture.now,
	})
	if !errors.Is(err, store.ErrLoginRestartRequired) {
		t.Fatalf("配置禁用后的校验错误 = %v，期望 login restart", err)
	}
	assertLoginChallengeUnconsumed(t, fixture.db, challenge.JTI)
}

func TestSecondFactorRequestCapturesNowOnce(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	r := fixture.router()
	calls := 0
	r.now = func() time.Time {
		calls++
		return fixture.now
	}

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusOK {
		t.Fatalf("二步登录状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	if calls != 1 {
		t.Fatalf("一次二步请求读取路由时钟 %d 次，期望 1 次", calls)
	}
}

func TestCorruptedTwoFactorCiphertextReturnsUnavailable(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if _, err := fixture.db.SQL.ExecContext(context.Background(),
		`update user_two_factor set secret_ciphertext = ? where user_id = ?`,
		"corrupted", testLoginUserID); err != nil {
		t.Fatalf("损坏双因素密文失败：%v", err)
	}

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
		t.Fatalf("密文损坏响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestFifthPasswordFailureIsRateLimited(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	for attempt := 1; attempt <= 6; attempt++ {
		rr := fixture.passwordLogin(testLoginUsername, "wrong-password")
		want := http.StatusUnauthorized
		if attempt >= 5 {
			want = http.StatusTooManyRequests
		}
		if rr.Code != want {
			t.Fatalf("第 %d 次密码失败状态码 = %d，期望 %d；响应=%s", attempt, rr.Code, want, rr.Body.String())
		}
		assertNoSessionCookie(t, rr)
		if attempt >= 5 {
			if rr.Header().Get("Retry-After") == "" {
				t.Fatal("密码限流响应缺少 Retry-After")
			}
			if code := responseErrorCode(t, rr); code != "too_many_attempts" {
				t.Fatalf("密码限流错误码 = %q，期望 too_many_attempts", code)
			}
		}
		audits := loginRateLimitAudits(t, fixture.db)
		wantAudits := 0
		if attempt >= 5 {
			wantAudits = 1
		}
		if len(audits) != wantAudits {
			t.Fatalf("第 %d 次密码失败后的限流审计数 = %d，期望 %d", attempt, len(audits), wantAudits)
		}
		if len(audits) == 1 {
			if len(audits[0]) != 2 || audits[0]["stage"] != "password" || audits[0]["source_ip"] != testLoginIP {
				t.Fatalf("密码限流审计元数据 = %#v", audits[0])
			}
		}
	}
}

func TestPasswordFailuresAcrossRotatingIPsTriggerAccountLimit(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	for attempt := 1; attempt <= 5; attempt++ {
		remoteIP := fmt.Sprintf("198.51.100.%d", attempt)
		rr := fixture.passwordLoginFromIP(testLoginUsername, "wrong-password", remoteIP, "")
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if rr.Code != want {
			t.Fatalf("轮换 IP 的第 %d 次密码失败状态码 = %d，期望 %d；响应=%s", attempt, rr.Code, want, rr.Body.String())
		}
		assertNoSessionCookie(t, rr)
	}
}

func TestPasswordFailuresUseDirectSourceIPInsteadOfForwardedHeader(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	const directIP = "192.0.2.44"
	for attempt := 1; attempt <= 5; attempt++ {
		username := fmt.Sprintf("missing-user-%d", attempt)
		forwardedIP := fmt.Sprintf("198.51.100.%d", attempt)
		rr := fixture.passwordLoginFromIP(username, "wrong-password", directIP, forwardedIP)
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if rr.Code != want {
			t.Fatalf("直连来源第 %d 次密码失败状态码 = %d，期望 %d；响应=%s", attempt, rr.Code, want, rr.Body.String())
		}
		assertNoSessionCookie(t, rr)
	}
	audits := loginRateLimitAudits(t, fixture.db)
	if len(audits) != 1 || audits[0]["source_ip"] != directIP {
		t.Fatalf("直连来源限流审计 = %#v，期望 source_ip=%s", audits, directIP)
	}
}

func TestFifthSecondFactorFailureIsRateLimited(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	for attempt := 1; attempt <= 6; attempt++ {
		rr := fixture.secondFactor(challenge, "abcdef", "application/json")
		want := http.StatusUnauthorized
		if attempt >= 5 {
			want = http.StatusTooManyRequests
		}
		if rr.Code != want {
			t.Fatalf("第 %d 次二因素失败状态码 = %d，期望 %d；响应=%s", attempt, rr.Code, want, rr.Body.String())
		}
		assertNoSessionCookie(t, rr)
		if attempt >= 5 {
			if rr.Header().Get("Retry-After") == "" {
				t.Fatal("二因素限流响应缺少 Retry-After")
			}
			if code := responseErrorCode(t, rr); code != "too_many_attempts" {
				t.Fatalf("二因素限流错误码 = %q，期望 too_many_attempts", code)
			}
		}
		audits := loginRateLimitAudits(t, fixture.db)
		wantAudits := 0
		if attempt >= 5 {
			wantAudits = 1
		}
		if len(audits) != wantAudits {
			t.Fatalf("第 %d 次二因素失败后的限流审计数 = %d，期望 %d", attempt, len(audits), wantAudits)
		}
		if len(audits) == 1 {
			if len(audits[0]) != 2 || audits[0]["stage"] != "second_factor" || audits[0]["source_ip"] != testLoginIP {
				t.Fatalf("二因素限流审计元数据 = %#v", audits[0])
			}
		}
	}
}

func TestSecondFactorFailuresAcrossRotatingIPsTriggerUserLimit(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	for attempt := 1; attempt <= 5; attempt++ {
		remoteIP := fmt.Sprintf("198.51.100.%d", attempt)
		rr := fixture.secondFactorFromIP(challenge, "abcdef", "application/json", remoteIP)
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if rr.Code != want {
			t.Fatalf("轮换 IP 的第 %d 次二因素失败状态码 = %d，期望 %d；响应=%s", attempt, rr.Code, want, rr.Body.String())
		}
		assertNoSessionCookie(t, rr)
	}
}

func TestInvalidTOTPUsesExactErrorCode(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(challenge, "abcdef", "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "invalid_two_factor_code" {
		t.Fatalf("无效 TOTP 响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestInvalidRecoveryCodeUsesExactErrorCode(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(challenge, "WRONG-RECOVERY-CODE", "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "invalid_two_factor_code" {
		t.Fatalf("无效恢复码响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestSecondFactorLoginRejectsNonJSONContentType(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "text/plain")
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("状态码 = %d，期望 %d；响应=%s", rr.Code, http.StatusUnsupportedMediaType, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestPasswordLoginRejectsNonJSONContentType(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.RemoteAddr = testLoginIP + ":43210"
	rr := httptest.NewRecorder()

	fixture.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("密码登录非 JSON 状态码 = %d，期望 415；响应=%s", rr.Code, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestLoginJSONRejectsUnknownFieldsMultipleValuesAndTrailingContent(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "密码未知字段",
			path: "/api/login",
			body: `{"username":"admin","password":"secret","unexpected":true}`,
		},
		{
			name: "密码连续两个值",
			path: "/api/login",
			body: `{"username":"admin","password":"secret"}{"extra":true}`,
		},
		{
			name: "密码尾随垃圾",
			path: "/api/login",
			body: `{"username":"admin","password":"secret"} trailing`,
		},
		{
			name: "密码重复字段",
			path: "/api/login",
			body: `{"username":"ignored","username":"admin","password":"secret"}`,
		},
		{
			name: "二因素未知字段",
			path: "/api/login/2fa",
			body: `{"challenge_token":"invalid","code":"123456","unexpected":true}`,
		},
		{
			name: "二因素连续两个值",
			path: "/api/login/2fa",
			body: `{"challenge_token":"invalid","code":"123456"}{"extra":true}`,
		},
		{
			name: "二因素尾随垃圾",
			path: "/api/login/2fa",
			body: `{"challenge_token":"invalid","code":"123456"} trailing`,
		},
		{
			name: "二因素重复字段",
			path: "/api/login/2fa",
			body: `{"challenge_token":"invalid","code":"000000","code":"123456"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = testLoginIP + ":43210"
			rr := httptest.NewRecorder()

			fixture.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest || responseErrorCode(t, rr) != "invalid_json" {
				t.Fatalf("严格 JSON 响应 = %d %s", rr.Code, rr.Body.String())
			}
			assertNoSessionCookie(t, rr)
		})
	}
}

func TestLoginJSONRejectsOversizedBodiesWithoutUnboundedRead(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	const limit = 16 << 10
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "密码字段过大",
			path: "/api/login",
			body: `{"username":"admin","password":"` + strings.Repeat("x", limit*2) + `"}`,
		},
		{
			name: "二因素未知字段过大",
			path: "/api/login/2fa",
			body: `{"challenge_token":"invalid","code":"123456","unexpected":"` + strings.Repeat("x", limit*2) + `"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &countingReader{reader: strings.NewReader(tt.body)}
			req := httptest.NewRequest(http.MethodPost, tt.path, reader)
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = testLoginIP + ":43210"
			rr := httptest.NewRecorder()

			fixture.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusRequestEntityTooLarge || responseErrorCode(t, rr) != "request_too_large" {
				t.Fatalf("超大登录请求响应 = %d %s", rr.Code, rr.Body.String())
			}
			if reader.read > limit+1 {
				t.Fatalf("超大登录请求读取 %d 字节，期望不超过 %d", reader.read, limit+1)
			}
		})
	}
}

func TestLoginAuditIsWrittenOnlyAfterSecondFactorCompletion(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	var count int
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select count(*) from audit_events where event_type = 'login'`).Scan(&count); err != nil {
		t.Fatalf("查询密码阶段登录审计失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("密码阶段登录审计数量 = %d，期望 0", count)
	}

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusOK {
		t.Fatalf("完成登录状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var metadataJSON string
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select metadata_json from audit_events where event_type = 'login'`).Scan(&metadataJSON); err != nil {
		t.Fatalf("查询完成登录审计失败：%v", err)
	}
	var metadata map[string]string
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("解析登录审计元数据失败：%v", err)
	}
	if len(metadata) != 1 || metadata["method"] != "totp" {
		t.Fatalf("登录审计元数据 = %#v，期望仅 method=totp", metadata)
	}
}

func TestPasswordLoginAuditContainsOnlyPasswordMethod(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	rr := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code != http.StatusOK {
		t.Fatalf("密码登录状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var metadataJSON string
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select metadata_json from audit_events where event_type = 'login'`).Scan(&metadataJSON); err != nil {
		t.Fatalf("查询密码登录审计失败：%v", err)
	}
	if metadataJSON != `{"method":"password"}` {
		t.Fatalf("密码登录审计元数据 = %s", metadataJSON)
	}
}

func TestAuditFailurePreventsSessionCookie(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	fixture.handler = NewRouter(Deps{
		Store:     fixture.db,
		Sessions:  auth.NewSessionManager(testLoginRootKey, time.Hour),
		TwoFactor: fixture.manager,
		Audit:     failingAuditWriter{err: errors.New("审计写入失败")},
		Now:       func() time.Time { return fixture.now },
	})

	rr := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("审计失败状态码 = %d，期望 500；响应=%s", rr.Code, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
}

func TestPasswordLoginStoreFailureReturnsUnavailableWithoutCountingFailure(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, false)
	if err := fixture.db.Close(); err != nil {
		t.Fatalf("关闭测试数据库失败：%v", err)
	}
	limiter := auth.NewFailureLimiter(1, 10*time.Minute, 15*time.Minute, 10, func() time.Time { return fixture.now })
	fixture.handler = NewRouter(Deps{
		Store:           fixture.db,
		Sessions:        auth.NewSessionManager(testLoginRootKey, time.Hour),
		TwoFactor:       fixture.manager,
		PasswordLimiter: limiter,
		Now:             func() time.Time { return fixture.now },
	})

	rr := fixture.passwordLogin(testLoginUsername, testLoginPassword)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("密码阶段存储故障状态码 = %d，期望 500；响应=%s", rr.Code, rr.Body.String())
	}
	assertNoSessionCookie(t, rr)
	for _, key := range passwordLoginLimitKeys(testLoginUsername, testLoginIP) {
		if allowed, _ := limiter.Allow(key); !allowed {
			t.Fatalf("密码阶段存储故障不应计入失败限流：%s", key)
		}
	}
}

func TestSecondFactorUserStoreFailureReturnsUnavailable(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if err := fixture.db.Close(); err != nil {
		t.Fatalf("关闭测试数据库失败：%v", err)
	}

	rr := fixture.secondFactor(challenge, fixture.currentTOTP(), "application/json")
	if rr.Code != http.StatusInternalServerError || responseErrorCode(t, rr) != "two_factor_unavailable" {
		t.Fatalf("二步用户存储故障响应 = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRecoveryCodeCompletesLogin(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(challenge, strings.ToLower(fixture.recoveryCode), "application/json; charset=utf-8")
	if rr.Code != http.StatusOK {
		t.Fatalf("恢复码登录状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	var metadataJSON string
	if err := fixture.db.SQL.QueryRowContext(context.Background(),
		`select metadata_json from audit_events where event_type = 'login'`).Scan(&metadataJSON); err != nil {
		t.Fatalf("查询恢复码登录审计失败：%v", err)
	}
	if metadataJSON != `{"method":"recovery_code"}` {
		t.Fatalf("恢复码登录审计元数据 = %s", metadataJSON)
	}
}

func TestRecoveryCodeLoginAuditFailureRollsBackChallengeAndCode(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	challenge := fixture.beginTwoFactorLogin().ChallengeToken
	if _, err := fixture.db.SQL.ExecContext(context.Background(), `
		create trigger fail_two_factor_login_audit
		before insert on audit_events
		when new.event_type = 'login'
		begin select raise(fail, 'login audit failed'); end`); err != nil {
		t.Fatalf("创建登录审计失败触发器失败：%v", err)
	}

	failed := fixture.secondFactor(challenge, fixture.recoveryCode, "application/json")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("审计失败状态码 = %d，期望 500；响应=%s", failed.Code, failed.Body.String())
	}
	assertNoSessionCookie(t, failed)
	if _, err := fixture.db.SQL.ExecContext(context.Background(), `drop trigger fail_two_factor_login_audit`); err != nil {
		t.Fatalf("删除登录审计失败触发器失败：%v", err)
	}

	retry := fixture.secondFactor(challenge, fixture.recoveryCode, "application/json")
	if retry.Code != http.StatusOK {
		t.Fatalf("审计恢复后重试状态码 = %d，期望 200；响应=%s", retry.Code, retry.Body.String())
	}
}

func TestRecoveryCodeReplayWithNewChallengeIsRejected(t *testing.T) {
	fixture := newLoginTwoFactorFixture(t, true)
	firstChallenge := fixture.beginTwoFactorLogin().ChallengeToken
	if rr := fixture.secondFactor(firstChallenge, fixture.recoveryCode, "application/json"); rr.Code != http.StatusOK {
		t.Fatalf("首次恢复码状态码 = %d，响应=%s", rr.Code, rr.Body.String())
	}
	secondChallenge := fixture.beginTwoFactorLogin().ChallengeToken

	rr := fixture.secondFactor(secondChallenge, fixture.recoveryCode, "application/json")
	if rr.Code != http.StatusUnauthorized || responseErrorCode(t, rr) != "invalid_two_factor_code" {
		t.Fatalf("恢复码重放响应 = %d %s", rr.Code, rr.Body.String())
	}
}
