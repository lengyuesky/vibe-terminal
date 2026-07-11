package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
)

var errInvalidSecondFactor = errors.New("invalid second factor")
var errLoginRestartRequired = errors.New("login restart required")

func (r *router) handleLoginTwoFactor(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ChallengeToken string `json:"challenge_token"`
		Code           string `json:"code"`
	}
	if !readLoginJSON(w, req, &body) {
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
	if _, err := r.store.GetActiveLoginChallenge(req.Context(), challenge.JTI, challenge.UserID, challenge.ConfigurationID, r.now()); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	ip := requestIP(req)
	limitKeys := secondFactorLimitKeys(challenge.UserID, ip)
	if allowed, retryAfter := allowLoginAttempt(r.twoFactorLimiter, limitKeys); !allowed {
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
	if errors.Is(err, errLoginRestartRequired) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}
	if errors.Is(err, errInvalidSecondFactor) {
		if blocked, retryAfter := recordLoginFailure(r.twoFactorLimiter, limitKeys); blocked {
			r.auditLoginRateLimit(req.Context(), user.ID, "second_factor", ip)
			writeRateLimit(w, retryAfter)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid two factor code")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if err := r.store.ConsumeLoginChallenge(req.Context(), challenge.JTI, challenge.UserID, challenge.ConfigurationID, r.now()); errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}

	clearLoginFailures(r.twoFactorLimiter, limitKeys)
	r.completeLogin(w, req, user, method)
}

func secondFactorLimitKeys(userID, sourceIP string) []string {
	return []string{
		"user|" + userID,
		"source|" + sourceIP,
		"user_source|" + userID + "|" + sourceIP,
	}
}

func (r *router) verifyLoginCode(req *http.Request, setting store.UserTwoFactor, code string) (string, error) {
	now := r.now()
	trimmedCode := strings.TrimSpace(code)
	if len(trimmedCode) == 6 {
		secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
		if err != nil {
			return "", err
		}
		counter, matched, err := auth.MatchTOTP(secret, trimmedCode, now)
		if err != nil {
			return "", err
		}
		if !matched {
			return "", errInvalidSecondFactor
		}
		if err := r.store.ConsumeTOTPCounter(req.Context(), setting.UserID, setting.ConfigurationID, counter, now); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return "", r.classifySecondFactorConflict(req, setting)
			}
			return "", err
		}
		return "totp", nil
	}

	hash := r.twoFactor.RecoveryCodeHash(setting.UserID, code)
	if err := r.store.ConsumeRecoveryCode(req.Context(), setting.UserID, hash, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", r.classifySecondFactorConflict(req, setting)
		}
		return "", err
	}
	return "recovery_code", nil
}

func (r *router) classifySecondFactorConflict(req *http.Request, previous store.UserTwoFactor) error {
	current, err := r.store.GetEnabledTwoFactor(req.Context(), previous.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return errLoginRestartRequired
	}
	if err != nil {
		return err
	}
	if current.ConfigurationID != previous.ConfigurationID {
		return errLoginRestartRequired
	}
	return errInvalidSecondFactor
}
