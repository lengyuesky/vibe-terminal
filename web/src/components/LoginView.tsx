import { Terminal } from 'lucide-react';
import { FormEvent, useEffect, useRef, useState } from 'react';
import { APIError, type LoginResult } from '../api';
import { useLang, useT } from '../i18n';

type LoginViewProps = {
  onLogin: (username: string, password: string) => Promise<LoginResult>;
  onVerifyTwoFactor: (challengeToken: string, code: string) => Promise<void>;
};

// 登录页右上角语言切换:未登录用户也能切换界面语言
function LoginLanguageSwitch() {
  const { t } = useT();
  const { lang, setLang } = useLang();
  return (
    <div className="loginLangSwitch" role="group" aria-label={t('login.languageLabel')}>
      <button
        type="button"
        aria-pressed={lang === 'zh'}
        className={lang === 'zh' ? 'active' : ''}
        onClick={() => setLang('zh')}
      >
        中文
      </button>
      <button
        type="button"
        aria-pressed={lang === 'en'}
        className={lang === 'en' ? 'active' : ''}
        onClick={() => setLang('en')}
      >
        EN
      </button>
    </div>
  );
}

export function LoginView({ onLogin, onVerifyTwoFactor }: LoginViewProps) {
  const { t } = useT();
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
      setError(errorMessage(caught, t('login.failed')));
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
      setError(errorMessage(caught, t('login.twoFactorFailed')));
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
      <LoginLanguageSwitch />
      <form onSubmit={step === 'password' ? submitPassword : submitSecondFactor} className="loginForm">
        <h1 className="loginBrand">
          <Terminal size={22} aria-hidden="true" />
          vibe-terminal
        </h1>
        {step === 'password' ? (
          <>
            <label>
              {t('login.username')}
              <input
                autoComplete="username"
                disabled={submitting}
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </label>
            <label>
              {t('login.password')}
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
            <h2>{t('login.twoFactorTitle')}</h2>
            <p className="loginHint">
              {recoveryMode
                ? t('login.recoveryHint')
                : t('login.authenticatorHint')}
            </p>
            <label>
              {recoveryMode ? t('login.recoveryCode') : t('login.authenticatorCode')}
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
              {recoveryMode ? t('login.useAuthenticator') : t('login.useRecovery')}
            </button>
            <button className="loginBackButton" type="button" disabled={submitting} onClick={backToLogin}>
              {t('login.back')}
            </button>
          </>
        )}
        {error && (
          <p className="error" id="login-error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" disabled={submitting || (step === 'second_factor' && !code.trim())}>
          {step === 'password' ? t('login.submit') : t('login.verify')}
        </button>
      </form>
    </main>
  );
}
