import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { APIError, type LoginResult } from '../api';
import { LoginView } from '../components/LoginView';

const challenge: LoginResult = {
  status: 'two_factor_required',
  challengeToken: 'challenge-1',
  expiresIn: 300,
};

async function submitPassword(onLogin: ReturnType<typeof vi.fn>) {
  await userEvent.type(screen.getByLabelText('Password'), 'secret');
  await userEvent.click(screen.getByRole('button', { name: 'Login' }));
  await waitFor(() => expect(onLogin).toHaveBeenCalledWith('admin', 'secret'));
}

describe('LoginView', () => {
  it('从密码登录转入 TOTP 验证并提交挑战', async () => {
    const onLogin = vi.fn().mockResolvedValue(challenge);
    const onVerifyTwoFactor = vi.fn().mockResolvedValue(undefined);
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);

    expect(screen.getByLabelText('Username')).toHaveAttribute('autocomplete', 'username');
    expect(screen.getByLabelText('Password')).toHaveAttribute('autocomplete', 'current-password');
    await submitPassword(onLogin);

    expect(await screen.findByRole('heading', { name: 'Two-factor authentication' })).toBeInTheDocument();
    const code = screen.getByLabelText('Authenticator code');
    expect(code).toHaveAttribute('autocomplete', 'one-time-code');
    await userEvent.type(code, '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(onVerifyTwoFactor).toHaveBeenCalledWith('challenge-1', '123456');
  });

  it('可切换为恢复码模式并提交恢复码', async () => {
    const onLogin = vi.fn().mockResolvedValue(challenge);
    const onVerifyTwoFactor = vi.fn().mockResolvedValue(undefined);
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);
    await submitPassword(onLogin);

    await userEvent.click(await screen.findByRole('button', { name: 'Use a recovery code' }));
    expect(screen.queryByLabelText('Authenticator code')).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Recovery code'), 'ABCD-EFGH-IJKL');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(onVerifyTwoFactor).toHaveBeenCalledWith('challenge-1', 'ABCD-EFGH-IJKL');
  });

  it('login_restart_required 会清空第二步状态并返回密码页', async () => {
    const onLogin = vi.fn().mockResolvedValue(challenge);
    const onVerifyTwoFactor = vi
      .fn()
      .mockRejectedValue(new APIError(401, 'login_restart_required', 'restart login to continue'));
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);
    await submitPassword(onLogin);
    await userEvent.click(await screen.findByRole('button', { name: 'Use a recovery code' }));
    await userEvent.type(screen.getByLabelText('Recovery code'), 'ABCD-EFGH-IJKL');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));

    expect(await screen.findByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toHaveValue('');
    expect(screen.getByRole('alert')).toHaveTextContent('restart login to continue');
    expect(screen.queryByLabelText('Recovery code')).not.toBeInTheDocument();
  });

  it('手动返回会清理挑战、验证码与恢复码模式', async () => {
    const onLogin = vi.fn().mockResolvedValueOnce(challenge).mockResolvedValueOnce({
      status: 'two_factor_required',
      challengeToken: 'challenge-2',
      expiresIn: 300,
    });
    const onVerifyTwoFactor = vi.fn().mockResolvedValue(undefined);
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);
    await submitPassword(onLogin);
    await userEvent.click(await screen.findByRole('button', { name: 'Use a recovery code' }));
    await userEvent.type(screen.getByLabelText('Recovery code'), 'OLD-CODE');
    await userEvent.click(screen.getByRole('button', { name: 'Back to login' }));

    expect(screen.getByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toHaveValue('');
    await userEvent.type(screen.getByLabelText('Password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Login' }));
    expect(await screen.findByLabelText('Authenticator code')).toHaveValue('');
    expect(screen.queryByLabelText('Recovery code')).not.toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Authenticator code'), '654321');
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(onVerifyTwoFactor).toHaveBeenCalledWith('challenge-2', '654321');
  });

  it('提交期间禁用表单并阻止重复提交', async () => {
    let resolveLogin!: (result: LoginResult) => void;
    const onLogin = vi.fn().mockReturnValue(
      new Promise<LoginResult>((resolve) => {
        resolveLogin = resolve;
      })
    );
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={vi.fn()} />);
    await userEvent.type(screen.getByLabelText('Password'), 'secret');
    const button = screen.getByRole('button', { name: 'Login' });
    await userEvent.click(button);

    expect(button).toBeDisabled();
    await userEvent.click(button);
    expect(onLogin).toHaveBeenCalledTimes(1);
    resolveLogin(challenge);
    expect(await screen.findByRole('heading', { name: 'Two-factor authentication' })).toBeInTheDocument();
  });

  it('验证码为空或仅空白时不允许提交', async () => {
    const onLogin = vi.fn().mockResolvedValue(challenge);
    const onVerifyTwoFactor = vi.fn();
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);
    await submitPassword(onLogin);

    const verify = await screen.findByRole('button', { name: 'Verify' });
    expect(verify).toBeDisabled();
    await userEvent.type(screen.getByLabelText('Authenticator code'), '   ');
    expect(verify).toBeDisabled();
    expect(onVerifyTwoFactor).not.toHaveBeenCalled();
  });

  it('在当前步骤显示普通错误且允许重试', async () => {
    const onLogin = vi.fn().mockRejectedValueOnce(new APIError(401, 'invalid_credentials', 'bad credentials'));
    const onVerifyTwoFactor = vi.fn();
    render(<LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />);
    await submitPassword(onLogin);

    expect(screen.getByRole('alert')).toHaveTextContent('bad credentials');
    expect(screen.getByRole('button', { name: 'Login' })).not.toBeDisabled();
  });
});
