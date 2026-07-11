import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { SecurityView } from '../components/SecurityView';

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>();
  return {
    ...actual,
    disableTwoFactor: vi.fn(),
    enableTwoFactor: vi.fn(),
    getTwoFactorStatus: vi.fn(),
    regenerateRecoveryCodes: vi.fn(),
    startTwoFactorSetup: vi.fn(),
  };
});

const mockedAPI = vi.mocked(api);
const recoveryCodes = Array.from({ length: 10 }, (_, index) => `RECOVERY-${index + 1}`);

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.resetAllMocks();
  mockedAPI.getTwoFactorStatus.mockResolvedValue({ enabled: false, recoveryCodesRemaining: 0 });
});

afterEach(() => {
  Reflect.deleteProperty(navigator, 'clipboard');
  Reflect.deleteProperty(URL, 'createObjectURL');
  Reflect.deleteProperty(URL, 'revokeObjectURL');
  vi.restoreAllMocks();
});

async function reachRecoveryCodes() {
  mockedAPI.startTwoFactorSetup.mockResolvedValue({
    manualKey: 'MANUALKEY',
    otpauthURI: 'otpauth://totp/example?secret=MANUALKEY',
    expiresAt: '2026-07-11T15:00:00Z',
  });
  mockedAPI.enableTwoFactor.mockResolvedValue(recoveryCodes);
  render(<SecurityView />);
  await screen.findByText('Two-factor authentication is disabled.');
  await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
  await userEvent.type(screen.getByLabelText('Current password'), 'secret');
  await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
  await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
  await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
  await screen.findByRole('heading', { name: 'Save your recovery codes' });
}

