import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App, AppView, useAgentTokenState } from '../App';
import { StrictMode, useEffect } from 'react';
import * as api from '../api';

vi.mock('../components/SecurityView', async (importOriginal) => {
  await new Promise((resolve) => setTimeout(resolve, 20));
  return importOriginal<typeof import('../components/SecurityView')>();
});

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>();
  return {
    ...actual,
    closeSession: vi.fn(),
    createAgentToken: vi.fn(),
    createSession: vi.fn(),
    deleteAgentToken: vi.fn(),
    disableTwoFactor: vi.fn(),
    enableTwoFactor: vi.fn(),
    getTwoFactorStatus: vi.fn(),
    listDeviceFiles: vi.fn(),
    deviceFileURL: vi.fn(() => ''),
    uploadDeviceFile: vi.fn(),
    listDevices: vi.fn(),
    listAgentTokens: vi.fn(),
    listSessionOutput: vi.fn(),
    listSessions: vi.fn(),
    listSnippets: vi.fn(() => Promise.resolve([])),
    login: vi.fn(),
    me: vi.fn(),
    revokeAgentToken: vi.fn(),
    renameDevice: vi.fn(),
    renameSession: vi.fn(),
    regenerateRecoveryCodes: vi.fn(),
    startTwoFactorSetup: vi.fn(),
    verifyTwoFactor: vi.fn(),
  };
});

const mockedApi = vi.mocked(api);

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

// 通过 设置 → 安全 分区打开 2FA 界面(替代原独立 Security 导航项)
async function openSecuritySettings() {
  await userEvent.click(screen.getByRole('button', { name: 'Settings' }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
}

beforeEach(() => {
  vi.resetAllMocks();
  mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: false, recoveryCodesRemaining: 0 });
  mockedApi.listAgentTokens.mockResolvedValue([]);
});

function loggedInAppViewProps(
  overrides: Partial<Parameters<typeof AppView>[0]> = {}
): Parameters<typeof AppView>[0] {
  return {
    user: { id: 'user-1', username: 'admin' },
    devices: [],
    sessions: {},
    onLogin: vi.fn(),
    onVerifyTwoFactor: vi.fn(),
    onCloseSession: vi.fn(),
    onCreateSession: vi.fn(),
    onRenameSession: vi.fn(),
    agentTokens: [],
    createdAgentToken: null,
    tokenLoading: false,
    tokenError: null,
    onCreateAgentToken: vi.fn(),
    onRevokeAgentToken: vi.fn(),
    onRefreshAgentTokens: vi.fn(),
    ...overrides,
  };
}

function TokenStateHarness({ user }: { user: api.User | null }) {
  const state = useAgentTokenState(user);
  return (
    <div>
      <span>{state.loading ? 'loading' : 'idle'}</span>
      <span>{state.error ?? ''}</span>
      {state.tokens.map((token) => <span key={token.id}>{token.name}</span>)}
      {state.createdToken && <span>{state.createdToken.token}</span>}
      <button onClick={() => void state.load()}>list</button>
      <button onClick={() => void state.create('agent-a', 24).catch(() => undefined)}>create</button>
      <button onClick={() => void state.revoke('token-a').catch(() => undefined)}>revoke</button>
      <button onClick={() => void state.remove('token-a').catch(() => undefined)}>delete</button>
    </div>
  );
}

function AutoLoadTokenHarness({ user }: { user: api.User }) {
  const state = useAgentTokenState(user);
  useEffect(() => { void state.load(); }, [state.load]);
  return <span>{state.loading ? 'loading' : state.tokens.map((token) => token.name).join(',') || 'empty'}</span>;
}

