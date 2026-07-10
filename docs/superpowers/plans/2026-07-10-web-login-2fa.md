# 可选 Web 登录 TOTP 2FA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为单管理员 Web 登录增加默认关闭、可在界面启停的 TOTP 2FA，并提供一次性恢复码、登录限流和防重放保护。

**Architecture:** 密码验证成功后，未启用 2FA 的用户沿用现有 Cookie 登录；已启用用户获得 5 分钟签名挑战令牌，只有 TOTP 或恢复码验证成功后才签发正式 Cookie。TOTP 密钥使用 AES-GCM 加密，恢复码只保存 HMAC，SQLite 事务负责 TOTP 计数器和恢复码的原子消费。

**Tech Stack:** Go 标准库、golang.org/x/crypto、SQLite、React 18、TypeScript、Vitest、Testing Library、qrcode.react。

**Design:** `docs/superpowers/specs/2026-07-10-web-login-2fa-design.md`

---

## 文件职责映射

- 新建 `server/internal/auth/totp.go`：TOTP 密钥、URI、验证码和时间计数器。
- 新建 `server/internal/auth/two_factor_crypto.go`：用途密钥派生、AES-GCM、恢复码。
- 新建 `server/internal/auth/challenge.go`：登录挑战和统一的 TwoFactorManager。
- 新建 `server/internal/auth/limiter.go`：有容量上限的失败限流器。
- 修改 `server/internal/store/store.go`：2FA 表、模型和事务操作。
- 新建 `server/internal/httpapi/router_2fa.go`：第二步登录和 2FA 管理接口。
- 修改 `server/internal/httpapi/router.go`：依赖、路由和密码登录分流。
- 修改 `server/cmd/server/main.go`：构造 2FA 管理器。
- 修改 `web/src/api.ts`：联合登录响应、结构化错误和安全设置 API。
- 修改 `web/src/components/LoginView.tsx`：密码、TOTP、恢复码状态机。
- 新建 `web/src/components/SecurityView.tsx`：启用、恢复码和关闭流程。
- 修改 `web/src/App.tsx`：登录回调和 Security 导航。
- 修改 `web/src/styles.css`：登录第二步和安全设置页面样式。
- 修改 `README.md`：部署、时钟、恢复码和 session_secret 注意事项。

### Task 1: TOTP 原语

**Files:**
- Create: `server/internal/auth/totp.go`
- Create: `server/internal/auth/totp_test.go`

- [ ] **Step 1: 编写失败测试**

创建 `server/internal/auth/totp_test.go`：

~~~go
package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestGenerateTOTPSecretReturnsTwentyRandomBytes(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	if len(decoded) != 20 {
		t.Fatalf("decoded length = %d, want 20", len(decoded))
	}
}

func TestMatchTOTPAcceptsAdjacentWindowAndRejectsReplayCandidate(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("01234567890123456789"))
	now := time.Unix(1_700_000_000, 0).UTC()
	previousCounter := now.Unix()/TOTPPeriodSeconds - 1
	code := hotpCode([]byte("01234567890123456789"), uint64(previousCounter))

	counter, ok, err := MatchTOTP(secret, code, now)
	if err != nil {
		t.Fatalf("MatchTOTP() error = %v", err)
	}
	if !ok || counter != previousCounter {
		t.Fatalf("counter = %d, ok = %v, want %d, true", counter, ok, previousCounter)
	}

	if _, ok, err := MatchTOTP(secret, "12345", now); err != nil || ok {
		t.Fatalf("invalid format ok = %v, err = %v", ok, err)
	}
}

func TestTOTPProvisioningURIContainsIssuerAndAccount(t *testing.T) {
	uri := TOTPProvisioningURI("vibe-terminal", "admin@example.com", "ABCDEF")
	if !strings.Contains(uri, "otpauth://totp/vibe-terminal:admin%40example.com") {
		t.Fatalf("uri = %q", uri)
	}
	if !strings.Contains(uri, "issuer=vibe-terminal") || !strings.Contains(uri, "period=30") {
		t.Fatalf("uri = %q", uri)
	}
}
~~~

- [ ] **Step 2: 运行测试并确认因缺少实现而失败**

Run: `cd server && go test ./internal/auth -run 'TestGenerateTOTPSecret|TestMatchTOTP|TestTOTPProvisioningURI' -v`

Expected: FAIL，提示 `GenerateTOTPSecret`、`MatchTOTP` 或 `TOTPProvisioningURI` 未定义。

- [ ] **Step 3: 编写最小 TOTP 实现**

创建 `server/internal/auth/totp.go`：

~~~go
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const TOTPPeriodSeconds int64 = 30

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return totpBase32.EncodeToString(raw), nil
}

func TOTPProvisioningURI(issuer string, account string, secret string) string {
	escapeLabel := func(value string) string {
		return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
	}
	label := escapeLabel(issuer) + ":" + escapeLabel(account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	return "otpauth://totp/" + label + "?" + query.Encode()
}

func MatchTOTP(secret string, code string, now time.Time) (int64, bool, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false, nil
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			return 0, false, nil
		}
	}
	key, err := totpBase32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return 0, false, err
	}
	current := now.UTC().Unix() / TOTPPeriodSeconds
	for offset := int64(-1); offset <= 1; offset++ {
		counter := current + offset
		expected := hotpCode(key, uint64(counter))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return counter, true, nil
		}
	}
	return 0, false, nil
}

func hotpCode(key []byte, counter uint64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
~~~

- [ ] **Step 4: 运行认证包测试并确认通过**

Run: `cd server && go test ./internal/auth -run 'TestGenerateTOTPSecret|TestMatchTOTP|TestTOTPProvisioningURI' -v`

Expected: PASS。

- [ ] **Step 5: 提交**

~~~bash
git add server/internal/auth/totp.go server/internal/auth/totp_test.go
git commit -m "feat(auth): add totp primitives"
~~~

### Task 2: TOTP 密钥加密和恢复码

**Files:**
- Create: `server/internal/auth/two_factor_crypto.go`
- Create: `server/internal/auth/two_factor_crypto_test.go`

- [ ] **Step 1: 编写失败测试**

创建 `server/internal/auth/two_factor_crypto_test.go`：

~~~go
package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSecretCipherRoundTripAndTamperDetection(t *testing.T) {
	key, err := derivePurposeKey([]byte("0123456789abcdef0123456789abcdef"), "test")
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	cipher, err := newSecretCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	encrypted, err := cipher.Encrypt("BASE32SECRET")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil || decrypted != "BASE32SECRET" {
		t.Fatalf("decrypted = %q, err = %v", decrypted, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, encryptedSecretVersion))
	if err != nil {
		t.Fatalf("decode encrypted value: %v", err)
	}
	payload[len(payload)-1] ^= 0x01
	tampered := encryptedSecretVersion + base64.RawURLEncoding.EncodeToString(payload)
	if _, err := cipher.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
}

func TestRecoveryCodesHaveExpectedFormatAndStableHash(t *testing.T) {
	codes, err := generateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("len(codes) = %d", len(codes))
	}
	for _, code := range codes {
		if len(code) != 19 || strings.Count(code, "-") != 3 {
			t.Fatalf("invalid recovery code %q", code)
		}
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	first := recoveryCodeHash(key, "user-1", codes[0])
	second := recoveryCodeHash(key, "user-1", strings.ToLower(strings.ReplaceAll(codes[0], "-", " ")))
	if first != second {
		t.Fatalf("hashes differ: %q != %q", first, second)
	}
	if first == recoveryCodeHash(key, "user-2", codes[0]) {
		t.Fatal("hash must be bound to user")
	}
}
~~~

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/auth -run 'TestSecretCipher|TestRecoveryCodes' -v`

Expected: FAIL，提示加密或恢复码函数未定义。

- [ ] **Step 3: 编写最小加密和恢复码实现**

创建 `server/internal/auth/two_factor_crypto.go`：

~~~go
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const encryptedSecretVersion = "v1."

type secretCipher struct {
	aead cipher.AEAD
}

func derivePurposeKey(root []byte, purpose string) ([]byte, error) {
	reader := hkdf.New(sha256.New, root, nil, []byte("vibe-terminal/"+purpose+"/v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func newSecretCipher(key []byte) (*secretCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretCipher{aead: aead}, nil
}

func (c *secretCipher) Encrypt(secret string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(secret), nil)
	payload := append(nonce, sealed...)
	return encryptedSecretVersion + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c *secretCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedSecretVersion) {
		return "", errors.New("unsupported encrypted secret version")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, encryptedSecretVersion))
	if err != nil {
		return "", err
	}
	if len(payload) < c.aead.NonceSize() {
		return "", errors.New("encrypted secret is truncated")
	}
	plain, err := c.aead.Open(nil, payload[:c.aead.NonceSize()], payload[c.aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func generateRecoveryCodes(count int) ([]string, error) {
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	codes := make([]string, 0, count)
	for index := 0; index < count; index++ {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		encoded := encoding.EncodeToString(raw)
		codes = append(codes, encoded[0:4]+"-"+encoded[4:8]+"-"+encoded[8:12]+"-"+encoded[12:16])
	}
	return codes, nil
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

func recoveryCodeHash(key []byte, userID string, code string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(userID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(normalizeRecoveryCode(code)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
~~~

- [ ] **Step 4: 运行测试并确认通过**

Run: `cd server && go test ./internal/auth -run 'TestSecretCipher|TestRecoveryCodes' -v`

Expected: PASS。

- [ ] **Step 5: 提交**

~~~bash
git add server/internal/auth/two_factor_crypto.go server/internal/auth/two_factor_crypto_test.go
git commit -m "feat(auth): protect two factor secrets"
~~~

### Task 3: 登录挑战和 TwoFactorManager

**Files:**
- Create: `server/internal/auth/challenge.go`
- Create: `server/internal/auth/challenge_test.go`

- [ ] **Step 1: 编写失败测试**

创建 `server/internal/auth/challenge_test.go`：

~~~go
package auth

import (
	"strings"
	"testing"
	"time"
)

func TestLoginChallengeRoundTripAndConfigurationBinding(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	manager, err := NewTwoFactorManager([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	token, err := manager.IssueLoginChallenge("user-1", "config-1")
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	challenge, err := manager.VerifyLoginChallenge(token)
	if err != nil {
		t.Fatalf("verify challenge: %v", err)
	}
	if challenge.UserID != "user-1" || challenge.ConfigurationID != "config-1" {
		t.Fatalf("challenge = %#v", challenge)
	}

	manager.now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, err := manager.VerifyLoginChallenge(token); err == nil {
		t.Fatal("expired challenge should fail")
	}
}

func TestLoginChallengeRejectsTampering(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	manager, err := NewTwoFactorManager([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	token, err := manager.IssueLoginChallenge("user-1", "config-1")
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	last := token[len(token)-1]
	replacement := byte('A')
	if last == replacement {
		replacement = 'B'
	}
	tampered := token[:len(token)-1] + string(replacement)
	if _, err := manager.VerifyLoginChallenge(tampered); err == nil {
		t.Fatal("tampered challenge should fail")
	}
}

func TestTwoFactorManagerBuildsSetupAndRecoveryHashes(t *testing.T) {
	manager, err := NewTwoFactorManager([]byte("0123456789abcdef0123456789abcdef"), time.Now)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	secret, uri, encrypted, err := manager.GenerateSetup("vibe-terminal", "admin")
	if err != nil {
		t.Fatalf("generate setup: %v", err)
	}
	if secret == "" || !strings.HasPrefix(uri, "otpauth://") || encrypted == "" {
		t.Fatalf("secret=%q uri=%q encrypted=%q", secret, uri, encrypted)
	}
	decrypted, err := manager.DecryptSecret(encrypted)
	if err != nil || decrypted != secret {
		t.Fatalf("decrypted = %q, err = %v", decrypted, err)
	}
	raw, hashes, err := manager.GenerateRecoveryCodes("user-1")
	if err != nil || len(raw) != 10 || len(hashes) != 10 {
		t.Fatalf("raw=%d hashes=%d err=%v", len(raw), len(hashes), err)
	}
}
~~~

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/auth -run 'TestLoginChallenge|TestTwoFactorManager' -v`

Expected: FAIL，提示 `NewTwoFactorManager` 未定义。

- [ ] **Step 3: 编写挑战和管理器实现**

创建 `server/internal/auth/challenge.go`：

~~~go
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const loginChallengeVersion = 1

type LoginChallenge struct {
	UserID          string
	ConfigurationID string
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

type challengePayload struct {
	Version         int    `json:"version"`
	UserID          string `json:"user_id"`
	ConfigurationID string `json:"configuration_id"`
	IssuedAt        int64  `json:"issued_at"`
	ExpiresAt       int64  `json:"expires_at"`
}

type TwoFactorManager struct {
	cipher       *secretCipher
	recoveryKey  []byte
	challengeKey []byte
	now          func() time.Time
}

func NewTwoFactorManager(root []byte, now func() time.Time) (*TwoFactorManager, error) {
	if now == nil {
		now = time.Now
	}
	encryptionKey, err := derivePurposeKey(root, "totp-encryption")
	if err != nil {
		return nil, err
	}
	cipher, err := newSecretCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	recoveryKey, err := derivePurposeKey(root, "recovery-codes")
	if err != nil {
		return nil, err
	}
	challengeKey, err := derivePurposeKey(root, "login-challenge")
	if err != nil {
		return nil, err
	}
	return &TwoFactorManager{
		cipher:       cipher,
		recoveryKey:  recoveryKey,
		challengeKey: challengeKey,
		now:          now,
	}, nil
}

func (m *TwoFactorManager) GenerateSetup(issuer string, account string) (string, string, string, error) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		return "", "", "", err
	}
	encrypted, err := m.cipher.Encrypt(secret)
	if err != nil {
		return "", "", "", err
	}
	return secret, TOTPProvisioningURI(issuer, account, secret), encrypted, nil
}