describe('SecurityView', () => {
  it('完成启用流程并在 Done 后彻底移除一次性恢复码', async () => {
    mockedAPI.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/Vibe%20Terminal:admin?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    mockedAPI.enableTwoFactor.mockResolvedValue(recoveryCodes);
    render(<SecurityView />);

    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
    expect(screen.getByText('Two-factor authentication is disabled.')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));

    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await waitFor(() => expect(mockedAPI.startTwoFactorSetup).toHaveBeenCalledWith('secret'));

    expect(await screen.findByRole('img', { name: 'Two-factor setup QR code' })).toBeInTheDocument();
    expect(screen.getByText('MANUALKEY')).toBeInTheDocument();
    expect(screen.getByText(/Setup expires/)).toHaveTextContent('2026-07-11T15:00:00Z');
    await userEvent.type(screen.getByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    await waitFor(() => expect(mockedAPI.enableTwoFactor).toHaveBeenCalledWith('123456'));

    expect(await screen.findByRole('heading', { name: 'Save your recovery codes' })).toBeInTheDocument();
    for (const code of recoveryCodes) expect(screen.getByText(code)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Done' }));

    expect(await screen.findByText('Two-factor authentication is enabled.')).toBeInTheDocument();
    for (const code of recoveryCodes) expect(screen.queryByText(code)).not.toBeInTheDocument();
    expect(screen.queryByText('MANUALKEY')).not.toBeInTheDocument();
  });

  it('使用当前密码和TOTP轮换恢复码并在完成后清空敏感字段', async () => {
    mockedAPI.getTwoFactorStatus.mockResolvedValue({ enabled: true, recoveryCodesRemaining: 3 });
    mockedAPI.regenerateRecoveryCodes.mockResolvedValue(recoveryCodes);
    render(<SecurityView />);

    expect(await screen.findByText('3 recovery codes remaining.')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.type(screen.getByLabelText('Authenticator code'), '654321');
    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));

    await waitFor(() =>
      expect(mockedAPI.regenerateRecoveryCodes).toHaveBeenCalledWith('secret', '654321')
    );
    expect(await screen.findByRole('heading', { name: 'Save your recovery codes' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(await screen.findByText('10 recovery codes remaining.')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');
    expect(screen.getByLabelText('Authenticator code')).toHaveValue('');
  });

  it('确认当前密码后关闭二因素并清空密码', async () => {
    mockedAPI.getTwoFactorStatus.mockResolvedValue({ enabled: true, recoveryCodesRemaining: 4 });
    mockedAPI.disableTwoFactor.mockResolvedValue(undefined);
    render(<SecurityView />);

    await screen.findByText('Two-factor authentication is enabled.');
    await userEvent.click(screen.getByRole('button', { name: 'Disable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm disable two-factor authentication' }));

    await waitFor(() => expect(mockedAPI.disableTwoFactor).toHaveBeenCalledWith('secret'));
    expect(await screen.findByText('Two-factor authentication is disabled.')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');
  });

  it('展示未认证和管理API错误并停留在当前步骤', async () => {
    mockedAPI.getTwoFactorStatus.mockRejectedValueOnce(new api.APIError(401, 'unauthorized', 'login required'));
    const first = render(<SecurityView />);
    expect(await screen.findByRole('alert')).toHaveTextContent('login required');
    first.unmount();

    mockedAPI.getTwoFactorStatus.mockResolvedValue({ enabled: false, recoveryCodesRemaining: 0 });
    mockedAPI.startTwoFactorSetup.mockRejectedValue(
      new api.APIError(409, 'two_factor_state_conflict', 'two factor state changed')
    );
    const second = render(<SecurityView />);
    await screen.findByText('Two-factor authentication is disabled.');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('two factor state changed');
    expect(screen.getByLabelText('Current password')).toHaveValue('secret');
    second.unmount();

    mockedAPI.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/example?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    mockedAPI.enableTwoFactor.mockRejectedValue(
      new api.APIError(409, 'two_factor_setup_expired', 'two factor setup expired')
    );
    render(<SecurityView />);
    await screen.findByText('Two-factor authentication is disabled.');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('two factor setup expired');
    expect(screen.getByText('MANUALKEY')).toBeInTheDocument();
    expect(screen.getByLabelText('Authenticator code')).toHaveValue('123456');
  });

  it('取消启用各步骤会清空密码、验证码和setup', async () => {
    mockedAPI.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/example?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    render(<SecurityView />);
    await screen.findByText('Two-factor authentication is disabled.');

    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'first-secret');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');

    await userEvent.type(screen.getByLabelText('Current password'), 'second-secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('MANUALKEY')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');
  });

  it('取消轮换和关闭会清空各自敏感字段', async () => {
    mockedAPI.getTwoFactorStatus.mockResolvedValue({ enabled: true, recoveryCodesRemaining: 5 });
    render(<SecurityView />);
    await screen.findByText('Two-factor authentication is enabled.');

    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.type(screen.getByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await userEvent.click(screen.getByRole('button', { name: 'Regenerate recovery codes' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');
    expect(screen.getByLabelText('Authenticator code')).toHaveValue('');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    await userEvent.click(screen.getByRole('button', { name: 'Disable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await userEvent.click(screen.getByRole('button', { name: 'Disable two-factor authentication' }));
    expect(screen.getByLabelText('Current password')).toHaveValue('');
  });

  it('可复制和下载一次性恢复码并回收下载URL', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const createObjectURL = vi.fn().mockReturnValue('blob:recovery-codes');
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    await reachRecoveryCodes();

    await userEvent.click(screen.getByRole('button', { name: 'Copy recovery codes' }));
    expect(writeText).toHaveBeenCalledWith(recoveryCodes.join('\n'));
    await userEvent.click(screen.getByRole('button', { name: 'Download recovery codes' }));
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob));
    expect(click).toHaveBeenCalledOnce();
    expect(click.mock.instances[0]).toMatchObject({ download: 'vibe-terminal-recovery-codes.txt' });
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:recovery-codes');
  });

  it('复制或下载失败会提示且不会清除恢复码，Done才清除', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('clipboard denied'));
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => {
        throw new Error('download blocked');
      }),
    });
    await reachRecoveryCodes();

    await userEvent.click(screen.getByRole('button', { name: 'Copy recovery codes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('clipboard denied');
    expect(screen.getByText(recoveryCodes[0])).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Download recovery codes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('download blocked');
    expect(screen.getByText(recoveryCodes[0])).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText(recoveryCodes[0])).not.toBeInTheDocument();
  });

  it('管理请求未决时禁用表单、Cancel和重复提交', async () => {
    const pending = deferred<api.TwoFactorSetup>();
    mockedAPI.startTwoFactorSetup.mockReturnValue(pending.promise);
    render(<SecurityView />);
    await screen.findByText('Two-factor authentication is disabled.');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    const submit = screen.getByRole('button', { name: 'Continue' });
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    await userEvent.click(submit);

    expect(submit).toBeDisabled();
    expect(cancel).toBeDisabled();
    expect(screen.getByLabelText('Current password')).toBeDisabled();
    await userEvent.click(submit);
    expect(mockedAPI.startTwoFactorSetup).toHaveBeenCalledTimes(1);

    pending.resolve({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/example?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    expect(await screen.findByLabelText('Authenticator code')).toBeEnabled();
  });

  it('effect清理后旧status请求不会覆盖新generation', async () => {
    const oldRequest = deferred<api.TwoFactorStatus>();
    const currentRequest = deferred<api.TwoFactorStatus>();
    mockedAPI.getTwoFactorStatus
      .mockReturnValueOnce(oldRequest.promise)
      .mockReturnValueOnce(currentRequest.promise);
    render(
      <StrictMode>
        <SecurityView />
      </StrictMode>
    );
    await waitFor(() => expect(mockedAPI.getTwoFactorStatus).toHaveBeenCalledTimes(2));

    currentRequest.resolve({ enabled: true, recoveryCodesRemaining: 6 });
    expect(await screen.findByText('6 recovery codes remaining.')).toBeInTheDocument();
    await act(async () => {
      oldRequest.resolve({ enabled: false, recoveryCodesRemaining: 0 });
      await oldRequest.promise;
    });
    expect(screen.getByText('Two-factor authentication is enabled.')).toBeInTheDocument();
    expect(screen.queryByText('Two-factor authentication is disabled.')).not.toBeInTheDocument();
  });

  it('在overview、密码、TOTP和恢复码步骤管理焦点', async () => {
    mockedAPI.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MANUALKEY',
      otpauthURI: 'otpauth://totp/example?secret=MANUALKEY',
      expiresAt: '2026-07-11T15:00:00Z',
    });
    mockedAPI.enableTwoFactor.mockResolvedValue(recoveryCodes);
    render(<SecurityView />);

    const title = await screen.findByRole('heading', { name: 'Two-factor security' });
    await waitFor(() => expect(title).toHaveFocus());
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    expect(screen.getByLabelText('Current password')).toHaveFocus();
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    const codeInput = await screen.findByLabelText('Authenticator code');
    expect(codeInput).toHaveFocus();
    await userEvent.type(codeInput, '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    const recoveryTitle = await screen.findByRole('heading', { name: 'Save your recovery codes' });
    expect(recoveryTitle).toHaveFocus();
    await userEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(title).toHaveFocus();
  });
});
