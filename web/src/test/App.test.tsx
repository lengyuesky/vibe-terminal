import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App, AppView } from '../App';
import * as api from '../api';

vi.mock('../api', () => ({
  closeSession: vi.fn(),
  createSession: vi.fn(),
  listDevices: vi.fn(),
  listSessionOutput: vi.fn(),
  listSessions: vi.fn(),
  login: vi.fn(),
  me: vi.fn(),
  renameSession: vi.fn(),
}));

const mockedApi = vi.mocked(api);

beforeEach(() => {
  vi.resetAllMocks();
});

describe('AppView', () => {
  it('shows login when there is no user', () => {
    render(
      <AppView
        user={null}
        devices={[]}
        sessions={{}}
        onLogin={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
      />
    );
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument();
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
        onCloseSession={vi.fn()}
        onCreateSession={createSession}
        onRenameSession={vi.fn()}
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

    expect(await screen.findByRole('tab', { name: /sess-res/i })).toBeInTheDocument();
    expect(await screen.findByText(/\/srv\/app/)).toBeInTheDocument();
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
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
      />
    );

    expect(screen.getByText('/work/app')).toBeInTheDocument();
    expect(screen.getByText('/work/agent')).toBeInTheDocument();
    expect(screen.getByText('running')).toHaveClass('statusRunning');
    expect(screen.getByText('lost')).toHaveClass('statusLost');
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
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
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
        onCloseSession={closeSession}
        onCreateSession={vi.fn()}
        onRenameSession={renameSession}
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
});
