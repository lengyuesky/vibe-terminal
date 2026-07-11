package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
)

var errInvalidSecondFactor = errors.New("invalid second factor")

type verifiedLoginCode struct {
	method       string
	totpCounter  sql.NullInt64
	recoveryHash string
}

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
	now := r.now()

	challenge, err := r.twoFactor.VerifyLoginChallengeAt(body.ChallengeToken, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
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

	verifiedCode, err := r.verifyLoginCode(setting, body.Code, now)
	if errors.Is(err, errInvalidSecondFactor) {
		r.writeInvalidSecondFactor(w, req, user.ID, ip, limitKeys)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	err = r.store.ConsumeLoginSecondFactor(req.Context(), store.ConsumeLoginSecondFactorParams{
		ChallengeJTI:    challenge.JTI,
		UserID:          challenge.UserID,
		ConfigurationID: challenge.ConfigurationID,
		TOTPCounter:     verifiedCode.totpCounter,
		RecoveryHash:    verifiedCode.recoveryHash,
		Now:             now,
	})
	if errors.Is(err, store.ErrLoginRestartRequired) {
		writeError(w, http.StatusUnauthorized, "login_restart_required", "restart login to continue")
		return
	}
	if errors.Is(err, store.ErrInvalidSecondFactor) {
		r.writeInvalidSecondFactor(w, req, user.ID, ip, limitKeys)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}

	clearLoginFailures(r.twoFactorLimiter, limitKeys)
	r.completeLogin(w, req, user, verifiedCode.method)
}

func (r *router) writeInvalidSecondFactor(w http.ResponseWriter, req *http.Request, userID, sourceIP string, limitKeys []string) {
	if blocked, retryAfter := recordLoginFailure(r.twoFactorLimiter, limitKeys); blocked {
		r.auditLoginRateLimit(req.Context(), userID, "second_factor", sourceIP)
		writeRateLimit(w, retryAfter)
		return
	}
	writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid two factor code")
}

func secondFactorLimitKeys(userID, sourceIP string) []string {
	return []string{
		"user|" + userID,
		"source|" + sourceIP,
		"user_source|" + userID + "|" + sourceIP,
	}
}

func (r *router) verifyLoginCode(setting store.UserTwoFactor, code string, now time.Time) (verifiedLoginCode, error) {
	trimmedCode := strings.TrimSpace(code)
	if len(trimmedCode) == 6 {
		secret, err := r.twoFactor.DecryptSecret(setting.SecretCiphertext)
		if err != nil {
			return verifiedLoginCode{}, err
		}
		counter, matched, err := auth.MatchTOTP(secret, trimmedCode, now)
		if err != nil {
			return verifiedLoginCode{}, err
		}
		if !matched {
			return verifiedLoginCode{}, errInvalidSecondFactor
		}
		return verifiedLoginCode{
			method:      "totp",
			totpCounter: sql.NullInt64{Int64: counter, Valid: true},
		}, nil
	}

	return verifiedLoginCode{
		method:       "recovery_code",
		recoveryHash: r.twoFactor.RecoveryCodeHash(setting.UserID, code),
	}, nil
}
