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
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const encryptedSecretVersion = "v1."

type secretCipher struct {
	aead cipher.AEAD
}

func derivePurposeKey(root []byte, purpose string) ([]byte, error) {
	key := make([]byte, 32)
	reader := hkdf.New(sha256.New, root, nil, []byte("vibe-terminal/"+purpose+"/v1"))
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("派生用途密钥失败：%w", err)
	}
	return key, nil
}

func newSecretCipher(key []byte) (*secretCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 密钥失败：%w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 加密器失败：%w", err)
	}
	return &secretCipher{aead: aead}, nil
}

func (c *secretCipher) Encrypt(secret string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 GCM nonce 失败：%w", err)
	}

	sealed := c.aead.Seal(nil, nonce, []byte(secret), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return encryptedSecretVersion + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c *secretCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedSecretVersion) {
		return "", errors.New("不支持的密文版本")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, encryptedSecretVersion))
	if err != nil {
		return "", fmt.Errorf("解码密文失败：%w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return "", errors.New("密文短于 GCM nonce")
	}

	plaintext, err := c.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("验证密文失败：%w", err)
	}
	return string(plaintext), nil
}

func generateRecoveryCodes(count int) ([]string, error) {
	if count < 0 {
		return nil, errors.New("恢复码数量不能为负数")
	}

	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	codes := make([]string, count)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("生成恢复码随机值失败：%w", err)
		}
		encoded := encoding.EncodeToString(raw)
		codes[i] = encoded[:4] + "-" + encoded[4:8] + "-" + encoded[8:12] + "-" + encoded[12:16]
	}
	return codes, nil
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(code)
	code = strings.ReplaceAll(code, "-", "")
	return strings.ReplaceAll(code, " ", "")
}

func recoveryCodeHash(key []byte, userID, code string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(userID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(normalizeRecoveryCode(code)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
