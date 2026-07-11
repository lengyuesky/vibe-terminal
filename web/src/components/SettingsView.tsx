import { Component, lazy, Suspense, useMemo, useState } from 'react';
import type { ComponentType, ErrorInfo, ReactNode } from 'react';
import { useLang, useT } from '../i18n';

export type SecurityViewProps = { onRecoveryDeliveryLockChange?: (locked: boolean) => void };
export type SecurityLoader = () => Promise<{ default: ComponentType<SecurityViewProps> }>;

export const defaultSecurityLoader: SecurityLoader = () =>
  import('./SecurityView').then((module) => ({ default: module.SecurityView }));

// 懒加载失败的可重试错误提示(函数组件才能使用 useT)
function SecurityLoadError({ onRetry }: { onRetry: () => void }) {
  const { t } = useT();
  return (
    <section className="securityLoading" role="alert">
      <p>{t('security.loadFailed')}</p>
      <button type="button" onClick={onRetry}>
        {t('security.retryLoad')}
      </button>
    </section>
  );
}

function SecurityLoading() {
  const { t } = useT();
  return (
    <p className="securityLoading" role="status" aria-label={t('security.loadingLabel')}>
      {t('security.loadingText')}
    </p>
  );
}

class SecurityErrorBoundary extends Component<
  { children: ReactNode; onRetry: () => void },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(_error: Error, _info: ErrorInfo) {}
  render() {
    if (this.state.failed) {
      return <SecurityLoadError onRetry={this.props.onRetry} />;
    }
    return this.props.children;
  }
}

export function SecurityPanel({
  loader,
  onRecoveryDeliveryLockChange,
}: {
  loader: SecurityLoader;
  onRecoveryDeliveryLockChange: (locked: boolean) => void;
}) {
  const [attempt, setAttempt] = useState(0);
  const LazySecurityView = useMemo(() => lazy(loader), [loader, attempt]);
  return (
    <SecurityErrorBoundary key={attempt} onRetry={() => setAttempt((value) => value + 1)}>
      <Suspense fallback={<SecurityLoading />}>
        <LazySecurityView onRecoveryDeliveryLockChange={onRecoveryDeliveryLockChange} />
      </Suspense>
    </SecurityErrorBoundary>
  );
}

type SettingsSection = 'general' | 'security';

export function SettingsView({
  securityLoader = defaultSecurityLoader,
  onRecoveryDeliveryLockChange,
  deliveryLocked,
}: {
  securityLoader?: SecurityLoader;
  onRecoveryDeliveryLockChange: (locked: boolean) => void;
  deliveryLocked: boolean;
}) {
  const { t } = useT();
  const { lang, setLang } = useLang();
  const [section, setSection] = useState<SettingsSection>('general');

  return (
    <main className="settingsPage">
      <h1>{t('settings.title')}</h1>
      <div role="tablist" className="settingsTabs" aria-label={t('settings.title')}>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'general'}
          className={section === 'general' ? 'active' : ''}
          disabled={deliveryLocked}
          onClick={() => setSection('general')}
        >
          {t('settings.tabGeneral')}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'security'}
          className={section === 'security' ? 'active' : ''}
          disabled={deliveryLocked}
          onClick={() => setSection('security')}
        >
          {t('settings.tabSecurity')}
        </button>
      </div>
      {section === 'general' ? (
        <section className="settingsPanel" aria-labelledby="language-setting-title">
          <h2 id="language-setting-title">{t('settings.language')}</h2>
          <p className="muted">{t('settings.languageHint')}</p>
          {/* 语言名称按惯例以各自语言原文显示,不随界面语言翻译 */}
          <div className="languageOptions" role="group" aria-label={t('settings.language')}>
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
              English
            </button>
          </div>
        </section>
      ) : (
        /* 复用 .securityPage 既有后代样式,settingsSecurityHost 去掉页面级留白 */
        <div className="securityPage settingsSecurityHost">
          <SecurityPanel loader={securityLoader} onRecoveryDeliveryLockChange={onRecoveryDeliveryLockChange} />
        </div>
      )}
    </main>
  );
}
