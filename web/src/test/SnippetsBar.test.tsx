import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { SnippetsBar } from '../components/SnippetsBar';

vi.mock('../api', () => ({
  listSnippets: vi.fn(),
  createSnippet: vi.fn(),
  updateSnippet: vi.fn(),
  deleteSnippet: vi.fn(),
}));

const mockedApi = vi.mocked(api);
const snippet = {
  id: 'snip-1',
  name: 'disk',
  command: 'df -h',
  created_at: '2026-07-02T00:00:00Z',
  updated_at: '2026-07-02T00:00:00Z',
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SnippetsBar', () => {
  it('loads snippets when opened and inserts on click', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    const onInsert = vi.fn();
    render(<SnippetsBar onInsert={onInsert} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /insert disk/i }));
    expect(onInsert).toHaveBeenCalledWith('df -h');
    expect(screen.queryByRole('button', { name: /insert disk/i })).not.toBeInTheDocument();
  });

  it('clears the load error after a successful reload', async () => {
    mockedApi.listSnippets.mockRejectedValueOnce(new Error('network down'));
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    render(<SnippetsBar onInsert={vi.fn()} />);
    const toggle = screen.getByRole('button', { name: /quick commands/i });
    await userEvent.click(toggle);
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load snippets.');
    await userEvent.click(toggle);
    await userEvent.click(toggle);
    expect(await screen.findByRole('button', { name: /insert disk/i })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('creates a snippet from the form', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([]);
    mockedApi.createSnippet.mockResolvedValueOnce({ ...snippet, id: 'snip-2', name: 'uptime', command: 'uptime' });
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.type(await screen.findByLabelText('Snippet name'), 'uptime');
    await userEvent.type(screen.getByLabelText('Snippet command'), 'uptime');
    await userEvent.click(screen.getByRole('button', { name: /^add$/i }));
    await waitFor(() => expect(mockedApi.createSnippet).toHaveBeenCalledWith('uptime', 'uptime'));
    expect(await screen.findByRole('button', { name: /insert uptime/i })).toBeInTheDocument();
  });

  it('deletes a snippet', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    mockedApi.deleteSnippet.mockResolvedValueOnce(undefined);
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /delete disk/i }));
    await waitFor(() => expect(mockedApi.deleteSnippet).toHaveBeenCalledWith('snip-1'));
    expect(screen.queryByRole('button', { name: /insert disk/i })).not.toBeInTheDocument();
  });

  it('edits a snippet', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    mockedApi.updateSnippet.mockResolvedValueOnce({ ...snippet, name: 'disk usage', command: 'df -h /' });
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /edit disk/i }));
    const nameInput = screen.getByLabelText('Snippet name') as HTMLInputElement;
    expect(nameInput.value).toBe('disk');
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, 'disk usage');
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(mockedApi.updateSnippet).toHaveBeenCalledWith('snip-1', 'disk usage', 'df -h'));
  });
});
