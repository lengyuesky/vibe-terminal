package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testTwoFactorRoot = []byte("0123456789abcdef0123456789abcdef")

func TestTwoFactorManagerDerivesPurposeKeysAndDefaultsClock(t *testing.T) {
	manager, err := NewTwoFactorManager(testTwoFactorRoot, nil)
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	if manager.now == nil {
		t.Fatal("默认时钟不应为空")
	}

	wantRecoveryKey, err := derivePurposeKey(testTwoFactorRoot, "recovery-codes")
	if err != nil {
		t.Fatalf("派生期望恢复码密钥失败：%v", err)
	}
	if !bytes.Equal(manager.recoveryKey, wantRecoveryKey) {
		t.Fatal("恢复码密钥未使用 recovery-codes 用途派生")
	}

	wantChallengeKey, err := derivePurposeKey(testTwoFactorRoot, "login-challenge")
	if err != nil {
		t.Fatalf("派生期望登录挑战密钥失败：%v", err)
	}
	if !bytes.Equal(manager.challengeKey, wantChallengeKey) {
		t.Fatal("登录挑战密钥未使用 login-challenge 用途派生")
	}
}

func TestTwoFactorManagerGenerateSetup(t *testing.T) {
	manager, err := NewTwoFactorManager(testTwoFactorRoot, nil)
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	secret, uri, encrypted, err := manager.GenerateSetup("Vibe Terminal", "user@example.com")
	if err != nil {
		t.Fatalf("生成双因素设置失败：%v", err)
	}
	if secret == "" {
		t.Fatal("TOTP 密钥不应为空")
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("配置 URI = %q，期望 otpauth://totp/ 前缀", uri)
	}
	if encrypted == "" {
		t.Fatal("加密后的 TOTP 密钥不应为空")
	}

	decrypted, err := manager.DecryptSecret(encrypted)
	if err != nil {
		t.Fatalf("解密 TOTP 密钥失败：%v", err)
	}
	if decrypted != secret {
		t.Fatalf("解密后的 TOTP 密钥 = %q，期望 %q", decrypted, secret)
	}

	encryptionKey, err := derivePurposeKey(testTwoFactorRoot, "totp-encryption")
	if err != nil {
		t.Fatalf("派生期望 TOTP 加密密钥失败：%v", err)
	}
	expectedCipher, err := newSecretCipher(encryptionKey)
	if err != nil {
		t.Fatalf("创建期望 TOTP 加密器失败：%v", err)
	}
	decrypted, err = expectedCipher.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("使用 totp-encryption 用途密钥解密失败：%v", err)
	}
	if decrypted != secret {
		t.Fatalf("用途密钥解密结果 = %q，期望 %q", decrypted, secret)
	}
}

func TestTwoFactorManagerGenerateRecoveryCodes(t *testing.T) {
	manager, err := NewTwoFactorManager(testTwoFactorRoot, nil)
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	rawCodes, hashes, err := manager.GenerateRecoveryCodes("user-1")
	if err != nil {
		t.Fatalf("生成恢复码失败：%v", err)
	}
	if len(rawCodes) != 10 {
		t.Fatalf("原始恢复码数量 = %d，期望 10", len(rawCodes))
	}
	if len(hashes) != 10 {
		t.Fatalf("恢复码哈希数量 = %d，期望 10", len(hashes))
	}

	for i, rawCode := range rawCodes {
		if rawCode == "" {
			t.Fatalf("第 %d 个原始恢复码为空", i)
		}
		if hashes[i] == "" {
			t.Fatalf("第 %d 个恢复码哈希为空", i)
		}
		if got := manager.RecoveryCodeHash("user-1", rawCode); got != hashes[i] {
			t.Fatalf("第 %d 个恢复码哈希 = %q，期望 %q", i, got, hashes[i])
		}
		if got := manager.RecoveryCodeHash("user-2", rawCode); got == hashes[i] {
			t.Fatalf("第 %d 个恢复码哈希未绑定用户", i)
		}
	}
}

