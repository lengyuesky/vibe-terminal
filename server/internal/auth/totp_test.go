package auth

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("生成 TOTP 密钥失败：%v", err)
	}
	if strings.Contains(secret, "=") {
		t.Fatalf("密钥包含 Base32 填充：%q", secret)
	}

	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("解码 TOTP 密钥失败：%v", err)
	}
	if len(decoded) != 20 {
		t.Fatalf("解码后的密钥长度 = %d，期望 20", len(decoded))
	}
}

func TestMatchTOTP(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(75, 0)

	tests := []struct {
		name        string
		code        string
		wantCounter int64
	}{
		{name: "前一个窗口", code: "287082", wantCounter: 1},
		{name: "当前窗口", code: "359152", wantCounter: 2},
		{name: "后一个窗口", code: "969429", wantCounter: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter, matched, err := MatchTOTP("  "+strings.ToLower(secret)+"  ", tt.code, now)
			if err != nil {
				t.Fatalf("匹配 TOTP 失败：%v", err)
			}
			if !matched {
				t.Fatal("TOTP 未匹配")
			}
			if counter != tt.wantCounter {
				t.Fatalf("匹配计数器 = %d，期望 %d", counter, tt.wantCounter)
			}
		})
	}
}

func TestMatchTOTPRejectsInvalidCodeFormat(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	for _, code := range []string{"12345", "12a456"} {
		t.Run(code, func(t *testing.T) {
			_, matched, err := MatchTOTP(secret, code, time.Unix(75, 0))
			if err != nil {
				t.Fatalf("格式无效的验证码返回错误：%v", err)
			}
			if matched {
				t.Fatalf("格式无效的验证码 %q 不应匹配", code)
			}
		})
	}
}

func TestTOTPProvisioningURI(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	uri := TOTPProvisioningURI("Acme Inc", "user+ops@example.com", secret)

	const wantPrefix = "otpauth://totp/Acme%20Inc:user%2Bops%40example.com?"
	if !strings.HasPrefix(uri, wantPrefix) {
		t.Fatalf("配置 URI = %q，期望前缀 %q", uri, wantPrefix)
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("解析配置 URI 失败：%v", err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"secret":    secret,
		"issuer":    "Acme Inc",
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("查询参数 %s = %q，期望 %q", key, got, want)
		}
	}
}

func TestHOTPCodeRFC4226(t *testing.T) {
	key := []byte("12345678901234567890")
	if got := hotpCode(key, 1); got != "287082" {
		t.Fatalf("HOTP 验证码 = %q，期望 %q", got, "287082")
	}
}