func (m *TwoFactorManager) DecryptSecret(encrypted string) (string, error) {
	return m.cipher.Decrypt(encrypted)
}

func (m *TwoFactorManager) GenerateRecoveryCodes(userID string) ([]string, []string, error) {
	raw, err := generateRecoveryCodes(10)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, len(raw))
	for index, code := range raw {
		hashes[index] = recoveryCodeHash(m.recoveryKey, userID, code)
	}
	return raw, hashes, nil
}

func (m *TwoFactorManager) RecoveryCodeHash(userID string, code string) string {
	return recoveryCodeHash(m.recoveryKey, userID, code)
}

func (m *TwoFactorManager) IssueLoginChallenge(userID string, configurationID string) (string, error) {
	now := m.now().UTC()
	payload := challengePayload{
		Version:         loginChallengeVersion,
		UserID:          userID,
		ConfigurationID: configurationID,
		IssuedAt:        now.Unix(),
		ExpiresAt:       now.Add(5 * time.Minute).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded + "." + m.signChallenge(encoded), nil
}

func (m *TwoFactorManager) VerifyLoginChallenge(token string) (LoginChallenge, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(parts[1]), []byte(m.signChallenge(parts[0]))) {
		return LoginChallenge{}, errors.New("invalid login challenge")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return LoginChallenge{}, errors.New("invalid login challenge")
	}
	var payload challengePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return LoginChallenge{}, errors.New("invalid login challenge")
	}
	now := m.now().UTC()
	if payload.Version != loginChallengeVersion || payload.UserID == "" || payload.ConfigurationID == "" {
		return LoginChallenge{}, errors.New("invalid login challenge")
	}
	if now.Unix() > payload.ExpiresAt || payload.IssuedAt > now.Add(time.Minute).Unix() {
		return LoginChallenge{}, errors.New("expired login challenge")
	}
	return LoginChallenge{
		UserID:          payload.UserID,
		ConfigurationID: payload.ConfigurationID,
		IssuedAt:        time.Unix(payload.IssuedAt, 0).UTC(),
		ExpiresAt:       time.Unix(payload.ExpiresAt, 0).UTC(),
	}, nil
}

func (m *TwoFactorManager) signChallenge(value string) string {
	mac := hmac.New(sha256.New, m.challengeKey)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
~~~

- [ ] **Step 4: 运行认证测试**

Run: `cd server && go test ./internal/auth -v`

Expected: PASS。

- [ ] **Step 5: 提交**

~~~bash
git add server/internal/auth/challenge.go server/internal/auth/challenge_test.go
git commit -m "feat(auth): add signed two factor challenges"
~~~

### Task 4: 登录失败限流器

**Files:**
- Create: `server/internal/auth/limiter.go`
- Create: `server/internal/auth/limiter_test.go`

- [ ] **Step 1: 编写失败测试**

创建 `server/internal/auth/limiter_test.go`：

~~~go
package auth

import (
	"testing"
	"time"
)

func TestFailureLimiterBlocksAtThresholdAndClearsOnSuccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	limiter := NewFailureLimiter(5, 10*time.Minute, 15*time.Minute, 100, func() time.Time { return now })
	for attempt := 1; attempt <= 4; attempt++ {
		blocked, _ := limiter.RecordFailure("admin|127.0.0.1")
		if blocked {
			t.Fatalf("attempt %d blocked too early", attempt)
		}
	}
	blocked, retryAfter := limiter.RecordFailure("admin|127.0.0.1")
	if !blocked || retryAfter != 15*time.Minute {
		t.Fatalf("blocked=%v retryAfter=%s", blocked, retryAfter)
	}
	if allowed, _ := limiter.Allow("admin|127.0.0.1"); allowed {
		t.Fatal("blocked key should not be allowed")
	}
	limiter.Success("admin|127.0.0.1")
	if allowed, _ := limiter.Allow("admin|127.0.0.1"); !allowed {
		t.Fatal("success should clear failures")
	}
}

func TestFailureLimiterExpiresWindowAndCapsEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	limiter := NewFailureLimiter(2, time.Minute, time.Minute, 2, func() time.Time { return now })
	limiter.RecordFailure("first")
	limiter.RecordFailure("second")
	limiter.RecordFailure("third")
	if len(limiter.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(limiter.entries))
	}
	now = now.Add(2 * time.Minute)
	if allowed, _ := limiter.Allow("second"); !allowed {
		t.Fatal("expired entry should be allowed")
	}
}
~~~

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/auth -run TestFailureLimiter -v`

Expected: FAIL，提示 `NewFailureLimiter` 未定义。

- [ ] **Step 3: 编写最小限流实现**

创建 `server/internal/auth/limiter.go`：

~~~go
package auth

import (
	"sync"
	"time"
)

type failureEntry struct {
	Failures     int
	WindowStart time.Time
	BlockedUntil time.Time
	LastSeen     time.Time
}

type FailureLimiter struct {
	mu          sync.Mutex
	entries     map[string]failureEntry
	maxFailures int
	window      time.Duration
	blockFor    time.Duration
	maxEntries  int
	now         func() time.Time
}

func NewFailureLimiter(maxFailures int, window time.Duration, blockFor time.Duration, maxEntries int, now func() time.Time) *FailureLimiter {
	if now == nil {
		now = time.Now
	}
	return &FailureLimiter{
		entries:     make(map[string]failureEntry),
		maxFailures: maxFailures,
		window:      window,
		blockFor:    blockFor,
		maxEntries:  maxEntries,
		now:         now,
	}
}

func (l *FailureLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	entry, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if !entry.BlockedUntil.IsZero() && now.Before(entry.BlockedUntil) {
		return false, entry.BlockedUntil.Sub(now)
	}
	if now.Sub(entry.WindowStart) >= l.window {
		delete(l.entries, key)
		return true, 0
	}
	return true, 0
}

func (l *FailureLimiter) RecordFailure(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	l.evict(now)
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.WindowStart) >= l.window {
		entry = failureEntry{WindowStart: now}
	}
	entry.Failures++
	entry.LastSeen = now
	if entry.Failures >= l.maxFailures {
		entry.BlockedUntil = now.Add(l.blockFor)
	}
	l.entries[key] = entry
	if now.Before(entry.BlockedUntil) {
		return true, entry.BlockedUntil.Sub(now)
	}
	return false, 0
}

func (l *FailureLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *FailureLimiter) evict(now time.Time) {
	for key, entry := range l.entries {
		if now.After(entry.BlockedUntil) && now.Sub(entry.LastSeen) >= l.window {
			delete(l.entries, key)
		}
	}
	for len(l.entries) >= l.maxEntries {
		var oldestKey string
		var oldest time.Time
		for key, entry := range l.entries {
			if oldestKey == "" || entry.LastSeen.Before(oldest) {
				oldestKey = key
				oldest = entry.LastSeen
			}
		}
		delete(l.entries, oldestKey)
	}
}
~~~

- [ ] **Step 4: 运行认证包全量测试**

Run: `cd server && go test ./internal/auth -v`

Expected: PASS。

- [ ] **Step 5: 提交**

~~~bash
git add server/internal/auth/limiter.go server/internal/auth/limiter_test.go
git commit -m "feat(auth): rate limit login failures"
~~~

### Task 5: SQLite 2FA 模型和待确认设置

**Files:**
- Modify: `server/internal/store/store.go:27-200`
- Modify: `server/internal/store/store_test.go`

- [ ] **Step 1: 编写迁移和待确认状态失败测试**

向 `server/internal/store/store_test.go` 追加：

~~~go
func TestMigrateCreatesTwoFactorTables(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"user_two_factor", "two_factor_recovery_codes"} {
		var name string
		if err := db.SQL.QueryRowContext(ctx, "select name from sqlite_master where type='table' and name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestPendingTwoFactorRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := newSnippetTestDB(t)
	_, err := db.CreateUser(ctx, User{ID: "user-2fa", Username: "admin", PasswordHash: "hash"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	err = db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "user-2fa",
		ConfigurationID:  "config-1",
		SecretCiphertext: "v1.ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(10 * time.Minute), Valid: true},
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	pending, err := db.GetPendingTwoFactor(ctx, "user-2fa", now)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if pending.ConfigurationID != "config-1" || pending.EnabledAt.Valid {
		t.Fatalf("pending = %#v", pending)
	}
	if _, err := db.GetEnabledTwoFactor(ctx, "user-2fa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enabled error = %v", err)
	}
}
~~~

同时在测试文件 import 中加入 `database/sql`。

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/store -run 'TestMigrateCreatesTwoFactorTables|TestPendingTwoFactorRoundTrip' -v`