func TestLoginChallengeRoundTripPreservesConfigurationID(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	token, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发登录挑战失败：%v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("登录挑战包含 %d 段，期望 2 段", len(parts))
	}

	payloadJSON, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		t.Fatalf("解码登录挑战载荷失败：%v", err)
	}
	var payloadFields map[string]json.RawMessage
	if err := json.Unmarshal(payloadJSON, &payloadFields); err != nil {
		t.Fatalf("解析登录挑战载荷失败：%v", err)
	}
	wantFields := []string{"version", "jti", "user_id", "configuration_id", "issued_at", "expires_at"}
	if len(payloadFields) != len(wantFields) {
		t.Fatalf("登录挑战载荷字段数量 = %d，期望 %d", len(payloadFields), len(wantFields))
	}
	for _, field := range wantFields {
		if _, ok := payloadFields[field]; !ok {
			t.Fatalf("登录挑战载荷缺少字段 %q", field)
		}
	}

	var payload challengePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("解析登录挑战载荷字段失败：%v", err)
	}
	if loginChallengeVersion != 2 || payload.Version != loginChallengeVersion {
		t.Fatalf("登录挑战版本 = %d，期望 %d", payload.Version, loginChallengeVersion)
	}
	if payload.JTI == "" {
		t.Fatal("登录挑战载荷缺少随机 JTI")
	}
	if payload.UserID != "user-1" {
		t.Fatalf("载荷用户 ID = %q，期望 %q", payload.UserID, "user-1")
	}
	if payload.ConfigurationID != "configuration-1" {
		t.Fatalf("载荷配置 ID = %q，期望 %q", payload.ConfigurationID, "configuration-1")
	}
	if payload.IssuedAt != now.UTC().Unix() {
		t.Fatalf("载荷签发时间 = %d，期望 %d", payload.IssuedAt, now.UTC().Unix())
	}
	if payload.ExpiresAt != now.UTC().Add(5*time.Minute).Unix() {
		t.Fatalf("载荷过期时间 = %d，期望 %d", payload.ExpiresAt, now.UTC().Add(5*time.Minute).Unix())
	}

	challenge, err := manager.VerifyLoginChallenge(token)
	if err != nil {
		t.Fatalf("验证登录挑战失败：%v", err)
	}
	if challenge.UserID != "user-1" {
		t.Fatalf("挑战用户 ID = %q，期望 %q", challenge.UserID, "user-1")
	}
	if challenge.JTI != payload.JTI {
		t.Fatalf("挑战 JTI = %q，期望 %q", challenge.JTI, payload.JTI)
	}
	if challenge.ConfigurationID != "configuration-1" {
		t.Fatalf("挑战配置 ID = %q，期望 %q", challenge.ConfigurationID, "configuration-1")
	}
	if !challenge.IssuedAt.Equal(now.UTC()) || challenge.IssuedAt.Location() != time.UTC {
		t.Fatalf("挑战签发时间 = %v，期望 UTC 时间 %v", challenge.IssuedAt, now.UTC())
	}
	if !challenge.ExpiresAt.Equal(now.UTC().Add(5*time.Minute)) || challenge.ExpiresAt.Location() != time.UTC {
		t.Fatalf("挑战过期时间 = %v，期望 UTC 时间 %v", challenge.ExpiresAt, now.UTC().Add(5*time.Minute))
	}
}

func TestLoginChallengesUseUniqueJTI(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	firstToken, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发第一个登录挑战失败：%v", err)
	}
	secondToken, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发第二个登录挑战失败：%v", err)
	}
	first, err := manager.VerifyLoginChallenge(firstToken)
	if err != nil {
		t.Fatalf("验证第一个登录挑战失败：%v", err)
	}
	second, err := manager.VerifyLoginChallenge(secondToken)
	if err != nil {
		t.Fatalf("验证第二个登录挑战失败：%v", err)
	}
	if first.JTI == "" || second.JTI == "" || first.JTI == second.JTI {
		t.Fatalf("登录挑战 JTI 不唯一：first=%q second=%q", first.JTI, second.JTI)
	}
}

func TestLoginChallengeIssueRejectsMissingIdentifiers(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	tests := []struct {
		name            string
		userID          string
		configurationID string
	}{
		{name: "用户 ID 为空", configurationID: "configuration-1"},
		{name: "配置 ID 为空", userID: "user-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := manager.IssueLoginChallenge(tt.userID, tt.configurationID)
			if err == nil {
				t.Fatalf("标识为空时不应签发登录挑战，实际令牌 = %q", token)
			}
		})
	}
}

func TestLoginChallengeExpiresAfterFiveMinutes(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	token, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发登录挑战失败：%v", err)
	}

	now = now.Add(6 * time.Minute)
	if _, err := manager.VerifyLoginChallenge(token); err == nil {
		t.Fatal("签发六分钟后的登录挑战应已过期")
	}
}

func TestVerifyLoginChallengeAtUsesExplicitTime(t *testing.T) {
	issuedAt := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	managerNow := issuedAt
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return managerNow })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	token, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发登录挑战失败：%v", err)
	}
	managerNow = issuedAt.Add(6 * time.Minute)

	if _, err := manager.VerifyLoginChallengeAt(token, issuedAt.Add(4*time.Minute)); err != nil {
		t.Fatalf("显式时间仍在有效期内时验证失败：%v", err)
	}
}

func TestLoginChallengeExpirationBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	token := signChallengePayloadForTest(t, manager, challengePayload{
		Version:         loginChallengeVersion,
		JTI:             "expiration-boundary-jti",
		UserID:          "user-1",
		ConfigurationID: "configuration-1",
		IssuedAt:        now.Add(-5 * time.Minute).Unix(),
		ExpiresAt:       now.Unix(),
	})

	if _, err := manager.VerifyLoginChallenge(token); err != nil {
		t.Fatalf("当前时间等于过期时间时验证失败：%v", err)
	}
	now = now.Add(time.Second)
	if _, err := manager.VerifyLoginChallenge(token); err == nil {
		t.Fatal("当前时间晚于过期时间时不应验证成功")
	}
}

func TestLoginChallengeIssuedAtClockSkewBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}

	atBoundary := signChallengePayloadForTest(t, manager, challengePayload{
		Version:         loginChallengeVersion,
		JTI:             "clock-skew-boundary-jti",
		UserID:          "user-1",
		ConfigurationID: "configuration-1",
		IssuedAt:        now.Add(time.Minute).Unix(),
		ExpiresAt:       now.Add(5 * time.Minute).Unix(),
	})
	if _, err := manager.VerifyLoginChallenge(atBoundary); err != nil {
		t.Fatalf("签发时间正好领先一分钟时验证失败：%v", err)
	}

	beyondBoundary := signChallengePayloadForTest(t, manager, challengePayload{
		Version:         loginChallengeVersion,
		JTI:             "clock-skew-beyond-jti",
		UserID:          "user-1",
		ConfigurationID: "configuration-1",
		IssuedAt:        now.Add(time.Minute + time.Second).Unix(),
		ExpiresAt:       now.Add(5 * time.Minute).Unix(),
	})
	if _, err := manager.VerifyLoginChallenge(beyondBoundary); err == nil {
		t.Fatal("签发时间领先超过一分钟时不应验证成功")
	}
}

func TestLoginChallengeRejectsTamperedLastCharacter(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	token, err := manager.IssueLoginChallenge("user-1", "configuration-1")
	if err != nil {
		t.Fatalf("签发登录挑战失败：%v", err)
	}

	replacement := byte('A')
	if token[len(token)-1] == replacement {
		replacement = 'Q'
	}
	tampered := token[:len(token)-1] + string(replacement)
	if _, err := manager.VerifyLoginChallenge(tampered); err == nil {
		t.Fatal("修改最后一个字符的登录挑战不应验证成功")
	}
}

func TestLoginChallengeRejectsMalformedAndInvalidPayloads(t *testing.T) {
	now := time.Date(2026, time.July, 10, 1, 30, 0, 0, time.UTC)
	manager, err := NewTwoFactorManager(testTwoFactorRoot, func() time.Time { return now })
	if err != nil {
		t.Fatalf("创建双因素管理器失败：%v", err)
	}
	validPayload := challengePayload{
		Version:         loginChallengeVersion,
		JTI:             "valid-jti",
		UserID:          "user-1",
		ConfigurationID: "configuration-1",
		IssuedAt:        now.Unix(),
		ExpiresAt:       now.Add(5 * time.Minute).Unix(),
	}
	validToken := signChallengePayloadForTest(t, manager, validPayload)

	tests := []struct {
		name  string
		token string
	}{
		{name: "段数不足", token: "only-one-segment"},
		{name: "段数过多", token: validToken + ".extra"},
		{name: "签名不是 RawURL Base64", token: strings.Split(validToken, ".")[0] + ".%"},
		{name: "载荷不是 RawURL Base64", token: signChallengeSegmentForTest(manager, "%")},
		{name: "载荷不是 JSON", token: signChallengeSegmentForTest(manager, base64.RawURLEncoding.EncodeToString([]byte("not-json")))},
		{name: "版本不受支持", token: signChallengePayloadForTest(t, manager, challengePayload{
			Version: 1, JTI: "unsupported-version-jti", UserID: "user-1", ConfigurationID: "configuration-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
		})},
		{name: "JTI 为空", token: signChallengePayloadForTest(t, manager, challengePayload{
			Version: loginChallengeVersion, UserID: "user-1", ConfigurationID: "configuration-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
		})},
		{name: "用户 ID 为空", token: signChallengePayloadForTest(t, manager, challengePayload{
			Version: loginChallengeVersion, JTI: "missing-user-jti", ConfigurationID: "configuration-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
		})},
		{name: "配置 ID 为空", token: signChallengePayloadForTest(t, manager, challengePayload{
			Version: loginChallengeVersion, JTI: "missing-configuration-jti", UserID: "user-1", IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
		})},
		{name: "签发时间超过时钟容差", token: signChallengePayloadForTest(t, manager, challengePayload{
			Version: loginChallengeVersion, JTI: "clock-skew-jti", UserID: "user-1", ConfigurationID: "configuration-1", IssuedAt: now.Add(time.Minute + time.Second).Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := manager.VerifyLoginChallenge(tt.token); err == nil {
				t.Fatal("无效登录挑战不应验证成功")
			}
		})
	}
}

func signChallengePayloadForTest(t *testing.T, manager *TwoFactorManager, payload challengePayload) string {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码测试登录挑战载荷失败：%v", err)
	}
	return signChallengeSegmentForTest(manager, base64.RawURLEncoding.EncodeToString(payloadJSON))
}

func signChallengeSegmentForTest(manager *TwoFactorManager, encodedPayload string) string {
	mac := hmac.New(sha256.New, manager.challengeKey)
	_, _ = mac.Write([]byte(encodedPayload))
	return encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
