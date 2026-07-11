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
	"net/http"
	"net/http/httptest"
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

type failingAuditWriter struct {
	err error
}

func (w failingAuditWriter) Log(context.Context, store.AuditEvent) error {
	return w.err
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
	if _, err := fixture.db.GetActiveLoginChallenge(context.Background(), challenge.JTI, challenge.UserID, challenge.ConfigurationID, fixture.now); err != nil {
		t.Fatalf("配置轮换后 challenge 应回滚为可用：%v", err)
	}
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
	if _, err := fixture.db.GetActiveLoginChallenge(context.Background(), challenge.JTI, challenge.UserID, challenge.ConfigurationID, fixture.now); err != nil {
		t.Fatalf("配置禁用后 challenge 应回滚为可用：%v", err)
	}
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