describe('useAgentTokenState', () => {
  it.each(['create', 'revoke', 'delete'] as const)(
    '用户B发起pending %s时不会重新展开用户A的Token状态',
    async (operation) => {
      mockedApi.createAgentToken.mockResolvedValueOnce({
        id: 'token-a', name: '用户A-Token', token: '用户A-明文', created_at: '', expires_at: '',
      });
      const { rerender } = render(<TokenStateHarness user={{ id: 'user-a', username: 'a' }} />);
      await userEvent.click(screen.getByRole('button', { name: 'create' }));
      expect(await screen.findByText('用户A-明文')).toBeInTheDocument();

      const request = deferred<never>();
      if (operation === 'create') mockedApi.createAgentToken.mockReturnValue(request.promise);
      if (operation === 'revoke') mockedApi.revokeAgentToken.mockReturnValue(request.promise);
      if (operation === 'delete') mockedApi.deleteAgentToken.mockReturnValue(request.promise);
      rerender(<TokenStateHarness user={{ id: 'user-b', username: 'b' }} />);
      await userEvent.click(screen.getByRole('button', { name: operation }));

      expect(screen.queryByText('用户A-Token')).not.toBeInTheDocument();
      expect(screen.queryByText('用户A-明文')).not.toBeInTheDocument();
      await act(async () => request.reject(new Error('用户B请求失败')));
      expect(screen.queryByText('用户A-Token')).not.toBeInTheDocument();
      expect(screen.queryByText('用户A-明文')).not.toBeInTheDocument();
    }
  );

  it('首次列表完成后load保持稳定且不产生刷新循环', async () => {
    mockedApi.listAgentTokens.mockResolvedValue([{ id: 'token-1', name: 'stable', created_at: '', expires_at: '' }]);
    render(<AutoLoadTokenHarness user={{ id: 'user-a', username: 'a' }} />);
    expect(await screen.findByText('stable')).toBeInTheDocument();
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(mockedApi.listAgentTokens).toHaveBeenCalledOnce();
  });

  it.each(['success', 'failure'] as const)('StrictMode下list %s仍复位loading', async (outcome) => {
    if (outcome === 'success') mockedApi.listAgentTokens.mockResolvedValue([]);
    else mockedApi.listAgentTokens.mockRejectedValue(new Error('failed'));
    render(<StrictMode><AutoLoadTokenHarness user={{ id: 'user-a', username: 'a' }} /></StrictMode>);
    expect(await screen.findByText('empty')).toBeInTheDocument();
    expect(screen.queryByText('loading')).not.toBeInTheDocument();
  });

  it('StrictMode下create成功仍可写回明文Token并复位loading', async () => {
    mockedApi.createAgentToken.mockResolvedValue({ id: 'token-a', name: 'agent-a', token: 'strict-raw', created_at: '', expires_at: '' });
    render(<StrictMode><TokenStateHarness user={{ id: 'user-a', username: 'a' }} /></StrictMode>);
    await userEvent.click(screen.getByRole('button', { name: 'create' }));
    expect(await screen.findByText('strict-raw')).toBeInTheDocument();
    expect(screen.getByText('idle')).toBeInTheDocument();
  });
  it.each(['list', 'create', 'revoke', 'delete'] as const)(
    '用户切换后忽略用户A延迟的 %s 成功和失败结果',
    async (operation) => {
      const request = deferred<never>();
      const apiMethod = {
        list: mockedApi.listAgentTokens,
        create: mockedApi.createAgentToken,
        revoke: mockedApi.revokeAgentToken,
        delete: mockedApi.deleteAgentToken,
      }[operation];
      apiMethod.mockReturnValue(request.promise);
      const { rerender } = render(<TokenStateHarness user={{ id: 'user-a', username: 'a' }} />);

      await userEvent.click(screen.getByRole('button', { name: operation }));
      rerender(<TokenStateHarness user={{ id: 'user-b', username: 'b' }} />);
      expect(screen.getByText('idle')).toBeInTheDocument();

      await act(async () => request.reject(new Error('用户A请求失败')));
      expect(screen.queryByText('用户A请求失败')).not.toBeInTheDocument();
      expect(screen.queryByText('raw-token-a')).not.toBeInTheDocument();
      expect(screen.getByText('idle')).toBeInTheDocument();
    }
  );

  it('用户A延迟创建成功后切换用户也绝不显示明文Token', async () => {
    const request = deferred<api.CreatedAgentToken>();
    mockedApi.createAgentToken.mockReturnValue(request.promise);
    const { rerender } = render(<TokenStateHarness user={{ id: 'user-a', username: 'a' }} />);
    await userEvent.click(screen.getByRole('button', { name: 'create' }));
    rerender(<TokenStateHarness user={{ id: 'user-b', username: 'b' }} />);

    await act(async () => request.resolve({
      id: 'token-a', name: 'agent-a', token: 'raw-token-a', created_at: '', expires_at: '',
    }));
    expect(screen.queryByText('raw-token-a')).not.toBeInTheDocument();
  });

  it.each(['list', 'revoke'] as const)('用户A延迟 %s 成功后不污染用户B', async (operation) => {
    const request = deferred<api.AgentToken[] | api.AgentToken>();
    (operation === 'list' ? mockedApi.listAgentTokens : mockedApi.revokeAgentToken).mockReturnValue(request.promise as never);
    const { rerender } = render(<TokenStateHarness user={{ id: 'user-a', username: 'a' }} />);
    await userEvent.click(screen.getByRole('button', { name: operation }));
    rerender(<TokenStateHarness user={{ id: 'user-b', username: 'b' }} />);
    const token = { id: 'token-a', name: '用户A-Token', created_at: '', expires_at: '' };
    await act(async () => request.resolve(operation === 'list' ? [token] : token));
    expect(screen.queryByText('用户A-Token')).not.toBeInTheDocument();
  });
});

