package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const TOTPPeriodSeconds int64 = 30

func GenerateTOTPSecret() (string, error) {
	key := make([]byte, 20)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key), nil
}

func TOTPProvisioningURI(issuer, account, secret string) string {
	escapeLabelPart := func(value string) string {
		return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
	}
	label := escapeLabelPart(issuer) + ":" + escapeLabelPart(account)
	query := url.Values{
		"secret":    {secret},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {fmt.Sprintf("%d", TOTPPeriodSeconds)},
	}
	return "otpauth://totp/" + label + "?" + query.Encode()
}

func MatchTOTP(secret, code string, now time.Time) (int64, bool, error) {
	if len(code) != 6 {
		return 0, false, nil
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return 0, false, nil
		}
	}

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		strings.ToUpper(strings.TrimSpace(secret)),
	)
	if err != nil {
		return 0, false, err
	}

	currentCounter := now.Unix() / TOTPPeriodSeconds
	for _, counter := range []int64{currentCounter - 1, currentCounter, currentCounter + 1} {
		if counter < 0 {
			continue
		}
		if hmac.Equal([]byte(hotpCode(key, uint64(counter))), []byte(code)) {
			return counter, true, nil
		}
	}
	return 0, false, nil
}

func hotpCode(key []byte, counter uint64) string {
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