Expected: FAIL，提示表、类型或方法不存在。

- [ ] **Step 3: 增加模型和迁移**

在 `store.go` 的模型区域加入：

~~~go
type UserTwoFactor struct {
	UserID           string
	ConfigurationID  string
	SecretCiphertext string
	SetupExpiresAt   sql.NullTime
	EnabledAt        sql.NullTime
	LastTOTPCounter  sql.NullInt64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type RecoveryCodeInput struct {
	ID   string
	Hash string
}
~~~

在 `Migrate` 的 users 表之后加入：

~~~go
`create table if not exists user_two_factor (
	user_id text primary key references users(id) on delete cascade,
	configuration_id text not null,
	secret_ciphertext text not null,
	setup_expires_at datetime,
	enabled_at datetime,
	last_totp_counter integer,
	created_at datetime not null,
	updated_at datetime not null
)`,
`create table if not exists two_factor_recovery_codes (
	id text primary key,
	user_id text not null references users(id) on delete cascade,
	code_hash text not null,
	used_at datetime,
	created_at datetime not null,
	unique(user_id, code_hash)
)`,
~~~

- [ ] **Step 4: 增加待确认设置读写方法**

在用户查询方法之后加入：

~~~go
func (db *DB) SavePendingTwoFactor(ctx context.Context, setting UserTwoFactor) error {
	now := time.Now().UTC()
	if setting.CreatedAt.IsZero() {
		setting.CreatedAt = now
	}
	if setting.UpdatedAt.IsZero() {
		setting.UpdatedAt = now
	}
	result, err := db.SQL.ExecContext(ctx,
		`insert into user_two_factor
			(user_id, configuration_id, secret_ciphertext, setup_expires_at, enabled_at, last_totp_counter, created_at, updated_at)
		 values (?, ?, ?, ?, null, null, ?, ?)
		 on conflict(user_id) do update set
			configuration_id = excluded.configuration_id,
			secret_ciphertext = excluded.secret_ciphertext,
			setup_expires_at = excluded.setup_expires_at,
			enabled_at = null,
			last_totp_counter = null,
			updated_at = excluded.updated_at
		 where user_two_factor.enabled_at is null`,
		setting.UserID, setting.ConfigurationID, setting.SecretCiphertext,
		setting.SetupExpiresAt, setting.CreatedAt, setting.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrConflict
	}
	return nil
}

func (db *DB) GetEnabledTwoFactor(ctx context.Context, userID string) (UserTwoFactor, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select user_id, configuration_id, secret_ciphertext, setup_expires_at,
		        enabled_at, last_totp_counter, created_at, updated_at
		   from user_two_factor
		  where user_id = ? and enabled_at is not null`, userID)
	return scanUserTwoFactor(row)
}

func (db *DB) GetPendingTwoFactor(ctx context.Context, userID string, now time.Time) (UserTwoFactor, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select user_id, configuration_id, secret_ciphertext, setup_expires_at,
		        enabled_at, last_totp_counter, created_at, updated_at
		   from user_two_factor
		  where user_id = ? and enabled_at is null and setup_expires_at > ?`, userID, now)
	return scanUserTwoFactor(row)
}

func scanUserTwoFactor(row scanner) (UserTwoFactor, error) {
	var setting UserTwoFactor
	err := row.Scan(
		&setting.UserID,
		&setting.ConfigurationID,
		&setting.SecretCiphertext,
		&setting.SetupExpiresAt,
		&setting.EnabledAt,
		&setting.LastTOTPCounter,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UserTwoFactor{}, ErrNotFound
	}
	return setting, err
}
~~~

- [ ] **Step 5: 运行 store 测试**

Run: `cd server && go test ./internal/store -run 'TestMigrateCreatesTwoFactorTables|TestPendingTwoFactorRoundTrip' -v`

Expected: PASS。

- [ ] **Step 6: 提交**

~~~bash
git add server/internal/store/store.go server/internal/store/store_test.go
git commit -m "feat(store): persist pending two factor setup"
~~~

### Task 6: SQLite 原子启用、消费、轮换和关闭

**Files:**
- Modify: `server/internal/store/store.go`
- Modify: `server/internal/store/store_test.go`

- [ ] **Step 1: 编写原子状态转换失败测试**

向 `store_test.go` 追加：

~~~go
func TestTwoFactorEnableConsumeRotateAndDisable(t *testing.T) {
	ctx := context.Background()
	db := newSnippetTestDB(t)
	_, err := db.CreateUser(ctx, User{ID: "user-2fa-flow", Username: "admin2", PasswordHash: "hash"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	err = db.SavePendingTwoFactor(ctx, UserTwoFactor{
		UserID:           "user-2fa-flow",
		ConfigurationID:  "config-flow",
		SecretCiphertext: "v1.ciphertext",
		SetupExpiresAt:   sql.NullTime{Time: now.Add(10 * time.Minute), Valid: true},
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	initialCodes := []RecoveryCodeInput{{ID: "code-1", Hash: "hash-1"}, {ID: "code-2", Hash: "hash-2"}}
	if err := db.EnableTwoFactor(ctx, "user-2fa-flow", "config-flow", 100, initialCodes, now); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := db.ConsumeTOTPCounter(ctx, "user-2fa-flow", "config-flow", 101, now); err != nil {
		t.Fatalf("consume totp: %v", err)
	}
	if err := db.ConsumeTOTPCounter(ctx, "user-2fa-flow", "config-flow", 101, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay error = %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "user-2fa-flow", "hash-1", now); err != nil {
		t.Fatalf("consume recovery: %v", err)
	}
	if err := db.ConsumeRecoveryCode(ctx, "user-2fa-flow", "hash-1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused recovery error = %v", err)
	}
	remaining, err := db.CountRecoveryCodes(ctx, "user-2fa-flow")
	if err != nil || remaining != 1 {
		t.Fatalf("remaining = %d, err = %v", remaining, err)
	}
	replacement := []RecoveryCodeInput{{ID: "code-3", Hash: "hash-3"}}
	if err := db.ReplaceRecoveryCodesAfterTOTP(ctx, "user-2fa-flow", "config-flow", 102, replacement, now); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if remaining, err := db.CountRecoveryCodes(ctx, "user-2fa-flow"); err != nil || remaining != 1 {
		t.Fatalf("remaining after replace = %d, err = %v", remaining, err)
	}
	if err := db.DisableTwoFactor(ctx, "user-2fa-flow"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := db.GetEnabledTwoFactor(ctx, "user-2fa-flow"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enabled after disable error = %v", err)
	}
}
~~~

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/store -run TestTwoFactorEnableConsumeRotateAndDisable -v`

Expected: FAIL，提示原子操作方法未定义。

- [ ] **Step 3: 添加事务辅助函数**

在 `store.go` 加入：

~~~go
func insertRecoveryCodes(ctx context.Context, tx *sql.Tx, userID string, codes []RecoveryCodeInput, createdAt time.Time) error {
	for _, code := range codes {
		if _, err := tx.ExecContext(ctx,
			`insert into two_factor_recovery_codes (id, user_id, code_hash, created_at)
			 values (?, ?, ?, ?)`,
			code.ID, userID, code.Hash, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func requireAffected(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}
~~~

- [ ] **Step 4: 添加启用和 TOTP 消费**

~~~go
func (db *DB) EnableTwoFactor(ctx context.Context, userID string, configurationID string, counter int64, codes []RecoveryCodeInput, now time.Time) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`update user_two_factor
		    set enabled_at = ?, setup_expires_at = null, last_totp_counter = ?, updated_at = ?
		  where user_id = ? and configuration_id = ? and enabled_at is null and setup_expires_at > ?`,
		now, counter, now, userID, configurationID, now)
	if err != nil {
		return err
	}
	if err := requireAffected(result, ErrConflict); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	if err := insertRecoveryCodes(ctx, tx, userID, codes, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) ConsumeTOTPCounter(ctx context.Context, userID string, configurationID string, counter int64, now time.Time) error {
	result, err := db.SQL.ExecContext(ctx,
		`update user_two_factor
		    set last_totp_counter = ?, updated_at = ?
		  where user_id = ? and configuration_id = ? and enabled_at is not null
		    and (last_totp_counter is null or last_totp_counter < ?)`,
		counter, now, userID, configurationID, counter)
	if err != nil {
		return err
	}
	return requireAffected(result, ErrConflict)
}
~~~

- [ ] **Step 5: 添加恢复码消费、轮换和关闭**

~~~go
func (db *DB) ConsumeRecoveryCode(ctx context.Context, userID string, hash string, now time.Time) error {
	result, err := db.SQL.ExecContext(ctx,
		`update two_factor_recovery_codes
		    set used_at = ?
		  where user_id = ? and code_hash = ? and used_at is null`,
		now, userID, hash)
	if err != nil {
		return err
	}
	return requireAffected(result, ErrNotFound)
}

func (db *DB) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var count int
	err := db.SQL.QueryRowContext(ctx,
		`select count(*) from two_factor_recovery_codes where user_id = ? and used_at is null`,
		userID).Scan(&count)
	return count, err
}

func (db *DB) ReplaceRecoveryCodesAfterTOTP(ctx context.Context, userID string, configurationID string, counter int64, codes []RecoveryCodeInput, now time.Time) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`update user_two_factor
		    set last_totp_counter = ?, updated_at = ?
		  where user_id = ? and configuration_id = ? and enabled_at is not null
		    and (last_totp_counter is null or last_totp_counter < ?)`,
		counter, now, userID, configurationID, counter)
	if err != nil {
		return err
	}
	if err := requireAffected(result, ErrConflict); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	if err := insertRecoveryCodes(ctx, tx, userID, codes, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) DisableTwoFactor(ctx context.Context, userID string) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `delete from two_factor_recovery_codes where user_id = ?`, userID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `delete from user_two_factor where user_id = ? and enabled_at is not null`, userID)
	if err != nil {
		return err
	}
	if err := requireAffected(result, ErrNotFound); err != nil {
		return err
	}
	return tx.Commit()
}
~~~

- [ ] **Step 6: 运行 store 全量测试**

Run: `cd server && go test ./internal/store -v`

Expected: PASS。

- [ ] **Step 7: 提交**

~~~bash
git add server/internal/store/store.go server/internal/store/store_test.go
git commit -m "feat(store): atomically consume two factor credentials"
~~~

### Task 7: 两阶段登录后端

**Files:**
- Modify: `server/internal/httpapi/router.go:38-157`
- Create: `server/internal/httpapi/router_2fa.go`
- Create: `server/internal/httpapi/router_2fa_test.go`
- Modify: `server/cmd/server/main.go:20-46`

- [ ] **Step 1: 编写已启用 2FA 时不签发 Cookie 的失败测试**

创建 `server/internal/httpapi/router_2fa_test.go`，包含固定时钟、管理员、已启用设置和 JSON 请求辅助函数：

~~~go
package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

type twoFactorFixture struct {
	handler http.Handler
	db      *store.DB
	manager *auth.TwoFactorManager
	now     time.Time
	secret  string
}

func newTwoFactorFixture(t *testing.T) twoFactorFixture {
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
	now := time.Unix(1_700_000_000, 0).UTC()
	manager, err := auth.NewTwoFactorManager([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	secret, _, encrypted, err := manager.GenerateSetup("vibe-terminal", "admin")
	if err != nil {
		t.Fatalf("generate setup: %v", err)
	}
	if err := db.SavePendingTwoFactor(ctx, store.UserTwoFactor{
		UserID: "user-1", ConfigurationID: "config-1", SecretCiphertext: encrypted,
		SetupExpiresAt: sqlNullTime(now.Add(10 * time.Minute)), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if err := db.EnableTwoFactor(ctx, "user-1", "config-1", now.Unix()/auth.TOTPPeriodSeconds-1, nil, now); err != nil {
		t.Fatalf("enable: %v", err)
	}
	handler := NewRouter(Deps{
		Store: db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		TwoFactor: manager,
		Now: func() time.Time { return now },
	})
	return twoFactorFixture{handler: handler, db: db, manager: manager, now: now, secret: secret}
}

func sqlNullTime(value time.Time) store.NullTime {
	return store.NullTime{Time: value, Valid: true}
}

func serveJSON(handler http.Handler, method string, path string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func testTOTPCode(secret string, at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(at.Unix()/auth.TOTPPeriodSeconds))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

func TestTwoFactorLoginRequiresSecondStepBeforeCookie(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	first := serveJSON(fixture.handler, http.MethodPost, "/api/login", `{"username":"admin","password":"secret"}`)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	if len(first.Result().Cookies()) != 0 {
		t.Fatal("first step must not set a session cookie")
	}
	var challenge struct {
		ChallengeToken string `json:"challenge_token"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	last := challenge.ChallengeToken[len(challenge.ChallengeToken)-1]
	replacement := byte('A')
	if last == replacement {
		replacement = 'B'
	}
	tampered := challenge.ChallengeToken[:len(challenge.ChallengeToken)-1] + string(replacement)
	tamperedResponse := serveJSON(fixture.handler, http.MethodPost, "/api/login/2fa",
		fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, tampered, testTOTPCode(fixture.secret, fixture.now)))
	if tamperedResponse.Code != http.StatusUnauthorized || !strings.Contains(tamperedResponse.Body.String(), "login_restart_required") {
		t.Fatalf("tampered status = %d body=%s", tamperedResponse.Code, tamperedResponse.Body.String())
	}
	second := serveJSON(fixture.handler, http.MethodPost, "/api/login/2fa",
		fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, challenge.ChallengeToken, testTOTPCode(fixture.secret, fixture.now)))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}
	if len(second.Result().Cookies()) == 0 {
		t.Fatal("second step should set the session cookie")
	}
	replay := serveJSON(fixture.handler, http.MethodPost, "/api/login/2fa",
		fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, challenge.ChallengeToken, testTOTPCode(fixture.secret, fixture.now)))
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
}
~~~

