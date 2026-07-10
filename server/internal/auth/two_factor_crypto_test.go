package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/hkdf"
)

func TestSecretCipherDerivePurposeKey(t *testing.T) {
	root := []byte("0123456789abcdef0123456789abcdef")
	want := make([]byte, 32)
	reader := hkdf.New(sha256.New, root, nil, []byte("vibe-terminal/two-factor/v1"))
	if _, err := io.ReadFull(reader, want); err != nil {
		t.Fatalf("计算期望密钥失败：%v", err)
	}

	got, err := derivePurposeKey(root, "two-factor")
	if err != nil {
		t.Fatalf("派生用途密钥失败：%v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("派生密钥 = %x，期望 %x", got, want)
	}
}

func TestSecretCipherRoundTrip(t *testing.T) {
	key, err := derivePurposeKey([]byte("0123456789abcdef0123456789abcdef"), "two-factor")
	if err != nil {
		t.Fatalf("派生用途密钥失败：%v", err)
	}
	cipher, err := newSecretCipher(key)
	if err != nil {
		t.Fatalf("创建密钥加密器失败：%v", err)
	}

	const secret = "JBSWY3DPEHPK3PXP"
	value, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("加密密钥失败：%v", err)
	}
	if !strings.HasPrefix(value, "v1.") {
		t.Fatalf("密文 = %q，期望 v1. 前缀", value)
	}

	got, err := cipher.Decrypt(value)
	if err != nil {
		t.Fatalf("解密密钥失败：%v", err)
	}
	if got != secret {
		t.Fatalf("解密结果 = %q，期望 %q", got, secret)
	}
}

func TestSecretCipherRejectsTampering(t *testing.T) {
	key, err := derivePurposeKey([]byte("0123456789abcdef0123456789abcdef"), "two-factor")
	if err != nil {
		t.Fatalf("派生用途密钥失败：%v", err)
	}
	cipher, err := newSecretCipher(key)
	if err != nil {
		t.Fatalf("创建密钥加密器失败：%v", err)
	}

	value, err := cipher.Encrypt("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("加密密钥失败：%v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "v1."))
	if err != nil {
		t.Fatalf("解码密文失败：%v", err)
	}
	payload[len(payload)-1] ^= 1
	tampered := "v1." + base64.RawURLEncoding.EncodeToString(payload)

	if _, err := cipher.Decrypt(tampered); err == nil {
		t.Fatal("被篡改的密文不应解密成功")
	}
}

func TestSecretCipherRejectsInvalidEnvelope(t *testing.T) {
	key, err := derivePurposeKey([]byte("0123456789abcdef0123456789abcdef"), "two-factor")
	if err != nil {
		t.Fatalf("派生用途密钥失败：%v", err)
	}
	cipher, err := newSecretCipher(key)
	if err != nil {
		t.Fatalf("创建密钥加密器失败：%v", err)
	}

	if _, err := cipher.Decrypt("v2.invalid"); err == nil {
		t.Fatal("非 v1 密文不应解密成功")
	}
	shortPayload := base64.RawURLEncoding.EncodeToString(make([]byte, cipher.aead.NonceSize()-1))
	if _, err := cipher.Decrypt("v1." + shortPayload); err == nil {
		t.Fatal("短于 nonce 的密文不应解密成功")
	}
}

func TestRecoveryCodesFormat(t *testing.T) {
	codes, err := generateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("生成恢复码失败：%v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("恢复码数量 = %d，期望 10", len(codes))
	}

	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	for _, code := range codes {
		if len(code) != 19 {
			t.Errorf("恢复码 %q 长度 = %d，期望 19", code, len(code))
		}
		if strings.Count(code, "-") != 3 {
			t.Errorf("恢复码 %q 的连字符数量不是 3", code)
		}
		raw, err := encoding.DecodeString(normalizeRecoveryCode(code))
		if err != nil {
			t.Errorf("恢复码 %q 不是无填充 Base32：%v", code, err)
			continue
		}
		if len(raw) != 10 {
			t.Errorf("恢复码 %q 解码后长度 = %d，期望 10", code, len(raw))
		}
	}
}

func TestRecoveryCodesHashNormalizationAndUserBinding(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	canonical := "ABCD-EFGH-JKLM-NOPQ"
	variant := "abcd efgh jklm nopq"

	want := recoveryCodeHash(key, "user-1", canonical)
	if got := recoveryCodeHash(key, "user-1", variant); got != want {
		t.Fatalf("格式变化后的恢复码哈希 = %q，期望 %q", got, want)
	}
	if got := recoveryCodeHash(key, "user-2", canonical); got == want {
		t.Fatal("不同用户的恢复码哈希不应相同")
	}

	raw, err := base64.RawURLEncoding.DecodeString(want)
	if err != nil {
		t.Fatalf("恢复码哈希不是 RawURL Base64：%v", err)
	}
	if len(raw) != sha256.Size {
		t.Fatalf("恢复码哈希解码后长度 = %d，期望 %d", len(raw), sha256.Size)
	}
}