describe('AppView', () => {
  it('shows login when there is no user', () => {
    render(
      <AppView
        user={null}
        devices={[]}
        sessions={{}}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument();
  });

  it('二因素登录提供提示和可样式化的Back按钮', async () => {
    const onLogin = vi.fn().mockResolvedValue({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
    render(<AppView {...loggedInAppViewProps({ user: null, onLogin })} />);

    await userEvent.type(screen.getByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));

    expect(await screen.findByText('Enter the code from your authenticator app.')).toHaveClass('loginHint');
    expect(screen.getByRole('button', { name: 'Back to login' })).toHaveClass('loginBackButton');
  });

  it('opens multiple terminal tabs for an online device', async () => {
    const createSession = vi.fn()
      .mockResolvedValueOnce({ id: 'sess-1', title: 'bash', status: 'running', working_directory: '/tmp/project' })
      .mockResolvedValueOnce({ id: 'sess-2', title: 'bash', status: 'running', working_directory: '/home/admin' });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{}}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={createSession}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );
    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
    expect(await screen.findByRole('tab', { name: /sess-1/i })).toBeInTheDocument();
    expect(await screen.findByRole('tab', { name: /sess-2/i })).toBeInTheDocument();
  });

  it('restores existing sessions after browser refresh', async () => {
    mockedApi.me.mockResolvedValue({ id: 'user-1', username: 'admin' });
    mockedApi.listDevices.mockResolvedValue([{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]);
    mockedApi.listSessions.mockResolvedValue([
      { id: 'sess-restored', title: 'bash', status: 'running', working_directory: '/srv/app' },
    ]);

    render(<App />);

    const tab = await screen.findByRole('tab', { name: /sess-res/i });
    expect(tab).toBeInTheDocument();
    expect(within(tab).getByText('/srv/app')).toBeInTheDocument();
    expect(mockedApi.listSessions).toHaveBeenCalledWith('dev-1');
  });

  it('shows the session directory and status color in terminal tabs', () => {
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{
          'dev-1': [
            { id: 'sess-running-1234', title: 'bash', status: 'running', working_directory: '/work/app' },
            { id: 'sess-lost-5678', title: 'claude', status: 'lost', working_directory: '/work/agent' },
          ],
        }}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    expect(screen.getByText('/work/app')).toBeInTheDocument();
    expect(screen.getByText('/work/agent')).toBeInTheDocument();
    expect(screen.getByText('running')).toHaveClass('statusRunning');
    expect(screen.getByText('lost')).toHaveClass('statusLost');
  });

  it('shows the owning device in terminal tabs and the active terminal header', () => {
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'office-laptop', platform: 'linux', online: true }]}
        sessions={{
          'dev-1': [
            {
              id: 'sess-running-1234',
              title: 'shell',
              status: 'running',
              shell_path: '/bin/bash',
              working_directory: '/work/app',
            },
          ],
        }}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    const tab = screen.getByRole('tab', { name: /bash/i });
    expect(within(tab).getByText('bash')).toBeInTheDocument();
    expect(within(tab).getByText('office-laptop')).toHaveClass('tabDeviceBadge');
    expect(screen.getByRole('heading', { name: /bash office-laptop/i })).toBeInTheDocument();
    expect(screen.getByText('linux · /work/app · session sess-running-1234')).toBeInTheDocument();
  });

  it('keeps custom session titles instead of replacing them with the shell path', () => {
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'office-laptop', platform: 'linux', online: true }]}
        sessions={{
          'dev-1': [
            {
              id: 'sess-custom-title',
              title: 'api server',
              status: 'running',
              shell_path: '/usr/bin/zsh',
              working_directory: '/srv/api',
            },
          ],
        }}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    const tab = screen.getByRole('tab', { name: /api server/i });
    expect(within(tab).getByText('api server')).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: /zsh/i })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /api server office-laptop/i })).toBeInTheDocument();
  });

  it('renames devices from the device list', async () => {
    const renameDevice = vi.fn().mockResolvedValue({
      id: 'dev-1',
      name: 'office-laptop',
      platform: 'linux',
      online: true,
    });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{}}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        onRenameDevice={renameDevice}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /rename laptop/i }));
    const input = await screen.findByRole('textbox', { name: /device name/i });
    await userEvent.clear(input);
    await userEvent.type(input, 'office-laptop');
    await userEvent.click(screen.getByRole('button', { name: /save device name/i }));

    expect(renameDevice).toHaveBeenCalledWith('dev-1', 'office-laptop');
    expect(await screen.findByText('office-laptop')).toBeInTheDocument();
  });

  it('hides closed sessions from terminal tabs', () => {
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{
          'dev-1': [
            { id: 'sess-running', title: 'bash', status: 'running', working_directory: '/work/app' },
            { id: 'sess-closed', title: 'old shell', status: 'closed', working_directory: '/work/old' },
          ],
        }}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    expect(screen.getByRole('tab', { name: /bash/i })).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: /old shell/i })).not.toBeInTheDocument();
  });

  it('renames and removes terminal tabs', async () => {
    const closeSession = vi.fn().mockResolvedValue(undefined);
    const renameSession = vi.fn().mockResolvedValue({
      id: 'sess-1',
      title: 'api server',
      status: 'running',
      working_directory: '/tmp/project',
    });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{ 'dev-1': [{ id: 'sess-1', title: 'bash', status: 'running', working_directory: '/tmp/project' }] }}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={closeSession}
        onCreateSession={vi.fn()}
        onRenameSession={renameSession}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /rename bash/i }));
    const input = await screen.findByLabelText(/session title/i);
    await userEvent.clear(input);
    await userEvent.type(input, 'api server');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    expect(renameSession).toHaveBeenCalledWith('sess-1', 'api server');
    expect(await screen.findByRole('tab', { name: /api server/i })).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /delete api server/i }));
    expect(closeSession).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: /confirm delete api server/i }));
    expect(closeSession).toHaveBeenCalledWith('sess-1');
    expect(screen.queryByRole('tab', { name: /api server/i })).not.toBeInTheDocument();
  });

  it('shows agent tokens after switching views', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[]}
        sessions={{}}
        agentTokens={[
          {
            id: 'tok-available-123',
            name: 'laptop',
            created_at: new Date().toISOString(),
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        ]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={refresh}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /agent tokens/i }));

    expect(screen.getByRole('heading', { name: /agent tokens/i })).toBeInTheDocument();
    expect(screen.getByText('laptop')).toBeInTheDocument();
    expect(screen.getByText('available')).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });

  it('通过可访问侧栏按钮打开Security并请求状态', async () => {
    render(<AppView {...loggedInAppViewProps()} />);

    const settings = screen.getByRole('button', { name: 'Settings' });
    expect(settings.tagName).toBe('BUTTON');
    settings.focus();
    await userEvent.keyboard('{Enter}');
    await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));

    expect(screen.getByRole('status', { name: 'Loading security settings' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
    expect(await screen.findByText('Two-factor authentication is disabled.')).toBeInTheDocument();
    expect(mockedApi.getTwoFactorStatus).toHaveBeenCalledOnce();
  });

  it('Security分包失败时保留终端导航并可重试成功', async () => {
    let attempts = 0;
    const securityLoader = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error('chunk failed');
      return { default: () => <h1>重试后的安全设置</h1> };
    });
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const preventChunkError = (event: ErrorEvent) => event.preventDefault();
    window.addEventListener('error', preventChunkError);
    render(<AppView {...loggedInAppViewProps({ securityLoader })} />);
    await openSecuritySettings();
    expect(await screen.findByRole('alert')).toHaveTextContent('Security settings failed to load.');
    expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toBeEnabled();
    await userEvent.click(screen.getByRole('button', { name: 'Retry loading security settings' }));
    expect(await screen.findByRole('heading', { name: '重试后的安全设置' })).toBeInTheDocument();
    expect(securityLoader).toHaveBeenCalledTimes(2);
    window.removeEventListener('error', preventChunkError);
    errorSpy.mockRestore();
  });

  it('切换Security时保留TerminalTabs活动标签', async () => {
    render(
      <AppView
        {...loggedInAppViewProps({
          sessions: {
            'dev-1': [
              { id: 'session-first', title: 'first', status: 'running' },
              { id: 'session-second', title: 'second', status: 'running' },
            ],
          },
        })}
      />
    );

    const second = screen.getByRole('tab', { name: /second/i });
    await userEvent.click(second);
    expect(second).toHaveAttribute('aria-selected', 'true');
    await openSecuritySettings();
    await userEvent.click(screen.getByRole('button', { name: 'Terminals' }));

    expect(screen.getByRole('tab', { name: /second/i })).toHaveAttribute('aria-selected', 'true');
  });

  it('切换Security时保留AgentToken草稿且不重复刷新', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    render(<AppView {...loggedInAppViewProps({ onRefreshAgentTokens: refresh })} />);

    await userEvent.click(screen.getByRole('button', { name: 'Agent Tokens' }));
    await waitFor(() => expect(refresh).toHaveBeenCalledOnce());
    const name = screen.getByLabelText('Token name');
    await userEvent.clear(name);
    await userEvent.type(name, 'draft-agent');
    await openSecuritySettings();
    expect(name).not.toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: 'Agent Tokens' }));

    expect(screen.getByLabelText('Token name')).toHaveValue('draft-agent');
    expect(refresh).toHaveBeenCalledOnce();
  });

  it('标记当前导航并在Security往返时保留终端和Token状态', async () => {
    const createSession = vi.fn().mockResolvedValue({
      id: 'sess-preserved',
      title: 'bash',
      status: 'running',
      working_directory: '/srv/preserved',
    });
    render(
      <AppView
        {...loggedInAppViewProps({
          devices: [{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }],
          onCreateSession: createSession,
          agentTokens: [{
            id: 'tok-preserved',
            name: 'preserved-token',
            created_at: new Date().toISOString(),
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          }],
        })}
      />
    );

    const terminals = screen.getByRole('button', { name: 'Terminals' });
    const settings = screen.getByRole('button', { name: 'Settings' });
    const agentTokens = screen.getByRole('button', { name: 'Agent Tokens' });
    expect(terminals).toHaveAttribute('aria-current', 'page');
    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
    expect(await screen.findByRole('tab', { name: /sess-pre/i })).toBeInTheDocument();

    await userEvent.click(settings);
    await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
    expect(settings).toHaveAttribute('aria-current', 'page');
    expect(terminals).not.toHaveAttribute('aria-current');
    await userEvent.click(terminals);
    expect(screen.getByRole('tab', { name: /sess-pre/i })).toBeInTheDocument();

    await userEvent.click(agentTokens);
    expect(agentTokens).toHaveAttribute('aria-current', 'page');
    expect(screen.getByText('preserved-token')).toBeInTheDocument();
    await userEvent.click(settings);
    await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
    await userEvent.click(agentTokens);
    expect(screen.getByText('preserved-token')).toBeInTheDocument();
  });

  it('未登录时不显示Settings导航', () => {
    render(<AppView {...loggedInAppViewProps({ user: null })} />);

    expect(screen.queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: 'Primary' })).not.toBeInTheDocument();
  });

  it('Security卸载后延迟状态请求不会污染当前终端视图', async () => {
    const status = deferred<api.TwoFactorStatus>();
    mockedApi.getTwoFactorStatus.mockReturnValue(status.promise);
    render(<AppView {...loggedInAppViewProps()} />);

    await openSecuritySettings();
    expect(screen.getByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Terminals' }));
    await act(async () => {
      status.resolve({ enabled: true, recoveryCodesRemaining: 8 });
      await status.promise;
    });

    expect(screen.queryByRole('heading', { name: 'Two-factor security' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');
    expect(screen.queryByText('8 recovery codes remaining.')).not.toBeInTheDocument();
  });

  it('登出或切换用户后回到终端默认视图', async () => {
    const props = loggedInAppViewProps();
    const { rerender } = render(<AppView {...props} />);
    await openSecuritySettings();
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();

    rerender(<AppView {...props} user={null} />);
    expect(screen.getByRole('button', { name: 'Login' })).toBeInTheDocument();
    rerender(<AppView {...props} user={{ id: 'user-2', username: 'operator' }} />);

    await waitFor(() => expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page'));
    expect(screen.queryByRole('heading', { name: 'Two-factor security' })).not.toBeInTheDocument();
  });

  it('用户A的Security秘密和旧请求不能进入用户B子树', async () => {
    const recoveryRequest = deferred<string[]>();
    mockedApi.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'USER-A-MANUAL-SECRET',
      otpauthURI: 'otpauth://totp/example?secret=USER-A-MANUAL-SECRET',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    mockedApi.enableTwoFactor.mockReturnValue(recoveryRequest.promise);
    const props = loggedInAppViewProps();
    const { rerender } = render(<AppView {...props} />);

    await openSecuritySettings();
    await userEvent.click(await screen.findByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret-a');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(await screen.findByText('USER-A-MANUAL-SECRET')).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));

    rerender(<AppView {...props} user={{ id: 'user-2', username: 'operator' }} />);
    expect(screen.queryByText('USER-A-MANUAL-SECRET')).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'Two-factor setup QR code' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');

    await act(async () => {
      recoveryRequest.resolve(Array.from({ length: 10 }, (_, index) => `USER-A-RECOVERY-${index}`));
      await recoveryRequest.promise;
    });
    expect(screen.queryByText('USER-A-RECOVERY-0')).not.toBeInTheDocument();
  });

  it('启用请求和恢复码交付期间阻止离开Security，Done后恢复导航', async () => {
    const recoveryRequest = deferred<string[]>();
    mockedApi.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'DELIVERY-SECRET', otpauthURI: 'otpauth://totp/example?secret=DELIVERY-SECRET', expiresAt: '',
    });
    mockedApi.enableTwoFactor.mockReturnValue(recoveryRequest.promise);
    render(<AppView {...loggedInAppViewProps()} />);
    await openSecuritySettings();
    await userEvent.click(await screen.findByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));

    expect(screen.getByRole('button', { name: 'Terminals' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Agent Tokens' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'General' })).toBeDisabled();
    expect(screen.getByText('DELIVERY-SECRET')).toBeInTheDocument();
    await act(async () => recoveryRequest.resolve(['DELIVERY-CODE']));
    expect(await screen.findByText('DELIVERY-CODE')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'General' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.getByRole('button', { name: 'Terminals' })).toBeEnabled();
  });

  it('轮换恢复码失败后恢复导航且不会卸载pending组件', async () => {
    const request = deferred<string[]>();
    mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: true, recoveryCodesRemaining: 3 });
    mockedApi.regenerateRecoveryCodes.mockReturnValue(request.promise);
    render(<AppView {...loggedInAppViewProps()} />);
    await openSecuritySettings();
    await userEvent.click(await screen.findByRole('button', { name: 'Regenerate recovery codes' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.type(screen.getByLabelText('Authenticator code'), '654321');
    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));
    expect(screen.getByRole('button', { name: 'Terminals' })).toBeDisabled();
    expect(screen.getByLabelText('Authenticator code')).toBeInTheDocument();
    await act(async () => request.reject(new Error('rotation failed')));
    expect(await screen.findByRole('alert')).toHaveTextContent('rotation failed');
    expect(screen.getByRole('button', { name: 'Terminals' })).toBeEnabled();
  });

  it('creates and revokes agent tokens from the management view', async () => {
    const createToken = vi.fn().mockResolvedValue(undefined);
    const revokeToken = vi.fn().mockResolvedValue(undefined);
    const deleteToken = vi.fn().mockResolvedValue(undefined);
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[]}
        sessions={{}}
        agentTokens={[
          {
            id: 'tok-1',
            name: 'desk',
            created_at: new Date().toISOString(),
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        ]}
        createdAgentToken={{
          id: 'tok-new',
          name: 'desk',
          token: 'raw-token-once',
          created_at: new Date().toISOString(),
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        }}
        tokenLoading={false}
        tokenError={null}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        onCreateAgentToken={createToken}
        onRevokeAgentToken={revokeToken}
        onDeleteAgentToken={deleteToken}
        onRefreshAgentTokens={vi.fn().mockResolvedValue(undefined)}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /agent tokens/i }));
    await userEvent.clear(screen.getByLabelText(/name/i));
    await userEvent.type(screen.getByLabelText(/name/i), 'rack');
    await userEvent.clear(screen.getByLabelText(/ttl hours/i));
    await userEvent.type(screen.getByLabelText(/ttl hours/i), '12');
    await userEvent.click(screen.getByRole('button', { name: /create/i }));

    expect(createToken).toHaveBeenCalledWith('rack', 12);
    const createdTokenPanel = screen.getByRole('status');
    expect(within(createdTokenPanel).getByText('Token name')).toBeInTheDocument();
    expect(within(createdTokenPanel).getByText('desk')).toBeInTheDocument();
    expect(screen.getByText('raw-token-once')).toBeInTheDocument();
    expect(
      screen.getByText(`vibe-agent register --server ${window.location.origin} --token raw-token-once`)
    ).toBeInTheDocument();
    expect(screen.getByText('vibe-agent run')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /revoke/i }));
    expect(revokeToken).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }));
    expect(revokeToken).toHaveBeenCalledWith('tok-1');
  });

  it('opens the file manager from the device list', async () => {
    mockedApi.listDeviceFiles.mockResolvedValue({ path: '/home/dev', entries: [] });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{}}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );
    await userEvent.click(screen.getByRole('button', { name: /browse files on laptop/i }));
    expect(await screen.findByRole('dialog', { name: /files on laptop/i })).toBeInTheDocument();
  });

  it('permanently deletes revoked agent tokens after confirmation', async () => {
    const deleteToken = vi.fn().mockResolvedValue(undefined);
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[]}
        sessions={{}}
        agentTokens={[
          {
            id: 'tok-revoked',
            name: 'old-token',
            created_at: new Date().toISOString(),
            expires_at: new Date(Date.now() + 60_000).toISOString(),
            revoked_at: new Date().toISOString(),
          },
        ]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onLogin={vi.fn()}
        onVerifyTwoFactor={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onDeleteAgentToken={deleteToken}
        onRefreshAgentTokens={vi.fn().mockResolvedValue(undefined)}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /agent tokens/i }));
    await userEvent.click(screen.getByRole('button', { name: /^delete$/i }));
    expect(deleteToken).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: /confirm delete/i }));
    expect(deleteToken).toHaveBeenCalledWith('tok-revoked');
  });

  it('收到 202 挑战时不进入主界面', async () => {
    mockedApi.me.mockRejectedValue(new Error('unauthorized'));
    mockedApi.login.mockResolvedValue({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
    render(<App />);

    await userEvent.type(await screen.findByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));

    expect(await screen.findByRole('heading', { name: 'Two-factor authentication' })).toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: 'Primary' })).not.toBeInTheDocument();
    expect(mockedApi.verifyTwoFactor).not.toHaveBeenCalled();
  });

  it('第二因素 200 后才进入主界面，延迟成功卸载不产生状态更新告警', async () => {
    let resolveVerification!: (user: api.User) => void;
    mockedApi.me.mockRejectedValue(new Error('unauthorized'));
    mockedApi.login.mockResolvedValue({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
    mockedApi.verifyTwoFactor.mockReturnValue(
      new Promise<api.User>((resolve) => {
        resolveVerification = resolve;
      })
    );
    mockedApi.listDevices.mockResolvedValue([]);
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(<App />);
    await userEvent.type(await screen.findByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));

    expect(screen.queryByRole('navigation', { name: 'Primary' })).not.toBeInTheDocument();
    resolveVerification({ id: 'user-1', username: 'admin' });
    expect(await screen.findByRole('navigation', { name: 'Primary' })).toBeInTheDocument();
    expect(mockedApi.verifyTwoFactor).toHaveBeenCalledWith('challenge-1', '123456');
    expect(errorSpy).not.toHaveBeenCalled();
    errorSpy.mockRestore();
  });

  it('第二因素失败时保留验证页且不进入主界面', async () => {
    mockedApi.me.mockRejectedValue(new Error('unauthorized'));
    mockedApi.login.mockResolvedValue({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
    mockedApi.verifyTwoFactor.mockRejectedValue(new Error('invalid two factor code'));
    render(<App />);
    await userEvent.type(await screen.findByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '000000');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid two factor code');
    expect(screen.getByRole('heading', { name: 'Two-factor authentication' })).toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: 'Primary' })).not.toBeInTheDocument();
  });

  it.each(['resolve', 'reject'] as const)('密码登录成功后忽略延迟 me %s', async (outcome) => {
    const bootstrap = deferred<api.User>();
    mockedApi.me.mockReturnValue(bootstrap.promise);
    mockedApi.login.mockResolvedValue({
      status: 'authenticated',
      user: { id: 'login-user', username: 'admin' },
    });
    mockedApi.listDevices.mockResolvedValue([]);
    render(<App />);
    await userEvent.type(await screen.findByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));
    expect(await screen.findByRole('navigation', { name: 'Primary' })).toBeInTheDocument();

    await act(async () => {
      if (outcome === 'resolve') bootstrap.resolve({ id: 'stale-user', username: 'stale' });
      else bootstrap.reject(new Error('stale unauthorized'));
      await bootstrap.promise.catch(() => undefined);
    });
    await waitFor(() => expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument());
    expect(mockedApi.listDevices).toHaveBeenCalledTimes(1);
  });

  it.each(['resolve', 'reject'] as const)('二因素成功后忽略延迟 me %s', async (outcome) => {
    const bootstrap = deferred<api.User>();
    mockedApi.me.mockReturnValue(bootstrap.promise);
    mockedApi.login.mockResolvedValue({
      status: 'two_factor_required',
      challengeToken: 'challenge-1',
      expiresIn: 300,
    });
    mockedApi.verifyTwoFactor.mockResolvedValue({ id: 'verified-user', username: 'admin' });
    mockedApi.listDevices.mockResolvedValue([]);
    render(<App />);
    await userEvent.type(await screen.findByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(await screen.findByRole('navigation', { name: 'Primary' })).toBeInTheDocument();

    await act(async () => {
      if (outcome === 'resolve') bootstrap.resolve({ id: 'stale-user', username: 'stale' });
      else bootstrap.reject(new Error('stale unauthorized'));
      await bootstrap.promise.catch(() => undefined);
    });
    await waitFor(() => expect(screen.getByRole('navigation', { name: 'Primary' })).toBeInTheDocument());
    expect(mockedApi.listDevices).toHaveBeenCalledTimes(1);
  });

  it.each(['resolve', 'reject'] as const)('卸载后忽略 me %s', async (outcome) => {
    const bootstrap = deferred<api.User>();
    mockedApi.me.mockReturnValue(bootstrap.promise);
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { unmount } = render(<App />);
    unmount();

    await act(async () => {
      if (outcome === 'resolve') bootstrap.resolve({ id: 'stale-user', username: 'stale' });
      else bootstrap.reject(new Error('stale unauthorized'));
      await bootstrap.promise.catch(() => undefined);
    });
    expect(errorSpy).not.toHaveBeenCalled();
    errorSpy.mockRestore();
  });
});