在测试 import 中加入 `strings`。

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd server && go test ./internal/httpapi -run TestTwoFactorLoginRequiresSecondStepBeforeCookie -v`

Expected: FAIL，第一步仍返回 200 并设置 Cookie，或新依赖字段未定义。

- [ ] **Step 3: 扩展 router 依赖并注册路由**

在 `Deps` 中加入：

~~~go
TwoFactor          *auth.TwoFactorManager
PasswordLimiter    *auth.FailureLimiter
TwoFactorLimiter   *auth.FailureLimiter
Now                func() time.Time
~~~

在 `router` 中加入对应的私有字段：

~~~go
twoFactor        *auth.TwoFactorManager
passwordLimiter  *auth.FailureLimiter
twoFactorLimiter *auth.FailureLimiter
now              func() time.Time
~~~

在 `NewRouter` 中设置默认值：

~~~go
if deps.Now == nil {
	deps.Now = time.Now
}
if deps.PasswordLimiter == nil {
	deps.PasswordLimiter = auth.NewFailureLimiter(5, 10*time.Minute, 15*time.Minute, 5000, deps.Now)
}
if deps.TwoFactorLimiter == nil {
	deps.TwoFactorLimiter = auth.NewFailureLimiter(5, 5*time.Minute, 15*time.Minute, 5000, deps.Now)
}
~~~

在 router 初始化字面量中加入：

~~~go
twoFactor:        deps.TwoFactor,
passwordLimiter:  deps.PasswordLimiter,
twoFactorLimiter: deps.TwoFactorLimiter,
now:              deps.Now,
~~~

并在 `routes()` 注册：

~~~go
r.mux.HandleFunc("POST /api/login/2fa", r.handleLoginTwoFactor)
~~~

- [ ] **Step 4: 修改密码登录分流**

用以下逻辑替换 `handleLogin`：

~~~go
func (r *router) handleLogin(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, req, &body) {
		return
	}
	limitKey := strings.ToLower(strings.TrimSpace(body.Username)) + "|" + requestIP(req)
	if allowed, retryAfter := r.passwordLimiter.Allow(limitKey); !allowed {
		writeRateLimit(w, retryAfter)
		return
	}
	user, err := r.store.GetUserByUsername(req.Context(), body.Username)
	if err != nil || !auth.CheckPassword(user.PasswordHash, body.Password) {
		if blocked, retryAfter := r.passwordLimiter.RecordFailure(limitKey); blocked {
			r.auditLoginRateLimit(req, user.ID, "password")
			writeRateLimit(w, retryAfter)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	r.passwordLimiter.Success(limitKey)
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
	writeJSON(w, http.StatusAccepted, map[string]any{
		"two_factor_required": true,
		"challenge_token": challenge,
		"expires_in": 300,
	})
}
~~~

- [ ] **Step 5: 创建第二步登录 handler 和公共辅助函数**

在 `router_2fa.go` 写入：

~~~go
package httpapi

import (
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
)

var errInvalidSecondFactor = errors.New("invalid second factor")

func (r *router) handleLoginTwoFactor(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ChallengeToken string `json:"challenge_token"`
		Code           string `json:"code"`
	}
	if !readTwoFactorJSON(w, req, &body) {
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	challenge, err := r.twoFactor.VerifyLoginChallenge(body.ChallengeToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login")
		return
	}
	limitKey := challenge.UserID + "|" + requestIP(req)
	if allowed, retryAfter := r.twoFactorLimiter.Allow(limitKey); !allowed {
		writeRateLimit(w, retryAfter)
		return
	}
	user, err := r.store.GetUserByID(req.Context(), challenge.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login")
		return
	}
	setting, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID)
	if err != nil || setting.ConfigurationID != challenge.ConfigurationID {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login")
		return
	}
	method, verifyErr := r.verifyLoginCode(req, setting, body.Code)
	if verifyErr != nil {
		if !errors.Is(verifyErr, errInvalidSecondFactor) {
			writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
			return
		}
		if blocked, retryAfter := r.twoFactorLimiter.RecordFailure(limitKey); blocked {
			r.auditLoginRateLimit(req, user.ID, "second_factor")
			writeRateLimit(w, retryAfter)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid authentication code")
		return
	}
	r.twoFactorLimiter.Success(limitKey)
	r.completeLogin(w, req, user, method)
}

func (r *router) verifyLoginCode(req *http.Request, setting store.UserTwoFactor, code string) (string, error) {
	now := r.now().UTC()
	if len(strings.TrimSpace(code)) == 6 {
		secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
		if err != nil {
			return "", err
		}
		counter, ok, err := auth.MatchTOTP(secret, code, now)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errInvalidSecondFactor
		}
		if err := r.store.ConsumeTOTPCounter(req.Context(), setting.UserID, setting.ConfigurationID, counter, now); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return "", errInvalidSecondFactor
			}
			return "", err
		}
		return "totp", nil
	}
	hash := r.twoFactor.RecoveryCodeHash(setting.UserID, code)
	if err := r.store.ConsumeRecoveryCode(req.Context(), setting.UserID, hash, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errInvalidSecondFactor
		}
		return "", err
	}
	return "recovery_code", nil
}

func (r *router) completeLogin(w http.ResponseWriter, req *http.Request, user store.User, method string) {
	if err := r.sessions.Set(w, user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "failed to create session")
		return
	}
	metadata, _ := json.Marshal(map[string]string{"method": method})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID: user.ID, EventType: "login", Summary: "administrator logged in", MetadataJSON: string(metadata),
	})
	writeJSON(w, http.StatusOK, userResponse(user))
}

func (r *router) auditLoginRateLimit(req *http.Request, userID string, stage string) {
	metadata, _ := json.Marshal(map[string]string{"stage": stage, "source_ip": requestIP(req)})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID: userID, EventType: "login_rate_limited", Summary: "authentication rate limit reached",
		MetadataJSON: string(metadata),
	})
}

func requestIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err == nil {
		return host
	}
	return req.RemoteAddr
}

func writeRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many authentication attempts")
}

func readTwoFactorJSON(w http.ResponseWriter, req *http.Request, dest any) bool {
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return false
	}
	return readJSON(w, req, dest)
}
~~~

- [ ] **Step 6: 在 main 中构造管理器**

在创建 router 前加入：

~~~go
twoFactor, err := auth.NewTwoFactorManager(cfg.SessionSecret, time.Now)
if err != nil {
	log.Fatalf("initialize two factor authentication: %v", err)
}
~~~

向 `main.go` import 添加 `time`，并在 `Deps` 中传入：

~~~go
TwoFactor: twoFactor,
~~~

- [ ] **Step 7: 运行登录测试和现有 HTTP 测试**

Run: `cd server && go test ./internal/httpapi -run 'TestTwoFactorLoginRequiresSecondStepBeforeCookie|TestLoginMeAndAgentTokenFlow' -v`

Expected: PASS；未启用 2FA 的现有登录仍返回 200。

- [ ] **Step 8: 提交**

~~~bash
git add server/internal/httpapi/router.go server/internal/httpapi/router_2fa.go server/internal/httpapi/router_2fa_test.go server/cmd/server/main.go
git commit -m "feat(server): require second factor before web session"
~~~

### Task 8: 2FA 设置、恢复码和关闭接口

**Files:**
- Modify: `server/internal/httpapi/router.go:99-119`
- Modify: `server/internal/httpapi/router_2fa.go`
- Modify: `server/internal/httpapi/router_2fa_test.go`

- [ ] **Step 1: 编写管理流程失败测试**

向 `router_2fa_test.go` 追加一个未启用 2FA 的 fixture 测试。使用现有密码登录取得 Cookie，再完成 setup、enable、status、disable：

~~~go
func TestTwoFactorManagementFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{ID: "user-manage", Username: "admin", PasswordHash: hash}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	manager, err := auth.NewTwoFactorManager([]byte("0123456789abcdef0123456789abcdef"), func() time.Time { return now })
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	handler := NewRouter(Deps{
		Store: db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		TwoFactor: manager,
		Now: func() time.Time { return now },
	})
	login := serveJSON(handler, http.MethodPost, "/api/login", `{"username":"admin","password":"secret"}`)
	cookies := login.Result().Cookies()

	setup := serveJSON(handler, http.MethodPost, "/api/security/2fa/setup", `{"password":"secret"}`, cookies...)
	if setup.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setup.Code, setup.Body.String())
	}
	var setupBody struct {
		ManualKey string `json:"manual_key"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &setupBody); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	enable := serveJSON(handler, http.MethodPost, "/api/security/2fa/enable",
		fmt.Sprintf(`{"code":%q}`, testTOTPCode(setupBody.ManualKey, now)), cookies...)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable status = %d body=%s", enable.Code, enable.Body.String())
	}
	var recovery struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(enable.Body.Bytes(), &recovery); err != nil || len(recovery.RecoveryCodes) != 10 {
		t.Fatalf("recovery = %#v err = %v", recovery, err)
	}
	now = now.Add(30 * time.Second)
	regenerated := serveJSON(handler, http.MethodPost, "/api/security/2fa/recovery-codes",
		fmt.Sprintf(`{"password":"secret","code":%q}`, testTOTPCode(setupBody.ManualKey, now)), cookies...)
	if regenerated.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d body=%s", regenerated.Code, regenerated.Body.String())
	}
	if err := json.Unmarshal(regenerated.Body.Bytes(), &recovery); err != nil || len(recovery.RecoveryCodes) != 10 {
		t.Fatalf("regenerated recovery = %#v err = %v", recovery, err)
	}
	status := serveJSON(handler, http.MethodGet, "/api/security/2fa", "", cookies...)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"enabled":true`) {
		t.Fatalf("status = %d body=%s", status.Code, status.Body.String())
	}
	disable := serveJSON(handler, http.MethodPost, "/api/security/2fa/disable", `{"password":"secret"}`, cookies...)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status = %d body=%s", disable.Code, disable.Body.String())
	}
}
~~~

在测试 import 中加入 `strings`。

- [ ] **Step 2: 运行测试并确认 404 失败**

Run: `cd server && go test ./internal/httpapi -run TestTwoFactorManagementFlow -v`

Expected: FAIL，管理接口返回 404。

- [ ] **Step 3: 注册管理路由**

在 `routes()` 中加入：

~~~go
r.mux.HandleFunc("GET /api/security/2fa", r.handleTwoFactorStatus)
r.mux.HandleFunc("POST /api/security/2fa/setup", r.handleTwoFactorSetup)
r.mux.HandleFunc("POST /api/security/2fa/enable", r.handleTwoFactorEnable)
r.mux.HandleFunc("POST /api/security/2fa/recovery-codes", r.handleTwoFactorRecoveryCodes)
r.mux.HandleFunc("POST /api/security/2fa/disable", r.handleTwoFactorDisable)
~~~

管理 handler 继续使用 Task 7 已有的 `errors` 和 `time` import，并新增 `github.com/google/uuid`。

- [ ] **Step 4: 实现状态和开始设置接口**

向 `router_2fa.go` 加入：

~~~go
func (r *router) handleTwoFactorStatus(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	if _, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "recovery_codes_remaining": 0})
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	count, err := r.store.CountRecoveryCodes(req.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "recovery_codes_remaining": count})
}

func (r *router) handleTwoFactorSetup(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readTwoFactorJSON(w, req, &body) {
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid password")
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if _, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID); err == nil {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication is already enabled")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	secret, uri, encrypted, err := r.twoFactor.GenerateSetup("vibe-terminal", user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	expires := now.Add(10 * time.Minute)
	err = r.store.SavePendingTwoFactor(req.Context(), store.UserTwoFactor{
		UserID: user.ID, ConfigurationID: uuid.NewString(), SecretCiphertext: encrypted,
		SetupExpiresAt: store.NullTime{Time: expires, Valid: true}, CreatedAt: now, UpdatedAt: now,
	})
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor state changed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"otpauth_uri": uri, "manual_key": secret, "expires_at": expires.Format(time.RFC3339),
	})
}
~~~

- [ ] **Step 5: 实现确认启用**

~~~go
func recoveryInputs(hashes []string) []store.RecoveryCodeInput {
	inputs := make([]store.RecoveryCodeInput, len(hashes))
	for index, hash := range hashes {
		inputs[index] = store.RecoveryCodeInput{ID: uuid.NewString(), Hash: hash}
	}
	return inputs
}

func (r *router) handleTwoFactorEnable(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !readTwoFactorJSON(w, req, &body) {
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	setting, err := r.store.GetPendingTwoFactor(req.Context(), user.ID, now)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_setup_expired", "two factor setup expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	counter, valid, err := auth.MatchTOTP(secret, body.Code, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid authentication code")
		return
	}
	raw, hashes, err := r.twoFactor.GenerateRecoveryCodes(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if err := r.store.EnableTwoFactor(req.Context(), user.ID, setting.ConfigurationID, counter, recoveryInputs(hashes), now); errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor state changed")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID: user.ID, EventType: "two_factor_enabled", Summary: "two factor authentication enabled",
	})
	writeJSON(w, http.StatusOK, map[string][]string{"recovery_codes": raw})
}
~~~

- [ ] **Step 6: 实现恢复码轮换和关闭**

~~~go
func (r *router) handleTwoFactorRecoveryCodes(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readTwoFactorJSON(w, req, &body) {
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid password")
		return
	}
	setting, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication is not enabled")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	counter, valid, err := auth.MatchTOTP(secret, body.Code, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid authentication code")
		return
	}
	raw, hashes, err := r.twoFactor.GenerateRecoveryCodes(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if err := r.store.ReplaceRecoveryCodesAfterTOTP(req.Context(), user.ID, setting.ConfigurationID, counter, recoveryInputs(hashes), now); errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid authentication code")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID: user.ID, EventType: "two_factor_recovery_codes_regenerated", Summary: "two factor recovery codes regenerated",
	})
	writeJSON(w, http.StatusOK, map[string][]string{"recovery_codes": raw})
}

func (r *router) handleTwoFactorDisable(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readTwoFactorJSON(w, req, &body) {
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid password")
		return
	}
	if err := r.store.DisableTwoFactor(req.Context(), user.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication is not enabled")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID: user.ID, EventType: "two_factor_disabled", Summary: "two factor authentication disabled",
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
~~~

- [ ] **Step 7: 增加恢复码单次登录和限流测试**

向 `router_2fa_test.go` 增加两个测试：

~~~go
func TestRecoveryCodeCanOnlyLoginOnce(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	raw, hashes, err := fixture.manager.GenerateRecoveryCodes("user-1")
	if err != nil {
		t.Fatalf("generate recovery: %v", err)
	}
	if err := fixture.db.ReplaceRecoveryCodesAfterTOTP(
		context.Background(), "user-1", "config-1",
		fixture.now.Unix()/auth.TOTPPeriodSeconds, recoveryInputs(hashes), fixture.now,
	); err != nil {
		t.Fatalf("store recovery: %v", err)
	}
	login := serveJSON(fixture.handler, http.MethodPost, "/api/login", `{"username":"admin","password":"secret"}`)
	var challenge struct{ ChallengeToken string `json:"challenge_token"` }
	_ = json.Unmarshal(login.Body.Bytes(), &challenge)
	first := serveJSON(fixture.handler, http.MethodPost, "/api/login/2fa",
		fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, challenge.ChallengeToken, raw[0]))
	if first.Code != http.StatusOK {
		t.Fatalf("first recovery status = %d", first.Code)
	}
	second := serveJSON(fixture.handler, http.MethodPost, "/api/login/2fa",
		fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, challenge.ChallengeToken, raw[0]))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second recovery status = %d", second.Code)
	}
}

func TestPasswordLoginRateLimitReturnsRetryAfter(t *testing.T) {
	fixture := newTwoFactorFixture(t)
	var last *httptest.ResponseRecorder
	for attempt := 0; attempt < 5; attempt++ {
		last = serveJSON(fixture.handler, http.MethodPost, "/api/login", `{"username":"admin","password":"wrong"}`)
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Fatalf("status = %d retry-after = %q", last.Code, last.Header().Get("Retry-After"))
	}
}
~~~

- [ ] **Step 8: 运行服务端全量测试**

Run: `cd server && go test ./...`

Expected: PASS。

- [ ] **Step 9: 提交**

~~~bash
git add server/internal/httpapi/router.go server/internal/httpapi/router_2fa.go server/internal/httpapi/router_2fa_test.go
git commit -m "feat(server): manage optional web two factor auth"
~~~

### Task 9: 前端登录 API 和两步登录页

**Files:**
- Modify: `web/src/api.ts:1-62`
- Modify: `web/src/components/LoginView.tsx`
- Create: `web/src/test/api.test.ts`
- Create: `web/src/test/LoginView.test.tsx`

- [ ] **Step 1: 编写 API 联合响应失败测试**

创建 `web/src/test/api.test.ts`：

~~~ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { login, verifyTwoFactor } from '../api';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('authentication api', () => {
  it('keeps password-only login compatible with a 200 user response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: 'user-1', username: 'admin' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    );

    await expect(login('admin', 'secret')).resolves.toEqual({
      kind: 'authenticated',
      user: { id: 'user-1', username: 'admin' },
    });
  });

  it('returns a two factor challenge for a 202 login response', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ two_factor_required: true, challenge_token: 'challenge-1', expires_in: 300 }),
          { status: 202, headers: { 'Content-Type': 'application/json' } }
        )
      )
    );

    await expect(login('admin', 'secret')).resolves.toEqual({
      kind: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
  });

  it('returns the authenticated user after second factor verification', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: 'user-1', username: 'admin' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    );

    await expect(verifyTwoFactor('challenge-1', '123456')).resolves.toEqual({
      id: 'user-1',
      username: 'admin',
    });
  });

  it('preserves structured error codes and retry-after', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: 'too_many_attempts', message: 'try later' }), {
          status: 429,
          headers: { 'Content-Type': 'application/json', 'Retry-After': '900' },
        })
      )
    );

    await expect(login('admin', 'wrong')).rejects.toMatchObject({
      status: 429,
      code: 'too_many_attempts',
      retryAfter: 900,
    });
  });
});
~~~

- [ ] **Step 2: 运行 API 测试并确认失败**

Run: `cd web && npm test -- --run src/test/api.test.ts`

Expected: FAIL，提示 `APIError`、联合登录响应或 `verifyTwoFactor` 不存在。

- [ ] **Step 3: 重构请求封装并增加认证类型**

在 `api.ts` 顶部加入：

~~~ts
export type LoginResult =
  | { kind: 'authenticated'; user: User }
  | { kind: 'two_factor_required'; challengeToken: string; expiresIn: number };

export type TwoFactorStatus = {
  enabled: boolean;
  recovery_codes_remaining: number;
};

export type TwoFactorSetup = {
  otpauth_uri: string;
  manual_key: string;
  expires_at: string;
};

export type RecoveryCodesResponse = {
  recovery_codes: string[];
};

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public retryAfter?: number
  ) {
    super(message);
    this.name = 'APIError';
  }
}
~~~

用以下请求辅助函数替换现有 `request`：

~~~ts
async function fetchResponse(path: string, init?: RequestInit): Promise<Response> {
  return fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    credentials: 'include',
  });
}

async function toAPIError(response: Response): Promise<APIError> {
  const text = await response.text();
  let code = 'request_failed';
  let message = text || `request failed with status ${response.status}`;
  try {
    const body = JSON.parse(text) as { code?: string; message?: string };
    code = body.code ?? code;
    message = body.message ?? message;
  } catch {
    code = 'request_failed';
  }
  const retryHeader = response.headers.get('Retry-After');
  const parsedRetry = retryHeader ? Number(retryHeader) : undefined;
  return new APIError(
    response.status,
    code,
    message,
    parsedRetry !== undefined && Number.isFinite(parsedRetry) ? parsedRetry : undefined
  );
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetchResponse(path, init);
  if (!response.ok) {
    throw await toAPIError(response);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}
~~~

- [ ] **Step 4: 修改登录 API 并新增第二步 API**

替换现有 `login`：

~~~ts
export async function login(username: string, password: string): Promise<LoginResult> {
  const response = await fetchResponse('/api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
  if (!response.ok) {
    throw await toAPIError(response);
  }
  if (response.status === 202) {
    const body = (await response.json()) as {
      challenge_token: string;
      expires_in: number;
    };
    return {
      kind: 'two_factor_required',
      challengeToken: body.challenge_token,
      expiresIn: body.expires_in,
    };
  }
  return { kind: 'authenticated', user: (await response.json()) as User };
}

export function verifyTwoFactor(challengeToken: string, code: string): Promise<User> {
  return request<User>('/api/login/2fa', {
    method: 'POST',
    body: JSON.stringify({ challenge_token: challengeToken, code }),
  });
}
~~~

- [ ] **Step 5: 运行 API 测试**

Run: `cd web && npm test -- --run src/test/api.test.ts`

Expected: PASS。

- [ ] **Step 6: 编写 LoginView 状态机失败测试**

创建 `web/src/test/LoginView.test.tsx`：

~~~tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { APIError } from '../api';
import { LoginView } from '../components/LoginView';

it('moves from password login to totp verification', async () => {
  const onLogin = vi.fn().mockResolvedValue({
    kind: 'two_factor_required',
    challengeToken: 'challenge-1',
    expiresIn: 300,
  });
  const onVerifyTwoFactor = vi.fn().mockResolvedValue(undefined);
  render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);

  await userEvent.type(screen.getByLabelText(/password/i), 'secret');
  await userEvent.click(screen.getByRole('button', { name: /^login$/i }));
  expect(await screen.findByLabelText(/authentication code/i)).toBeInTheDocument();

  await userEvent.type(screen.getByLabelText(/authentication code/i), '123456');
  await userEvent.click(screen.getByRole('button', { name: /verify/i }));
  expect(onVerifyTwoFactor).toHaveBeenCalledWith('challenge-1', '123456');
});

it('supports recovery codes and resets expired challenges', async () => {
  const onLogin = vi.fn().mockResolvedValue({
    kind: 'two_factor_required',
    challengeToken: 'challenge-1',
    expiresIn: 300,
  });
  const onVerifyTwoFactor = vi
    .fn()
    .mockRejectedValue(new APIError(401, 'login_restart_required', 'restart login'));
  render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);

  await userEvent.type(screen.getByLabelText(/password/i), 'secret');
  await userEvent.click(screen.getByRole('button', { name: /^login$/i }));
  await userEvent.click(await screen.findByRole('button', { name: /use recovery code/i }));
  await userEvent.type(screen.getByLabelText(/recovery code/i), 'ABCD-EFGH-JKLM-NPQR');
  await userEvent.click(screen.getByRole('button', { name: /verify/i }));

  expect(await screen.findByLabelText(/username/i)).toBeInTheDocument();
  expect(screen.getByText(/restart login/i)).toBeInTheDocument();
});
~~~

- [ ] **Step 7: 运行 LoginView 测试并确认失败**

Run: `cd web && npm test -- --run src/test/LoginView.test.tsx`

Expected: FAIL，现有组件不接受 `onVerifyTwoFactor`，也没有第二步页面。

- [ ] **Step 8: 用两步状态机替换 LoginView**

将 `LoginView.tsx` 替换为：

~~~tsx
import { ArrowLeft, KeyRound, Terminal } from 'lucide-react';
import { FormEvent, useState } from 'react';
import { APIError, LoginResult } from '../api';

type LoginViewProps = {
  onLogin: (username: string, password: string) => Promise<LoginResult>;
  onVerifyTwoFactor: (challengeToken: string, code: string) => Promise<void>;
};

export function LoginView({ onLogin, onVerifyTwoFactor }: LoginViewProps) {
  const [step, setStep] = useState<'password' | 'second_factor'>('password');
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [challengeToken, setChallengeToken] = useState('');
  const [code, setCode] = useState('');
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  async function submitPassword(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const result = await onLogin(username, password);
      if (result.kind === 'two_factor_required') {
        setChallengeToken(result.challengeToken);
        setPassword('');
        setStep('second_factor');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed');
    } finally {
      setSubmitting(false);
    }
  }

  async function submitSecondFactor(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      await onVerifyTwoFactor(challengeToken, code);
    } catch (err) {
      if (err instanceof APIError && err.code === 'login_restart_required') {
        resetToPassword();
      }
      setError(err instanceof Error ? err.message : 'verification failed');
    } finally {
      setSubmitting(false);
    }
  }

  function resetToPassword() {
    setStep('password');
    setChallengeToken('');
    setCode('');
    setRecoveryMode(false);
  }

  return (
    <main className="login">
      {step === 'password' ? (
        <form onSubmit={submitPassword} className="loginForm">
          <h1 className="loginBrand">
            <Terminal size={22} aria-hidden="true" />
            vibe-terminal
          </h1>
          <label>
            Username
            <input
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label>
            Password
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" className="primaryButton" disabled={submitting}>
            Login
          </button>
        </form>
      ) : (
        <form onSubmit={submitSecondFactor} className="loginForm">
          <h1 className="loginBrand">
            <KeyRound size={22} aria-hidden="true" />
            Two-factor authentication
          </h1>
          <p className="loginHint">
            {recoveryMode ? 'Enter one of your saved recovery codes.' : 'Enter the code from your authenticator app.'}
          </p>
          <label>
            {recoveryMode ? 'Recovery code' : 'Authentication code'}
            <input
              inputMode={recoveryMode ? 'text' : 'numeric'}
              autoComplete="one-time-code"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" className="primaryButton" disabled={submitting || code.trim() === ''}>
            Verify
          </button>
          <button type="button" className="secondaryButton" onClick={() => {
            setRecoveryMode((current) => !current);
            setCode('');
            setError('');
          }}>
            {recoveryMode ? 'Use authentication code' : 'Use recovery code'}
          </button>
          <button type="button" className="loginBackButton" onClick={resetToPassword}>
            <ArrowLeft size={16} aria-hidden="true" />
            Back to login
          </button>
        </form>
      )}
    </main>
  );
}
~~~

- [ ] **Step 9: 运行登录组件测试**

Run: `cd web && npm test -- --run src/test/LoginView.test.tsx src/test/api.test.ts`

Expected: PASS。

- [ ] **Step 10: 提交**

~~~bash
git add web/src/api.ts web/src/components/LoginView.tsx web/src/test/api.test.ts web/src/test/LoginView.test.tsx
git commit -m "feat(web): add two step login flow"
~~~

### Task 10: Security 页面和恢复码界面

**Files:**
- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Modify: `web/src/api.ts`
- Create: `web/src/components/SecurityView.tsx`
- Create: `web/src/test/SecurityView.test.tsx`

- [ ] **Step 1: 编写 SecurityView 失败测试**

创建 `web/src/test/SecurityView.test.tsx`：

~~~tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { SecurityView } from '../components/SecurityView';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getTwoFactorStatus: vi.fn(),
    startTwoFactorSetup: vi.fn(),
    enableTwoFactor: vi.fn(),
    regenerateRecoveryCodes: vi.fn(),
    disableTwoFactor: vi.fn(),
  };
});

const mockedApi = vi.mocked(api);

beforeEach(() => {
  vi.resetAllMocks();
  mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: false, recovery_codes_remaining: 0 });
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

it('enables two factor authentication and shows recovery codes once', async () => {
  mockedApi.startTwoFactorSetup.mockResolvedValue({
    otpauth_uri: 'otpauth://totp/vibe-terminal:admin?secret=ABC',
    manual_key: 'ABCDEF',
    expires_at: new Date(Date.now() + 600_000).toISOString(),
  });
  mockedApi.enableTwoFactor.mockResolvedValue({
    recovery_codes: ['ABCD-EFGH-JKLM-NPQR', 'QRST-UVWX-YZ23-4567'],
  });
  render(<SecurityView />);

  await userEvent.click(await screen.findByRole('button', { name: /enable two-factor/i }));
  await userEvent.type(screen.getByLabelText(/current password/i), 'secret');
  await userEvent.click(screen.getByRole('button', { name: /continue/i }));

  expect(await screen.findByText('ABCDEF')).toBeInTheDocument();
  expect(screen.getByLabelText(/authenticator qr code/i)).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText(/authentication code/i), '123456');
  await userEvent.click(screen.getByRole('button', { name: /confirm and enable/i }));

  expect(await screen.findByText('ABCD-EFGH-JKLM-NPQR')).toBeInTheDocument();
  expect(screen.getByText(/shown only once/i)).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: /done/i }));
  expect(screen.queryByText('ABCD-EFGH-JKLM-NPQR')).not.toBeInTheDocument();
});

it('regenerates and disables two factor authentication with confirmation', async () => {
  mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: true, recovery_codes_remaining: 7 });
  mockedApi.regenerateRecoveryCodes.mockResolvedValue({ recovery_codes: ['NEW1-NEW2-NEW3-NEW4'] });
  mockedApi.disableTwoFactor.mockResolvedValue(undefined);
  render(<SecurityView />);

  expect(await screen.findByText(/7 recovery codes remaining/i)).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: /regenerate recovery codes/i }));
  await userEvent.type(screen.getByLabelText(/current password/i), 'secret');
  await userEvent.type(screen.getByLabelText(/authentication code/i), '123456');
  await userEvent.click(screen.getByRole('button', { name: /^regenerate$/i }));
  expect(await screen.findByText('NEW1-NEW2-NEW3-NEW4')).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: /done/i }));
  await userEvent.click(screen.getByRole('button', { name: /disable two-factor/i }));
  await userEvent.type(screen.getByLabelText(/current password/i), 'secret');
  await userEvent.click(screen.getByRole('button', { name: /^disable$/i }));
  expect(mockedApi.disableTwoFactor).toHaveBeenCalledWith('secret');
});
~~~

- [ ] **Step 2: 运行测试并确认组件缺失**

Run: `cd web && npm test -- --run src/test/SecurityView.test.tsx`

Expected: FAIL，提示 `SecurityView` 不存在。

- [ ] **Step 3: 安装二维码依赖**

Run: `cd web && npm install qrcode.react@^4.2.0`

Expected: `package.json` 和 `package-lock.json` 增加 `qrcode.react`。

- [ ] **Step 4: 增加安全设置 API**

向 `api.ts` 加入：

~~~ts
export function getTwoFactorStatus(): Promise<TwoFactorStatus> {
  return request<TwoFactorStatus>('/api/security/2fa');
}

export function startTwoFactorSetup(password: string): Promise<TwoFactorSetup> {
  return request<TwoFactorSetup>('/api/security/2fa/setup', {
    method: 'POST',
    body: JSON.stringify({ password }),
  });
}

export function enableTwoFactor(code: string): Promise<RecoveryCodesResponse> {
  return request<RecoveryCodesResponse>('/api/security/2fa/enable', {
    method: 'POST',
    body: JSON.stringify({ code }),
  });
}

export function regenerateRecoveryCodes(password: string, code: string): Promise<RecoveryCodesResponse> {
  return request<RecoveryCodesResponse>('/api/security/2fa/recovery-codes', {
    method: 'POST',
    body: JSON.stringify({ password, code }),
  });
}

export function disableTwoFactor(password: string): Promise<void> {
  return request<void>('/api/security/2fa/disable', {
    method: 'POST',
    body: JSON.stringify({ password }),
  });
}
~~~

- [ ] **Step 5: 创建 SecurityView**

创建 `web/src/components/SecurityView.tsx`：

~~~tsx
import { Check, Clipboard, Download, KeyRound, RefreshCw, ShieldCheck, ShieldOff } from 'lucide-react';
import { FormEvent, useEffect, useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import * as api from '../api';
import type { TwoFactorSetup, TwoFactorStatus } from '../api';

type Mode = 'overview' | 'setup_password' | 'setup_code' | 'recovery_codes' | 'regenerate' | 'disable';

export function SecurityView() {
  const [status, setStatus] = useState<TwoFactorStatus | null>(null);
  const [mode, setMode] = useState<Mode>('overview');
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    void refreshStatus();
  }, []);

  async function refreshStatus() {
    setLoading(true);
    setError('');
    try {
      setStatus(await api.getTwoFactorStatus());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load security settings.');
    } finally {
      setLoading(false);
    }
  }

  function resetForm(nextMode: Mode = 'overview') {
    setMode(nextMode);
    setPassword('');
    setCode('');
    setSetup(null);
    setRecoveryCodes([]);
    setError('');
    setCopied(false);
  }

  async function beginSetup(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError('');
    try {
      setSetup(await api.startTwoFactorSetup(password));
      setPassword('');
      setMode('setup_code');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start setup.');
    } finally {
      setLoading(false);
    }
  }

  async function confirmSetup(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError('');
    try {
      const result = await api.enableTwoFactor(code);
      setRecoveryCodes(result.recovery_codes);
      setStatus({ enabled: true, recovery_codes_remaining: result.recovery_codes.length });
      setCode('');
      setSetup(null);
      setMode('recovery_codes');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to enable two-factor authentication.');
    } finally {
      setLoading(false);
    }
  }

  async function regenerate(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError('');
    try {
      const result = await api.regenerateRecoveryCodes(password, code);
      setRecoveryCodes(result.recovery_codes);
      setStatus({ enabled: true, recovery_codes_remaining: result.recovery_codes.length });
      setPassword('');
      setCode('');
      setMode('recovery_codes');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to regenerate recovery codes.');
    } finally {
      setLoading(false);
    }
  }

  async function disable(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError('');
    try {
      await api.disableTwoFactor(password);
      setStatus({ enabled: false, recovery_codes_remaining: 0 });
      setRecoveryCodes([]);
      resetForm();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to disable two-factor authentication.');
    } finally {
      setLoading(false);
    }
  }

  async function copyRecoveryCodes() {
    await navigator.clipboard.writeText(recoveryCodes.join('\n'));
    setCopied(true);
  }

  function downloadRecoveryCodes() {
    const blob = new Blob([recoveryCodes.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = 'vibe-terminal-recovery-codes.txt';
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return (
    <main className="securityPage">
      <header className="securityHeader">
        <div>
          <h1>Security</h1>
          <p>Protect administrator access with an authenticator app.</p>
        </div>
        <button type="button" className="secondaryButton" onClick={refreshStatus} disabled={loading}>
          <RefreshCw size={16} aria-hidden="true" />
          Refresh
        </button>
      </header>

      {error && <p className="error">{error}</p>}

      {mode === 'overview' && status && (
        <section className="securityPanel">
          <div className="securityStatus">
            {status.enabled ? <ShieldCheck size={28} aria-hidden="true" /> : <ShieldOff size={28} aria-hidden="true" />}
            <div>
              <h2>{status.enabled ? 'Two-factor authentication enabled' : 'Two-factor authentication disabled'}</h2>
              <p>
                {status.enabled
                  ? `${status.recovery_codes_remaining} recovery codes remaining`
                  : 'Password-only login is currently allowed.'}
              </p>
            </div>
          </div>
          {status.enabled ? (
            <div className="securityActions">
              <button type="button" className="secondaryButton" onClick={() => resetForm('regenerate')}>
                Regenerate recovery codes
              </button>
              <button type="button" className="dangerButton" onClick={() => resetForm('disable')}>
                Disable two-factor authentication
              </button>
            </div>
          ) : (
            <button type="button" className="primaryButton" onClick={() => resetForm('setup_password')}>
              <KeyRound size={16} aria-hidden="true" />
              Enable two-factor authentication
            </button>
          )}
        </section>
      )}

      {mode === 'setup_password' && (
        <SecurityPasswordForm
          title="Confirm your password"
          submitLabel="Continue"
          password={password}
          setPassword={setPassword}
          loading={loading}
          onSubmit={beginSetup}
          onCancel={() => resetForm()}
        />
      )}

      {mode === 'setup_code' && setup && (
        <section className="securityPanel setupGrid">
          <div className="qrCard" aria-label="Authenticator QR code">
            <QRCodeSVG value={setup.otpauth_uri} size={196} level="M" />
          </div>
          <form className="securityForm" onSubmit={confirmSetup}>
            <h2>Scan with your authenticator</h2>
            <p>Or enter this key manually:</p>
            <code className="manualKey">{setup.manual_key}</code>
            <label>
              Authentication code
              <input
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(event) => setCode(event.target.value)}
              />
            </label>
            <div className="securityActions">
              <button type="submit" className="primaryButton" disabled={loading || code.trim() === ''}>
                Confirm and enable
              </button>
              <button type="button" className="secondaryButton" onClick={() => resetForm()}>
                Cancel
              </button>
            </div>
          </form>
        </section>
      )}

      {mode === 'recovery_codes' && (
        <section className="securityPanel">
          <h2>Save your recovery codes</h2>
          <p>These codes are shown only once. Store them somewhere safe.</p>
          <div className="recoveryCodeGrid">
            {recoveryCodes.map((item) => <code key={item}>{item}</code>)}
          </div>
          <div className="securityActions">
            <button type="button" className="secondaryButton" onClick={copyRecoveryCodes}>
              {copied ? <Check size={16} aria-hidden="true" /> : <Clipboard size={16} aria-hidden="true" />}
              {copied ? 'Copied' : 'Copy all'}
            </button>
            <button type="button" className="secondaryButton" onClick={downloadRecoveryCodes}>
              <Download size={16} aria-hidden="true" />
              Download
            </button>
            <button type="button" className="primaryButton" onClick={() => resetForm()}>
              Done
            </button>
          </div>
        </section>
      )}

      {mode === 'regenerate' && (
        <form className="securityPanel securityForm" onSubmit={regenerate}>
          <h2>Regenerate recovery codes</h2>
          <label>
            Current password
            <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
          </label>
          <label>
            Authentication code
            <input inputMode="numeric" value={code} onChange={(event) => setCode(event.target.value)} />
          </label>
          <div className="securityActions">
            <button type="submit" className="primaryButton" disabled={loading}>Regenerate</button>
            <button type="button" className="secondaryButton" onClick={() => resetForm()}>Cancel</button>
          </div>
        </form>
      )}

      {mode === 'disable' && (
        <SecurityPasswordForm
          title="Disable two-factor authentication"
          submitLabel="Disable"
          password={password}
          setPassword={setPassword}
          loading={loading}
          danger
          onSubmit={disable}
          onCancel={() => resetForm()}
        />
      )}
    </main>
  );
}

function SecurityPasswordForm({
  title,
  submitLabel,
  password,
  setPassword,
  loading,
  danger = false,
  onSubmit,
  onCancel,
}: {
  title: string;
  submitLabel: string;
  password: string;
  setPassword: (value: string) => void;
  loading: boolean;
  danger?: boolean;
  onSubmit: (event: FormEvent) => Promise<void>;
  onCancel: () => void;
}) {
  return (
    <form className="securityPanel securityForm" onSubmit={onSubmit}>
      <h2>{title}</h2>
      <label>
        Current password
        <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
      </label>
      <div className="securityActions">
        <button type="submit" className={danger ? 'dangerButton' : 'primaryButton'} disabled={loading || password === ''}>
          {submitLabel}
        </button>
        <button type="button" className="secondaryButton" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}
~~~

- [ ] **Step 6: 运行 SecurityView 测试**

Run: `cd web && npm test -- --run src/test/SecurityView.test.tsx`

Expected: PASS。

- [ ] **Step 7: 提交**

~~~bash
git add web/package.json web/package-lock.json web/src/api.ts web/src/components/SecurityView.tsx web/src/test/SecurityView.test.tsx
git commit -m "feat(web): add two factor security settings"
~~~

### Task 11: App 集成、导航和样式

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`
- Modify: `web/src/test/App.test.tsx`

