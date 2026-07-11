package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
)

var errInvalidSecondFactor = errors.New("invalid second factor")

type verifiedLoginCode struct {
	method       string
	totpCounter  sql.NullInt64
	recoveryHash string
}

func (r *router) handleTwoFactorStatus(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	enabled, remaining, err := r.store.GetTwoFactorStatus(req.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":                  enabled,
		"recovery_codes_remaining": remaining,
	})
}

func (r *router) handleTwoFactorSetup(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readLoginJSON(w, req, &body) {
		return
	}
	ip, limitKeys := r.managementAttempt(w, req, user.ID)
	if limitKeys == nil {
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		r.writeInvalidManagementCredential(w, req, user.ID, "setup_password", ip, limitKeys, "invalid_credentials", "invalid current password")
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	current, err := r.store.GetUserTwoFactor(req.Context(), user.ID)
	previousConfigurationID := ""
	if err == nil && current.EnabledAt.Valid {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
		return
	} else if err == nil {
		previousConfigurationID = current.ConfigurationID
	} else if !errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}

	manualKey, otpauthURI, ciphertext, err := r.twoFactor.GenerateSetup("vibe-terminal", user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	expiresAt := now.Add(10 * time.Minute)
	if r.beforePendingTwoFactorSave != nil {
		r.beforePendingTwoFactorSave()
	}
	err = r.store.SavePendingTwoFactorIfUnchanged(req.Context(), store.UserTwoFactor{
		UserID:           user.ID,
		ConfigurationID:  uuid.NewString(),
		SecretCiphertext: ciphertext,
		SetupExpiresAt:   sql.NullTime{Time: expiresAt, Valid: true},
		CreatedAt:        now,
		UpdatedAt:        now,
	}, previousConfigurationID)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	clearLoginFailures(r.managementLimiter, limitKeys)
	writeJSON(w, http.StatusOK, map[string]any{
		"manual_key":  manualKey,
		"otpauth_uri": otpauthURI,
		"expires_at":  expiresAt,
	})
}

func (r *router) handleTwoFactorEnable(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !readLoginJSON(w, req, &body) {
		return
	}
	ip, limitKeys := r.managementAttempt(w, req, user.ID)
	if limitKeys == nil {
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	pending, err := r.store.GetPendingTwoFactor(req.Context(), user.ID, now)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_setup_expired", "two factor setup has expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	secret, err := r.twoFactor.DecryptSecret(pending.SecretCiphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	counter, matched, err := auth.MatchTOTP(secret, strings.TrimSpace(body.Code), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if !matched {
		r.writeInvalidManagementCredential(w, req, user.ID, "enable_totp", ip, limitKeys, "invalid_two_factor_code", "invalid two factor code")
		return
	}
	rawCodes, hashes, err := r.twoFactor.GenerateRecoveryCodes(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	codes := make([]store.RecoveryCodeInput, len(hashes))
	for i, hash := range hashes {
		codes[i] = store.RecoveryCodeInput{ID: uuid.NewString(), Hash: hash}
	}
	err = r.store.EnableTwoFactor(req.Context(), user.ID, pending.ConfigurationID, counter, codes, now)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	r.auditCommittedTwoFactorChange(req, user.ID, "two_factor_enabled", "two factor authentication enabled")
	clearLoginFailures(r.managementLimiter, limitKeys)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": rawCodes})
}

func (r *router) handleTwoFactorRecoveryCodes(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readLoginJSON(w, req, &body) {
		return
	}
	ip, limitKeys := r.managementAttempt(w, req, user.ID)
	if limitKeys == nil {
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		r.writeInvalidManagementCredential(w, req, user.ID, "recovery_password", ip, limitKeys, "invalid_credentials", "invalid current password")
		return
	}
	if r.twoFactor == nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	now := r.now().UTC()
	setting, err := r.store.GetEnabledTwoFactor(req.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
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
	counter, matched, err := auth.MatchTOTP(secret, strings.TrimSpace(body.Code), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	if !matched {
		r.writeInvalidManagementCredential(w, req, user.ID, "recovery_totp", ip, limitKeys, "invalid_two_factor_code", "invalid two factor code")
		return
	}
	rawCodes, hashes, err := r.twoFactor.GenerateRecoveryCodes(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	codes := make([]store.RecoveryCodeInput, len(hashes))
	for i, hash := range hashes {
		codes[i] = store.RecoveryCodeInput{ID: uuid.NewString(), Hash: hash}
	}
	err = r.store.ReplaceRecoveryCodesAfterTOTP(req.Context(), user.ID, setting.ConfigurationID, counter, codes, now)
	if errors.Is(err, store.ErrInvalidSecondFactor) {
		r.writeInvalidManagementCredential(w, req, user.ID, "recovery_totp", ip, limitKeys, "invalid_two_factor_code", "invalid two factor code")
		return
	}
	if writeRecoveryCodeRotationStoreError(w, err) {
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	r.auditCommittedTwoFactorChange(req, user.ID, "two_factor_recovery_codes_regenerated", "two factor recovery codes regenerated")
	clearLoginFailures(r.managementLimiter, limitKeys)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": rawCodes})
}

func writeRecoveryCodeRotationStoreError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
		return true
	case errors.Is(err, store.ErrInvalidSecondFactor):
		writeError(w, http.StatusUnauthorized, "invalid_two_factor_code", "invalid two factor code")
		return true
	default:
		return false
	}
}

func (r *router) handleTwoFactorDisable(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readLoginJSON(w, req, &body) {
		return
	}
	ip, limitKeys := r.managementAttempt(w, req, user.ID)
	if limitKeys == nil {
		return
	}
	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		r.writeInvalidManagementCredential(w, req, user.ID, "disable_password", ip, limitKeys, "invalid_credentials", "invalid current password")
		return
	}
	err := r.store.DisableTwoFactor(req.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusConflict, "two_factor_state_conflict", "two factor authentication state changed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "two_factor_unavailable", "two factor authentication is unavailable")
		return
	}
	r.auditCommittedTwoFactorChange(req, user.ID, "two_factor_disabled", "two factor authentication disabled")
	clearLoginFailures(r.managementLimiter, limitKeys)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (r *router) managementAttempt(w http.ResponseWriter, req *http.Request, userID string) (string, []string) {
	ip := requestIP(req)
	keys := managementLimitKeys(userID, ip)
	if allowed, retryAfter := allowLoginAttempt(r.managementLimiter, keys); !allowed {
		writeRateLimit(w, retryAfter)
		return ip, nil
	}
	return ip, keys
}

func (r *router) writeInvalidManagementCredential(w http.ResponseWriter, req *http.Request, userID, stage, sourceIP string, limitKeys []string, code, message string) {
	if blocked, retryAfter := recordLoginFailure(r.managementLimiter, limitKeys); blocked {
		r.auditManagementRateLimit(req.Context(), userID, stage, sourceIP)
		writeRateLimit(w, retryAfter)
		return
	}
	writeError(w, http.StatusUnauthorized, code, message)
}

func managementLimitKeys(userID, sourceIP string) []string {
	return []string{"user|" + userID, "source|" + sourceIP, "user_source|" + userID + "|" + sourceIP}
}

// auditCommittedTwoFactorChange 在状态提交后尽力记录审计。
// 此时不能再向客户端返回失败，否则启用或轮换成功后会丢失仅返回一次的恢复码。
func (r *router) auditCommittedTwoFactorChange(req *http.Request, userID, eventType, summary string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 2*time.Second)
	defer cancel()
	if err := r.audit.Log(ctx, store.AuditEvent{
		UserID:    userID,
		EventType: eventType,
		Summary:   summary,
	}); err != nil {
		log.Printf("记录已提交的二因素审计失败：event_type=%s user_id=%s error=%v", eventType, userID, err)
	}
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
	auditEvent, err := loginAuditEvent(user.ID, verifiedCode.method, now.UTC())
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
		Audit:           auditEvent,
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
	r.finishLogin(w, user)
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
