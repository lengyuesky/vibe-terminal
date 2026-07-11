import { Terminal } from 'lucide-react';
import { FormEvent, useEffect, useRef, useState } from 'react';
import { APIError, type LoginResult } from '../api';

type LoginViewProps = {
  onLogin: (username: string, password: string) => Promise<LoginResult>;
  onVerifyTwoFactor: (challengeToken: string, code: string) => Promise<void>;
};

export function LoginView({ onLogin, onVerifyTwoFactor }: LoginViewProps) {
  const [step, setStep] = useState<'password' | 'second_factor'>('password');
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [challengeToken, setChallengeToken] = useState('');
  const [code, setCode] = useState('');
  const [recoveryMode, setRecoveryMode] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const submittingRef = useRef(false);
  const mountedRef = useRef(true);
  const requestGenerationRef = useRef(0);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const codeInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestGenerationRef.current += 1;
    };
  }, []);

  useEffect(() => {
    if (step === 'second_factor') codeInputRef.current?.focus();
    else passwordInputRef.current?.focus();
  }, [step]);

  function clearSecondFactorState() {
    setStep('password');
    setPassword('');
    setChallengeToken('');
    setCode('');
    setRecoveryMode(false);
  }

  function errorMessage(value: unknown, fallback: string) {
    return value instanceof Error ? value.message : fallback;
  }

  async function submitPassword(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current) return;
    const requestGeneration = ++requestGenerationRef.current;
    submittingRef.current = true;
    setSubmitting(true);
    setError('');
    try {
      const result = await onLogin(username, password);
      if (!mountedRef.current || requestGeneration !== requestGenerationRef.current) return;
      if (result.status === 'two_factor_required') {
        setPassword('');
        setChallengeToken(result.challengeToken);
        setCode('');
        setRecoveryMode(false);
        setStep('second_factor');
      }
    } catch (caught) {
      if (!mountedRef.current || requestGeneration !== requestGenerationRef.current) return;
      setError(errorMessage(caught, 'login failed'));
    } finally {
      submittingRef.current = false;
      if (mountedRef.current && requestGeneration === requestGenerationRef.current) setSubmitting(false);
    }
  }

  async function submitSecondFactor(event: FormEvent) {
    event.preventDefault();
    if (submittingRef.current || !challengeToken || !code.trim()) return;
    const requestGeneration = ++requestGenerationRef.current;
    submittingRef.current = true;
    setSubmitting(true);
    setError('');
    try {
      await onVerifyTwoFactor(challengeToken, code.trim());
    } catch (caught) {
      if (!mountedRef.current || requestGeneration !== requestGenerationRef.current) return;
      if (caught instanceof APIError && caught.code === 'login_restart_required') {
        clearSecondFactorState();
      }
      setError(errorMessage(caught, 'two-factor verification failed'));
    } finally {
      submittingRef.current = false;
      if (mountedRef.current && requestGeneration === requestGenerationRef.current) setSubmitting(false);
    }
  }

  function backToLogin() {
    if (submittingRef.current) return;
    requestGenerationRef.current += 1;
    clearSecondFactorState();
    setError('');
  }

  function toggleRecoveryMode() {
    if (submittingRef.current) return;
    setRecoveryMode((current) => !current);
    setCode('');
    setError('');
  }

  return (
    <main className="login">
      <form onSubmit={step === 'password' ? submitPassword : submitSecondFactor} className="loginForm">
        <h1 className="loginBrand">
          <Terminal size={22} aria-hidden="true" />
          vibe-terminal
        </h1>
        {step === 'password' ? (
          <>
            <label>
              Username
              <input
                autoComplete="username"
                disabled={submitting}
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </label>
            <label>
              Password
              <input
                ref={passwordInputRef}
                autoComplete="current-password"
                aria-describedby={error ? 'login-error' : undefined}
                aria-invalid={error ? true : undefined}
                disabled={submitting}
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
          </>
        ) : (
          <>
            <h2>Two-factor authentication</h2>
            <p className="loginHint">
              {recoveryMode
                ? 'Enter one of your saved recovery codes.'
                : 'Enter the code from your authenticator app.'}
            </p>
            <label>
              {recoveryMode ? 'Recovery code' : 'Authenticator code'}
              <input
                ref={codeInputRef}
                autoComplete="one-time-code"
                aria-describedby={error ? 'login-error' : undefined}
                aria-invalid={error ? true : undefined}
                disabled={submitting}
                inputMode={recoveryMode ? undefined : 'numeric'}
                value={code}
                onChange={(event) => setCode(event.target.value)}
              />
            </label>
            <button type="button" disabled={submitting} onClick={toggleRecoveryMode}>
              {recoveryMode ? 'Use an authenticator code' : 'Use a recovery code'}
            </button>
            <button className="loginBackButton" type="button" disabled={submitting} onClick={backToLogin}>
              Back to login
            </button>
          </>
        )}
        {error && (
          <p className="error" id="login-error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" disabled={submitting || (step === 'second_factor' && !code.trim())}>
          {step === 'password' ? 'Login' : 'Verify'}
        </button>
      </form>
    </main>
  );
}