- [ ] **Step 1: 编写 App 集成失败测试**

在 `App.test.tsx` 的 API mock 中增加：

~~~ts
verifyTwoFactor: vi.fn(),
getTwoFactorStatus: vi.fn(),
startTwoFactorSetup: vi.fn(),
enableTwoFactor: vi.fn(),
regenerateRecoveryCodes: vi.fn(),
disableTwoFactor: vi.fn(),
~~~

并追加：

~~~tsx
it('opens the security settings from the sidebar', async () => {
  mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: false, recovery_codes_remaining: 0 });
  render(
    <AppView
      user={{ id: 'user-1', username: 'admin' }}
      devices={[]}
      sessions={{}}
      onLogin={vi.fn()}
      onVerifyTwoFactor={vi.fn()}
      onCloseSession={vi.fn()}
      onCreateSession={vi.fn()}
      onRenameSession={vi.fn()}
      agentTokens={[]}
      createdAgentToken={null}
      tokenLoading={false}
      tokenError={null}
      onCreateAgentToken={vi.fn()}
      onRevokeAgentToken={vi.fn()}
      onRefreshAgentTokens={vi.fn()}
    />
  );

  await userEvent.click(screen.getByRole('button', { name: /^security$/i }));
  expect(await screen.findByRole('heading', { name: /^security$/i })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: /enable two-factor/i })).toBeInTheDocument();
});
~~~

