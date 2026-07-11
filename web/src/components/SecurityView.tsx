import { FormEvent, useEffect, useRef, useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import {
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

export function SecurityView() {
  const [step, setStep] = useState<SecurityStep>('overview');
  const [status, setStatus] = useState<TwoFactorStatus | null>(null);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const mountedRef = useRef(false);
  const submittingRef = useRef(false);
  const requestGenerationRef = useRef(0);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const codeInputRef = useRef<HTMLInputElement>(null);
  const recoveryTitleRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    mountedRef.current = true;
    submittingRef.current = true;
    const generation = ++requestGenerationRef.current;
    setLoading(true);
    void getTwoFactorStatus()
      .then((result) => {
        if (isCurrentRequest(generation)) setStatus(result);
      })
      .catch((caught) => {
        if (isCurrentRequest(generation)) {
          setError(caught instanceof Error ? caught.message : 'Failed to load two-factor status.');
        }
      })
      .finally(() => finishRequest(generation));
    return () => {
      mountedRef.current = false;
      submittingRef.current = false;
      requestGenerationRef.current += 1;
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

  function clearSensitiveState() {
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
    const generation = beginRequest();
    if (generation === null) return;
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
        setError(caught instanceof Error ? caught.message : 'Failed to enable two-factor authentication.');
      }
    } finally {
      finishRequest(generation);
    }
  }

  async function submitRegenerate(event: FormEvent) {
    event.preventDefault();
    const generation = beginRequest();
    if (generation === null) return;
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
        setError(caught instanceof Error ? caught.message : 'Failed to regenerate recovery codes.');
      }
    } finally {
      finishRequest(generation);
    }
  }

  async function submitDisable(event: FormEvent) {
    event.preventDefault();
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
    setError('');
    setStep('overview');
  }

  async function copyRecoveryCodes() {
    setError('');
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard is unavailable.');
      await navigator.clipboard.writeText(recoveryCodes.join('\n'));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Failed to copy recovery codes.');
    }
  }

  function downloadRecoveryCodes() {
    setError('');
    let objectURL = '';
    try {
      const blob = new Blob([`${recoveryCodes.join('\n')}\n`], { type: 'text/plain;charset=utf-8' });
      objectURL = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = objectURL;
      link.download = 'vibe-terminal-recovery-codes.txt';
      link.click();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Failed to download recovery codes.');
    } finally {
      if (objectURL) URL.revokeObjectURL(objectURL);
    }
  }

  return (
    <section aria-labelledby="security-title">
      <h1 id="security-title" ref={titleRef} tabIndex={-1}>
        Two-factor security
      </h1>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
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
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading}>Continue</button>
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
              autoComplete="one-time-code"
              disabled={loading}
              inputMode="numeric"
              ref={codeInputRef}
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading}>Enable two-factor authentication</button>
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
          <ul>
            {recoveryCodes.map((recoveryCode, index) => (
              <li key={`${index}:${recoveryCode}`}>{recoveryCode}</li>
            ))}
          </ul>
          <button type="button" onClick={() => void copyRecoveryCodes()}>
            Copy recovery codes
          </button>
          <button type="button" onClick={downloadRecoveryCodes}>
            Download recovery codes
          </button>
          <button type="button" onClick={finishRecoveryCodes}>
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
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <label>
            Authenticator code
            <input
              autoComplete="one-time-code"
              disabled={loading}
              inputMode="numeric"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading}>Regenerate recovery codes</button>
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
              autoComplete="current-password"
              disabled={loading}
              ref={passwordInputRef}
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button type="submit" disabled={loading}>Confirm disable two-factor authentication</button>
          <button type="button" disabled={loading} onClick={cancelToOverview}>
            Cancel
          </button>
        </form>
      )}
    </section>
  );
}