describe('移动端布局', () => {
  const realMatchMedia = window.matchMedia;
  afterEach(() => {
    window.matchMedia = realMatchMedia;
  });

  type MediaListener = (event: MediaQueryListEvent) => void;

  // 可控的 matchMedia 替身:setMatches 可模拟视口宽窄切换
  function installMatchMedia(initialMatches: boolean) {
    const listeners = new Set<MediaListener>();
    let matches = initialMatches;
    const mediaQueryList = {
      get matches() {
        return matches;
      },
      media: '(max-width: 760px)',
      onchange: null,
      addEventListener: (_type: string, listener: MediaListener) => {
        listeners.add(listener);
      },
      removeEventListener: (_type: string, listener: MediaListener) => {
        listeners.delete(listener);
      },
      addListener: (listener: MediaListener) => {
        listeners.add(listener);
      },
      removeListener: (listener: MediaListener) => {
        listeners.delete(listener);
      },
      dispatchEvent: () => false,
    };
    window.matchMedia = (() => mediaQueryList) as unknown as typeof window.matchMedia;
    return {
      setMatches(next: boolean) {
        matches = next;
        listeners.forEach((listener) => listener({ matches: next } as MediaQueryListEvent));
      },
    };
  }

  const device = { id: 'dev-1', name: 'MacBook', platform: 'darwin', online: true };

  it('移动端渲染底部标签栏,桌面侧栏不渲染', () => {
    installMatchMedia(true);
    render(<AppView {...loggedInAppViewProps({ devices: [device] })} />);
    const tabBar = screen.getByRole('navigation', { name: 'Primary' });
    expect(tabBar).toHaveClass('mobileTabBar');
    expect(within(tabBar).getByRole('button', { name: 'Devices' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Agent Tokens' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Settings' })).toBeInTheDocument();
    // 桌面侧栏的品牌区不渲染(移动端品牌在设备视图内,初始隐藏)
    expect(screen.queryByRole('heading', { name: 'Devices' })).not.toBeInTheDocument();
  });

  it('设备 tab 显示设备列表,新建会话后自动切回终端视图', async () => {
    installMatchMedia(true);
    const session = {
      id: 'sess-1',
      title: 'shell',
      status: 'running',
      device_id: 'dev-1',
    };
    const onCreateSession = vi.fn().mockResolvedValue(session);
    render(<AppView {...loggedInAppViewProps({ devices: [device], onCreateSession })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Devices' }));
    expect(screen.getByRole('heading', { name: 'Devices' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'New terminal' }));
    expect(onCreateSession).toHaveBeenCalledWith('dev-1');
    // 自动切回终端视图:激活的 tab 是 Terminals,且会话标签出现
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');
    expect(await screen.findByRole('tab', { selected: true })).toBeInTheDocument();
  });

  it('视口变宽时设备视图回退为终端视图并恢复侧栏', async () => {
    const media = installMatchMedia(true);
    render(<AppView {...loggedInAppViewProps({ devices: [device] })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Devices' }));
    act(() => media.setMatches(false));
    // 桌面侧栏出现(设备标题可见),Terminals 导航激活,tab bar 消失
    expect(screen.getByRole('heading', { name: 'Devices' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');
    expect(screen.queryByRole('button', { name: 'Devices' })).not.toBeInTheDocument();
  });

  it('恢复码交付锁定期间移动端 tab 禁用(设置除外)', async () => {
    installMatchMedia(true);
    const recoveryRequest = deferred<string[]>();
    mockedApi.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MOBILE-SECRET',
      otpauthURI: 'otpauth://totp/example?secret=MOBILE-SECRET',
      expiresAt: '',
    });
    mockedApi.enableTwoFactor.mockReturnValue(recoveryRequest.promise);
    render(<AppView {...loggedInAppViewProps()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Settings' }));
    await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    const tabBar = screen.getByRole('navigation', { name: 'Primary' });
    expect(within(tabBar).getByRole('button', { name: 'Devices' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Agent Tokens' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Settings' })).toBeEnabled();
    await act(async () => recoveryRequest.resolve(['MOBILE-CODE']));
    await userEvent.click(await screen.findByRole('button', { name: 'Done' }));
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeEnabled();
  });
});
