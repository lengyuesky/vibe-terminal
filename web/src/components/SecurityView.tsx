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

type SecurityStep =
  | 'overview'
  | 'enable_password'
  | 'setup_confirm'
  | 'recovery_codes'
  | 'regenerate'
  | 'disable';

export function SecurityView({ onRecoveryDeliveryLockChange = () => {} }: { onRecoveryDeliveryLockChange?: (locked: boolean) => void }) {
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
        setError(caught instanceof Error ? caught.message : 'Failed to load two-factor status.');
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
        setError(caught instanceof Error ? caught.message : 'Failed to start two-factor setup.');
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
        setError(caught instanceof Error ? caught.message : 'Failed to enable two-factor authentication.');
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
        setError(caught instanceof Error ? caught.message : 'Failed to regenerate recovery codes.');
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
        setError(caught instanceof Error ? caught.message : 'Failed to disable two-factor authentication.');
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
        setError('Failed to copy recovery codes.');
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
      setError('Failed to download recovery codes.');
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
        Two-factor security
      </h1>
      {error && (
        <p id="security-error" role="alert" className="error">
          {error}
        </p>
      )}
      {step === 'overview' && !status && error && (
        <button type="button" disabled={loading} onClick={() => void loadStatus()}>
          Retry loading status
        </button>
      )}

      {step === 'overview' && status && (
        <>
          <p>
            Two-factor authentication is {status.enabled ? 'enabled' : 'disabled'}.
          </p>
          {status.enabled ? (
            <>
              <p>{status.recoveryCodesRemaining} recovery codes remaining.</p>
              <button type="button" disabled={loading} onClick={() => openStep('regenerate')}>
                Regenerate recovery codes
              </button>
              <button type="button" disabled={loading} onClick={() => openStep('disable')}>
                Disable two-factor authentication
              </button>
            </>
          ) : (
            <button type="button" disabled={loading} onClick={() => openStep('enable_password')}>
              Enable two-factor authentication
            </button>
          )}
        </>
      )}

      {step === 'enable_password' && (
        <form onSubmit={submitSetup}>
          <label>
            Current password
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
          <button type="submit" disabled={loading || !password.trim()}>Continue</button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            Cancel
          </button>
        </form>
      )}

      {step === 'setup_confirm' && setup && (
        <form onSubmit={submitEnable}>
          <h2>Scan the QR code</h2>
          <QRCodeSVG aria-label="Two-factor setup QR code" role="img" value={setup.otpauthURI} />
          <p>{setup.manualKey}</p>
          <p>Setup expires {setup.expiresAt}</p>
          <label>
            Authenticator code
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
          <button type="submit" disabled={loading || !code.trim()}>Enable two-factor authentication</button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            Cancel
          </button>
        </form>
      )}

      {step === 'recovery_codes' && (
        <div>
          <h2 ref={recoveryTitleRef} tabIndex={-1}>
            Save your recovery codes
          </h2>
          <p role="note">These recovery codes are shown only once. Store them safely before continuing.</p>
          <ul>
            {recoveryCodes.map((recoveryCode, index) => (
              <li key={`${index}:${recoveryCode}`}>{recoveryCode}</li>
            ))}
          </ul>
          <button type="button" disabled={copying} onClick={() => void copyRecoveryCodes()}>
            Copy recovery codes
          </button>
          <button type="button" disabled={copying} onClick={downloadRecoveryCodes}>
            Download recovery codes
          </button>
          <button type="button" disabled={copying} onClick={finishRecoveryCodes}>
            Done
          </button>
        </div>
      )}

      {step === 'regenerate' && (
        <form onSubmit={submitRegenerate}>
          <h2>Regenerate recovery codes</h2>
          <label>
            Current password
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
            Authenticator code
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
            Regenerate recovery codes
          </button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            Cancel
          </button>
        </form>
      )}

      {step === 'disable' && (
        <form onSubmit={submitDisable}>
          <h2>Disable two-factor authentication</h2>
          <p>This removes all recovery codes.</p>
          <label>
            Current password
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
            Confirm disable two-factor authentication
          </button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            Cancel
          </button>
        </form>
      )}
    </section>
  );
}