- [ ] **Step 2: 运行 App 测试并确认失败**

Run: `cd web && npm test -- --run src/test/App.test.tsx -t 'opens the security settings'`

Expected: FAIL，没有 Security 导航和页面。

- [ ] **Step 3: 集成登录回调**

在 `App.tsx` 中导入 `ShieldCheck`、`LoginResult` 和 `SecurityView`，修改登录函数：

~~~tsx
async function handleLogin(username: string, password: string): Promise<LoginResult> {
  const result = await api.login(username, password);
  if (result.kind === 'authenticated') {
    setUser(result.user);
  }
  return result;
}

async function handleVerifyTwoFactor(challengeToken: string, code: string) {
  setUser(await api.verifyTwoFactor(challengeToken, code));
}
~~~

向 `AppView` 传入 `onVerifyTwoFactor={handleVerifyTwoFactor}`。

把 `ViewMode` 和 AppView props 修改为：

~~~tsx
type ViewMode = 'terminals' | 'agentTokens' | 'security';

onLogin: (username: string, password: string) => Promise<LoginResult>;
onVerifyTwoFactor?: (challengeToken: string, code: string) => Promise<void>;
~~~

在参数解构中为旧测试提供安全默认值：

~~~tsx
onVerifyTwoFactor = async () => {},
~~~

