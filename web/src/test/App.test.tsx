import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { AppView } from '../App';

describe('AppView', () => {
  it('shows login when there is no user', () => {
    render(<AppView user={null} devices={[]} sessions={{}} onLogin={vi.fn()} onCreateSession={vi.fn()} />);
    expect(screen.getByRole('button', { name: /login/i })).toBeInTheDocument();
  });

  it('opens multiple terminal tabs for an online device', async () => {
    const createSession = vi.fn()
      .mockResolvedValueOnce({ id: 'sess-1', title: 'bash', status: 'running' })
      .mockResolvedValueOnce({ id: 'sess-2', title: 'bash', status: 'running' });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{}}
        onLogin={vi.fn()}
        onCreateSession={createSession}
      />
    );
    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
    await userEvent.click(screen.getByRole('button', { name: /new terminal/i }));
    expect(await screen.findByRole('tab', { name: /sess-1/i })).toBeInTheDocument();
    expect(await screen.findByRole('tab', { name: /sess-2/i })).toBeInTheDocument();
  });
});
