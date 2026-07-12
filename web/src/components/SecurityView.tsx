import { FormEvent, useEffect, useRef, useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import {
  APIError,
  disableTwoFactor,
  enableTwoFactor,
  getTwoFactorStatus,
  regenerateRecoveryCodes,
  startTwoFactorSetup,
  type TwoFactorSetup,
  type TwoFactorStatus,
} from '../api';
import { useT } from '../i18n';

type SecurityStep =
  | 'overview'
  | 'enable_password'
  | 'setup_confirm'
  | 'recovery_codes'
  | 'regenerate'
  | 'disable';

export function SecurityView({ onRecoveryDeliveryLockChange = () => {} }: { onRecoveryDeliveryLockChange?: (locked: boolean) => void }) {
  const { t } = useT();
  const [step, setStep] = useState<SecurityStep>('overview');
  const [status, setStatus] = useState<TwoFactorStatus | null>(null);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [copying, setCopying] = useState(false);
  const mountedRef = useRef(false);
  const submittingRef = useRef(false);
  const requestGenerationRef = useRef(0);
  const copyingRef = useRef(false);
  const copyGenerationRef = useRef(0);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const codeInputRef = useRef<HTMLInputElement>(null);
  const recoveryTitleRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    mountedRef.current = true;
    submittingRef.current = false;
    void loadStatus();
    return () => {
      onRecoveryDeliveryLockChange(false);
      mountedRef.current = false;
      submittingRef.current = false;
      requestGenerationRef.current += 1;
      copyingRef.current = false;
      copyGenerationRef.current += 1;
    };
  }, []);

  useEffect(() => {
    if (loading) return;
    if (step === 'overview' && status) titleRef.current?.focus();
    else if (step === 'enable_password' || step === 'regenerate' || step === 'disable') {
      passwordInputRef.current?.focus();
    } else if (step === 'setup_confirm') codeInputRef.current?.focus();
    else if (step === 'recovery_codes') recoveryTitleRef.current?.focus();
  }, [loading, status, step]);

  function isCurrentRequest(generation: number) {
    return mountedRef.current && generation === requestGenerationRef.current;
  }

  function beginRequest(): number | null {
    if (submittingRef.current) return null;
    submittingRef.current = true;
    setLoading(true);
    setError('');
    return ++requestGenerationRef.current;
  }

  function finishRequest(generation: number) {
    if (!isCurrentRequest(generation)) return;
    submittingRef.current = false;
    setLoading(false);
  }

  async function loadStatus() {
    const generation = beginRequest();
    if (generation === null) return;
    try {
      const result = await getTwoFactorStatus();
      if (isCurrentRequest(generation)) setStatus(result);
    } catch (caught) {
      if (isCurrentRequest(generation)) {
        setError(caught instanceof Error ? caught.message : t('security.errStatus'));
      }
    } finally {
      finishRequest(generation);
    }
  }

  function isCurrentCopy(generation: number) {
    return mountedRef.current && generation === copyGenerationRef.current;
  }

  function invalidateCopy() {
    copyGenerationRef.current += 1;
    copyingRef.current = false;
    if (mountedRef.current) setCopying(false);
  }

  function clearSensitiveState() {
    invalidateCopy();
    setPassword('');
    setCode('');
    setSetup(null);
    setRecoveryCodes([]);
  }

  function openStep(nextStep: SecurityStep) {
    if (submittingRef.current) return;
    clearSensitiveState();
    setError('');
    setStep(nextStep);
  }

  function cancelToOverview() {
    if (submittingRef.current) return;
    clearSensitiveState();
    setError('');
    setStep('overview');
  }

  async function submitSetup(event: FormEvent) {
    event.preventDefault();
    if (!password.trim()) return;
    const generation = beginRequest();
    if (generation === null) return;
    try {
      const result = await startTwoFactorSetup(password);
      if (!isCurrentRequest(generation)) return;
      setPassword('');
      setSetup(result);
      setStep('setup_confirm');
    } catch (caught) {
      if (isCurrentRequest(generation)) {
        setError(caught instanceof Error ? caught.message : t('security.errSetup'));
      }
    } finally {
      finishRequest(generation);
    }
  }

  async function submitEnable(event: FormEvent) {
    event.preventDefault();
    if (!code.trim()) return;
    const generation = beginRequest();
    if (generation === null) return;
    onRecoveryDeliveryLockChange(true);
    try {
      const codes = await enableTwoFactor(code);
      if (!isCurrentRequest(generation)) return;
      setStatus({ enabled: true, recoveryCodesRemaining: codes.length });
      setPassword('');
      setCode('');
      setSetup(null);
      setRecoveryCodes(codes);
      setStep('recovery_codes');
    } catch (caught) {
      if (isCurrentRequest(generation)) {
        onRecoveryDeliveryLockChange(false);
        if (caught instanceof APIError && caught.code === 'two_factor_setup_expired') {
          clearSensitiveState();
          setStep('enable_password');
        }
        setError(caught instanceof Error ? caught.message : t('security.errEnable'));
      }
    } finally {
      finishRequest(generation);
    }
  }

  async function submitRegenerate(event: FormEvent) {
    event.preventDefault();
    if (!password.trim() || !code.trim()) return;
    const generation = beginRequest();
    if (generation === null) return;
    onRecoveryDeliveryLockChange(true);
    try {
      const codes = await regenerateRecoveryCodes(password, code);
      if (!isCurrentRequest(generation)) return;
      setStatus({ enabled: true, recoveryCodesRemaining: codes.length });
      setPassword('');
      setCode('');
      setRecoveryCodes(codes);
      setStep('recovery_codes');
    } catch (caught) {
      if (isCurrentRequest(generation)) {
        onRecoveryDeliveryLockChange(false);
        setError(caught instanceof Error ? caught.message : t('security.errRegenerate'));
      }
    } finally {
      finishRequest(generation);
    }
  }

  async function submitDisable(event: FormEvent) {
    event.preventDefault();
    if (!password.trim()) return;
    const generation = beginRequest();
    if (generation === null) return;
    try {
      await disableTwoFactor(password);
      if (!isCurrentRequest(generation)) return;
      clearSensitiveState();
      setStatus({ enabled: false, recoveryCodesRemaining: 0 });
      setStep('overview');
    } catch (caught) {
      if (isCurrentRequest(generation)) {
        setError(caught instanceof Error ? caught.message : t('security.errDisable'));
      }
    } finally {
      finishRequest(generation);
    }
  }

  function finishRecoveryCodes() {
    if (submittingRef.current) return;
    clearSensitiveState();
    onRecoveryDeliveryLockChange(false);
    setError('');
    setStep('overview');
  }

  async function copyRecoveryCodes() {
    if (copyingRef.current) return;
    copyingRef.current = true;
    const generation = ++copyGenerationRef.current;
    setCopying(true);
    setError('');
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard is unavailable.');
      await navigator.clipboard.writeText(recoveryCodes.join('\n'));
    } catch (caught) {
      if (isCurrentCopy(generation)) {
        setError(t('security.errCopy'));
      }
    } finally {
      if (isCurrentCopy(generation)) {
        copyingRef.current = false;
        setCopying(false);
      }
    }
  }

  function downloadRecoveryCodes() {
    setError('');
    let objectURL = '';
    let link: HTMLAnchorElement | null = null;
    try {
      const blob = new Blob([`${recoveryCodes.join('\n')}\n`], { type: 'text/plain;charset=utf-8' });
      objectURL = URL.createObjectURL(blob);
      link = document.createElement('a');
      link.href = objectURL;
      link.download = 'vibe-terminal-recovery-codes.txt';
      document.body.append(link);
      link.click();
    } catch {
      setError(t('security.errDownload'));
    } finally {
      link?.remove();
      if (objectURL) {
        const urlToRevoke = objectURL;
        setTimeout(() => URL.revokeObjectURL(urlToRevoke), 0);
      }
    }
  }

  return (
    <section aria-labelledby="security-title">
      <h1 id="security-title" ref={titleRef} tabIndex={-1}>
        {t('security.title')}
      </h1>
      {error && (
        <p id="security-error" role="alert" className="error">
          {error}
        </p>
      )}
      {step === 'overview' && !status && error && (
        <button type="button" disabled={loading} onClick={() => void loadStatus()}>
          {t('security.retryStatus')}
        </button>
      )}

      {step === 'overview' && status && (
        <>
          <p>
            {status.enabled ? t('security.statusEnabled') : t('security.statusDisabled')}
          </p>
          {status.enabled ? (
            <>
              <p>{t('security.recoveryRemaining', { count: status.recoveryCodesRemaining })}</p>
              <button type="button" disabled={loading} onClick={() => openStep('regenerate')}>
                {t('security.regenerate')}
              </button>
              <button type="button" disabled={loading} onClick={() => openStep('disable')}>
                {t('security.disable')}
              </button>
            </>
          ) : (
            <button type="button" disabled={loading} onClick={() => openStep('enable_password')}>
              {t('security.enable')}
            </button>
          )}
        </>
      )}

      {step === 'enable_password' && (
        <form onSubmit={submitSetup}>
          <label>
            {t('security.currentPassword')}
            <input
              aria-describedby={error ? 'security-error' : undefined}
              aria-invalid={error ? true : undefined}
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              required
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading || !password.trim()}>{t('common.continue')}</button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            {t('common.cancel')}
          </button>
        </form>
      )}

      {step === 'setup_confirm' && setup && (
        <form onSubmit={submitEnable}>
          <h2>{t('security.scanQr')}</h2>
          <QRCodeSVG aria-label={t('security.qrLabel')} role="img" value={setup.otpauthURI} />
          <p>{setup.manualKey}</p>
          <p>{t('security.setupExpires', { time: setup.expiresAt })}</p>
          <label>
            {t('security.authenticatorCode')}
            <input
              aria-describedby={error ? 'security-error' : undefined}
              aria-invalid={error ? true : undefined}
              autoComplete="one-time-code"
              disabled={loading}
              inputMode="numeric"
              ref={codeInputRef}
              required
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading || !code.trim()}>{t('security.enable')}</button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            {t('common.cancel')}
          </button>
        </form>
      )}

      {step === 'recovery_codes' && (
        <div>
          <h2 ref={recoveryTitleRef} tabIndex={-1}>
            {t('security.saveRecovery')}
          </h2>
          <p role="note">{t('security.recoveryNote')}</p>
          <ul>
            {recoveryCodes.map((recoveryCode, index) => (
              <li key={`${index}:${recoveryCode}`}>{recoveryCode}</li>
            ))}
          </ul>
          <button type="button" disabled={copying} onClick={() => void copyRecoveryCodes()}>
            {t('security.copyRecovery')}
          </button>
          <button type="button" disabled={copying} onClick={downloadRecoveryCodes}>
            {t('security.downloadRecovery')}
          </button>
          <button type="button" disabled={copying} onClick={finishRecoveryCodes}>
            {t('common.done')}
          </button>
        </div>
      )}

      {step === 'regenerate' && (
        <form onSubmit={submitRegenerate}>
          <h2>{t('security.regenerate')}</h2>
          <label>
            {t('security.currentPassword')}
            <input
              aria-describedby={error ? 'security-error' : undefined}
              aria-invalid={error ? true : undefined}
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              required
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <label>
            {t('security.authenticatorCode')}
            <input
              aria-describedby={error ? 'security-error' : undefined}
              aria-invalid={error ? true : undefined}
              autoComplete="one-time-code"
              disabled={loading}
              inputMode="numeric"
              required
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading || !password.trim() || !code.trim()}>
            {t('security.regenerate')}
          </button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            {t('common.cancel')}
          </button>
        </form>
      )}

      {step === 'disable' && (
        <form onSubmit={submitDisable}>
          <h2>{t('security.disable')}</h2>
          <p>{t('security.disableWarning')}</p>
          <label>
            {t('security.currentPassword')}
            <input
              aria-describedby={error ? 'security-error' : undefined}
              aria-invalid={error ? true : undefined}
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              required
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button className="dangerButton" type="submit" disabled={loading || !password.trim()}>
            {t('security.confirmDisable')}
          </button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            {t('common.cancel')}
          </button>
        </form>
      )}
    </section>
  );
}
