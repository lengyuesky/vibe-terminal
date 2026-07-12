import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentTokenManager } from '../components/AgentTokenManager';
import { LanguageProvider } from '../i18n';

function renderManager(overrides: Partial<Parameters<typeof AgentTokenManager>[0]> = {}) {
  return render(
    <LanguageProvider>
      <AgentTokenManager
        tokens={[]}
        loading={false}
        error={null}
        createdToken={null}
        onCreate={vi.fn()}
        onRevoke={vi.fn()}
        onDelete={vi.fn()}
        onRefresh={vi.fn().mockResolvedValue(undefined)}
        {...overrides}
      />
    </LanguageProvider>
  );
}

afterEach(() => {
  window.localStorage.clear();
  document.documentElement.lang = 'en';
});

describe('AgentTokenManager i18n', () => {
  it('中文环境显示中文文案', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    renderManager();
    expect(screen.getByRole('heading', { name: 'Agent 令牌' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '创建令牌' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '刷新' })).toBeInTheDocument();
    expect(screen.getByText('暂无 Agent 令牌。')).toBeInTheDocument();
  });

  it('错误类别渲染为本地化消息', () => {
    renderManager({ error: 'load' });
    expect(screen.getByText('Failed to load agent tokens.')).toBeInTheDocument();
  });

  it('令牌状态值本地化', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    renderManager({
      tokens: [
        {
          id: 'tok-0001-abcd',
          name: 'desk',
          created_at: '2026-07-01T00:00:00Z',
          expires_at: '2099-01-01T00:00:00Z',
        },
      ],
    });
    expect(screen.getByText('可用')).toBeInTheDocument();
  });
});
