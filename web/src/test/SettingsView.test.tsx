import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LanguageProvider } from '../i18n';
import { SettingsView } from '../components/SettingsView';
import type { SecurityLoader } from '../components/SettingsView';

// 安全分区懒加载 stub:渲染一个可断言的标题
const stubSecurityLoader: SecurityLoader = async () => ({
  default: () => (
    <section aria-labelledby="security-title">
      <h1 id="security-title">Two-factor security</h1>
    </section>
  ),
});

function renderSettings(overrides: Partial<Parameters<typeof SettingsView>[0]> = {}) {
  return render(
    <LanguageProvider>
      <SettingsView
        securityLoader={stubSecurityLoader}
        onRecoveryDeliveryLockChange={vi.fn()}
        deliveryLocked={false}
        {...overrides}
      />
    </LanguageProvider>
  );
}

afterEach(() => {
  window.localStorage.clear();
  document.documentElement.lang = 'en';
});

describe('SettingsView', () => {
  it('默认显示通用分区和语言选项', () => {
    renderSettings();
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'General', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Security', selected: false })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '中文' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'English' })).toBeInTheDocument();
  });

  it('切换到安全分区后懒加载 SecurityView', async () => {
    renderSettings();
    await userEvent.click(screen.getByRole('tab', { name: 'Security' }));
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
  });

  it('点击中文立即切换界面语言并持久化', async () => {
    renderSettings();
    await userEvent.click(screen.getByRole('button', { name: '中文' }));
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '通用' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '安全' })).toBeInTheDocument();
    expect(window.localStorage.getItem('vibe.lang')).toBe('zh');
    expect(document.documentElement.lang).toBe('zh-CN');
  });

  it('当前语言选项标记为按下状态', async () => {
    renderSettings();
    expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: '中文' }));
    expect(screen.getByRole('button', { name: '中文' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('deliveryLocked 时禁用分区切换', () => {
    renderSettings({ deliveryLocked: true });
    expect(screen.getByRole('tab', { name: 'General' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'Security' })).toBeDisabled();
  });

  it('安全分包加载失败时显示错误并可重试', async () => {
    let fail = true;
    const loader: SecurityLoader = vi.fn(async () => {
      if (fail) throw new Error('chunk load failed');
      return stubSecurityLoader();
    });
    renderSettings({ securityLoader: loader });
    await userEvent.click(screen.getByRole('tab', { name: 'Security' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Security settings failed to load.');
    fail = false;
    await userEvent.click(screen.getByRole('button', { name: 'Retry loading security settings' }));
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
  });
});