未登录分支改为：

~~~tsx
if (!user) {
  return <LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />;
}
~~~

- [ ] **Step 4: 集成 Security 导航和页面**

在侧边导航加入：

~~~tsx
<button className={viewMode === 'security' ? 'active' : ''} onClick={() => setViewMode('security')}>
  <ShieldCheck size={16} aria-hidden="true" />
  Security
</button>
~~~

把内容分支改为：

~~~tsx
{viewMode === 'terminals' ? (
  <TerminalTabs
    sessions={localSessions}
    onSessionsChange={setLocalSessions}
    onCloseSession={onCloseSession}
    onRenameSession={onRenameSession}
  />
) : viewMode === 'agentTokens' ? (
  <AgentTokenManager
    tokens={agentTokens}
    loading={tokenLoading}
    error={tokenError}
    createdToken={createdAgentToken}
    onCreate={onCreateAgentToken}
    onRevoke={onRevokeAgentToken}
    onDelete={onDeleteAgentToken}
    onRefresh={onRefreshAgentTokens}
  />
) : (
  <SecurityView />
)}
~~~

- [ ] **Step 5: 增加登录和安全页样式**

向 `styles.css` 追加：

~~~css
.loginHint {
  margin: 0;
  color: var(--text-dim);
  line-height: 1.5;
}

