package httpapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"

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
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}
	ip := requestIP(req)
	limitKey := challenge.UserID + "|" + ip
	if allowed, retryAfter := r.twoFactorLimiter.Allow(limitKey); !allowed {
		writeRateLimit(w, retryAfter)
		return
	}

	user, err := r.store.GetUserByID(req.Context(), challenge.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	setting, err := r.store.GetEnabledTwoFactor(req.Context(), challenge.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if setting.ConfigurationID != challenge.ConfigurationID {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}

	method, err := r.verifyLoginCode(req, setting, body.Code)
	if errors.Is(err, errInvalidSecondFactor) {
		if blocked, retryAfter := r.twoFactorLimiter.RecordFailure(limitKey); blocked {
			r.auditLoginRateLimit(req.Context(), user.ID, "two_factor", ip)
			writeRateLimit(w, retryAfter)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_second_factor", "invalid two factor code")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}

	r.twoFactorLimiter.Success(limitKey)
	r.completeLogin(w, req, user, method)
}

func readTwoFactorJSON(w http.ResponseWriter, req *http.Request, dest any) bool {
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return false
	}
	defer req.Body.Close()
	if err := json.NewDecoder(req.Body).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func (r *router) verifyLoginCode(req *http.Request, setting store.UserTwoFactor, code string) (string, error) {
	trimmedCode := strings.TrimSpace(code)
	if len(trimmedCode) == 6 {
		secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
		if err != nil {
			return "", err
		}
		counter, matched, err := auth.MatchTOTP(secret, trimmedCode, r.now())
		if err != nil {
			return "", err
		}
		if !matched {
			return "", errInvalidSecondFactor
		}
		if err := r.store.ConsumeTOTPCounter(req.Context(), setting.UserID, setting.ConfigurationID, counter, r.now()); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return "", errInvalidSecondFactor
			}
			return "", err
		}
		return "totp", nil
	}

	hash := r.twoFactor.RecoveryCodeHash(setting.UserID, code)
	if err := r.store.ConsumeRecoveryCode(req.Context(), setting.UserID, hash, r.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errInvalidSecondFactor
		}
		return "", err
	}
	return "recovery_code", nil
}
