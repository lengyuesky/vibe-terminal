package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const loginChallengeVersion = 2

// LoginChallenge 表示已验证的登录挑战。
type LoginChallenge struct {
	JTI             string
	UserID          string
	ConfigurationID string
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

type challengePayload struct {
	Version         int    `json:"version"`
	JTI             string `json:"jti"`
	UserID          string `json:"user_id"`
	ConfigurationID string `json:"configuration_id"`
	IssuedAt        int64  `json:"issued_at"`
	ExpiresAt       int64  `json:"expires_at"`
}

// TwoFactorManager 管理双因素认证密钥、恢复码和登录挑战。
type TwoFactorManager struct {
	cipher       *secretCipher
	recoveryKey  []byte
	challengeKey []byte
	now          func() time.Time
}

// NewTwoFactorManager 使用根密钥创建双因素认证管理器。
func NewTwoFactorManager(root []byte, now func() time.Time) (*TwoFactorManager, error) {
	if now == nil {
		now = time.Now
	}

	encryptionKey, err := derivePurposeKey(root, "totp-encryption")
	if err != nil {
		return nil, fmt.Errorf("派生 TOTP 加密密钥失败：%w", err)
	}
	recoveryKey, err := derivePurposeKey(root, "recovery-codes")
	if err != nil {
		return nil, fmt.Errorf("派生恢复码密钥失败：%w", err)
	}
	challengeKey, err := derivePurposeKey(root, "login-challenge")
	if err != nil {
		return nil, fmt.Errorf("派生登录挑战密钥失败：%w", err)
	}
	secretCipher, err := newSecretCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("创建 TOTP 密钥加密器失败：%w", err)
	}

	return &TwoFactorManager{
		cipher:       secretCipher,
		recoveryKey:  recoveryKey,
		challengeKey: challengeKey,
		now:          now,
	}, nil
}

// GenerateSetup 生成 TOTP 密钥、配置 URI 和加密后的密钥。
func (m *TwoFactorManager) GenerateSetup(issuer, account string) (secret, uri, encrypted string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", "", fmt.Errorf("生成 TOTP 密钥失败：%w", err)
	}
	uri = TOTPProvisioningURI(issuer, account, secret)
	encrypted, err = m.cipher.Encrypt(secret)
	if err != nil {
		return "", "", "", fmt.Errorf("加密 TOTP 密钥失败：%w", err)
	}
	return secret, uri, encrypted, nil
}

// DecryptSecret 解密已保存的 TOTP 密钥。
func (m *TwoFactorManager) DecryptSecret(value string) (string, error) {
	return m.cipher.Decrypt(value)
}

// GenerateRecoveryCodes 生成十个恢复码及其用户绑定哈希。
func (m *TwoFactorManager) GenerateRecoveryCodes(userID string) ([]string, []string, error) {
	rawCodes, err := generateRecoveryCodes(10)
	if err != nil {
		return nil, nil, err
	}

	hashes := make([]string, len(rawCodes))
	for i, rawCode := range rawCodes {
		hashes[i] = m.RecoveryCodeHash(userID, rawCode)
	}
	return rawCodes, hashes, nil
}

// RecoveryCodeHash 计算单个恢复码的用户绑定哈希。
func (m *TwoFactorManager) RecoveryCodeHash(userID, code string) string {
	return recoveryCodeHash(m.recoveryKey, userID, code)
}

// IssueLoginChallenge 签发五分钟有效的登录挑战。
func (m *TwoFactorManager) IssueLoginChallenge(userID, configurationID string) (string, error) {
	if userID == "" || configurationID == "" {
		return "", errors.New("login challenge identifiers are required")
	}

	now := m.now().UTC()
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", fmt.Errorf("生成登录挑战 JTI 失败：%w", err)
	}
	payload := challengePayload{
		Version:         loginChallengeVersion,
		JTI:             base64.RawURLEncoding.EncodeToString(jtiBytes),
		UserID:          userID,
		ConfigurationID: configurationID,
		IssuedAt:        now.Unix(),
		ExpiresAt:       now.Add(5 * time.Minute).Unix(),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("编码登录挑战载荷失败：%w", err)
	}

	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signature := base64.RawURLEncoding.EncodeToString(m.loginChallengeSignature(encodedPayload))
	return encodedPayload + "." + signature, nil
}

// VerifyLoginChallenge 验证登录挑战并返回其中的用户和配置信息。
func (m *TwoFactorManager) VerifyLoginChallenge(token string) (LoginChallenge, error) {
	return m.VerifyLoginChallengeAt(token, m.now())
}

// VerifyLoginChallengeAt 使用调用方捕获的时间验证登录挑战。
func (m *TwoFactorManager) VerifyLoginChallengeAt(token string, now time.Time) (LoginChallenge, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return LoginChallenge{}, errors.New("登录挑战必须恰好包含两段")
	}

	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return LoginChallenge{}, fmt.Errorf("解码登录挑战签名失败：%w", err)
	}
	expectedSignature := m.loginChallengeSignature(parts[0])
	if !hmac.Equal(signature, expectedSignature) {
		return LoginChallenge{}, errors.New("登录挑战签名无效")
	}

	payloadJSON, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return LoginChallenge{}, fmt.Errorf("解码登录挑战载荷失败：%w", err)
	}
	var payload challengePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return LoginChallenge{}, fmt.Errorf("解析登录挑战载荷失败：%w", err)
	}
	if payload.Version != loginChallengeVersion {
		return LoginChallenge{}, errors.New("登录挑战版本无效")
	}
	if payload.JTI == "" || payload.UserID == "" || payload.ConfigurationID == "" {
		return LoginChallenge{}, errors.New("登录挑战缺少 JTI、用户或配置标识")
	}

	now = now.UTC()
	issuedAt := time.Unix(payload.IssuedAt, 0).UTC()
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	if now.After(expiresAt) {
		return LoginChallenge{}, errors.New("登录挑战已过期")
	}
	if issuedAt.After(now.Add(time.Minute)) {
		return LoginChallenge{}, errors.New("登录挑战签发时间超出允许范围")
	}

	return LoginChallenge{
		JTI:             payload.JTI,
		UserID:          payload.UserID,
		ConfigurationID: payload.ConfigurationID,
		IssuedAt:        issuedAt,
		ExpiresAt:       expiresAt,
	}, nil
}

func (m *TwoFactorManager) loginChallengeSignature(encodedPayload string) []byte {
	mac := hmac.New(sha256.New, m.challengeKey)
	_, _ = mac.Write([]byte(encodedPayload))
	return mac.Sum(nil)
}