.loginBackButton {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 0;
  background: transparent;
  color: var(--text-dim);
}

.securityPage {
  min-width: 0;
  overflow: auto;
  padding: 28px;
}

.securityHeader {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 20px;
}

.securityHeader h1,
.securityPanel h2 {
  margin: 0;
}

.securityHeader p,
.securityPanel p {
  color: var(--text-dim);
}

.securityPanel {
  display: grid;
  gap: 18px;
  max-width: 820px;
  padding: 24px;
  border: 1px solid var(--glass-border);
  border-radius: var(--r-card);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}

.securityStatus {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.securityStatus svg {
  color: var(--accent);
}

.securityActions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.securityPage .primaryButton {
  min-height: 36px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border-radius: var(--r-ctrl);
  padding: 0 14px;
  cursor: pointer;
}

.securityForm label {
  display: grid;
  gap: 7px;
  color: var(--text-dim);
}

.securityForm input {
  min-height: 40px;
  border: 1px solid var(--glass-border-strong);
  border-radius: var(--r-ctrl);
  background: rgba(8, 9, 16, 0.55);
  color: var(--text);
  padding: 0 12px;
}

.setupGrid {
  grid-template-columns: minmax(220px, 260px) minmax(0, 1fr);
  align-items: center;
}

.qrCard {
  display: grid;
  place-items: center;
  padding: 18px;
  border-radius: var(--r-card);
  background: #fff;
}

.manualKey,
.recoveryCodeGrid code {
  font-family: var(--font-mono);
  letter-spacing: 0.08em;
}

.manualKey {
  overflow-wrap: anywhere;
  padding: 12px;
  border-radius: var(--r-ctrl);
  background: rgba(8, 9, 16, 0.7);
}

.recoveryCodeGrid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.recoveryCodeGrid code {
  padding: 12px;
  border: 1px solid var(--glass-border);
  border-radius: var(--r-ctrl);
  background: rgba(8, 9, 16, 0.55);
  text-align: center;
}

@media (max-width: 820px) {
  .securityPage {
    padding: 18px;
  }

  .setupGrid {
    grid-template-columns: 1fr;
  }

  .securityHeader {
    flex-direction: column;
  }

  .recoveryCodeGrid {
    grid-template-columns: 1fr;
  }
}
~~~

将 `.securityPanel` 加入现有不支持 backdrop-filter 时的回退选择器。

- [ ] **Step 6: 运行前端全量测试和构建**

Run: `cd web && npm test -- --run && npm run build`

Expected: 所有 Vitest 测试通过，TypeScript 和 Vite 构建退出码为 0。

- [ ] **Step 7: 提交**

~~~bash
git add web/src/App.tsx web/src/styles.css web/src/test/App.test.tsx
git commit -m "feat(web): integrate two factor security navigation"
~~~

### Task 12: 文档、格式化和完整验证

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-07-10-web-login-2fa.md` only for checked boxes while executing

- [ ] **Step 1: 更新 README 的功能和安全说明**

在特性列表增加：

~~~markdown
- 可选的 TOTP 两步验证，支持验证器应用和一次性恢复码。
~~~

在配置和部署安全章节增加以下明确内容：

~~~markdown
## Web 两步验证

管理员登录后可从左侧 `Security` 页面启用 TOTP 两步验证。扫码应用可以使用 Google Authenticator、Microsoft Authenticator、1Password 或其他兼容 RFC 6238 的验证器。

启用成功时会显示 10 个一次性恢复码。恢复码只显示一次，应离线保存在密码管理器或其他安全位置。丢失验证器时，可以使用未消费的恢复码登录，然后关闭并重新配置 2FA。

TOTP 依赖准确的服务器时间，生产环境必须启用 NTP。TOTP 密钥使用从 `session_secret` 派生的密钥加密；启用 2FA 后必须稳定保存 `session_secret`，直接更换或丢失该值会导致已有 TOTP 设置无法解密。

2FA 不替代 HTTPS、强密码、网络访问控制和终端进程隔离。
~~~

- [ ] **Step 2: 格式化后端代码**

Run: `cd server && gofmt -w internal/auth/totp.go internal/auth/totp_test.go internal/auth/two_factor_crypto.go internal/auth/two_factor_crypto_test.go internal/auth/challenge.go internal/auth/challenge_test.go internal/auth/limiter.go internal/auth/limiter_test.go internal/store/store.go internal/store/store_test.go internal/httpapi/router.go internal/httpapi/router_2fa.go internal/httpapi/router_2fa_test.go cmd/server/main.go`

Expected: 命令退出码为 0。

- [ ] **Step 3: 运行后端测试**

Run: `cd server && go test ./...`

Expected: PASS，0 failures。

- [ ] **Step 4: 运行前端测试和构建**

Run: `cd web && npm test -- --run && npm run build`

Expected: PASS，TypeScript 无错误，Vite 构建成功。

- [ ] **Step 5: 运行仓库完整检查**

Run: `make test`

Expected:

- Go 服务端测试通过；
- Rust agent 测试通过；
- Web 测试和构建通过；
- `docker compose config` 退出码为 0。

- [ ] **Step 6: 检查敏感信息和提交范围**

Run: `rg -n "BASE32SECRET|recovery_codes|challenge_token" server web/src`

Expected: 只出现类型、测试夹具、响应字段和受控 UI 状态；日志语句中没有密钥、验证码、恢复码明文或挑战令牌。

Run: `git diff --check`

Expected: 无空白错误。

Run: `git status --short`

Expected: 只包含本计划实现涉及的文件以及用户原有的未提交文件。

- [ ] **Step 7: 提交文档和最终修正**

~~~bash
git add README.md
git commit -m "docs: document optional web two factor auth"
~~~
