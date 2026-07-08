import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { TerminalSearchBar } from '../components/TerminalSearchBar';

describe('TerminalSearchBar', () => {
  it('searches forward on Enter and backward on Shift+Enter', async () => {
    const onSearch = vi.fn();
    render(<TerminalSearchBar onSearch={onSearch} onClose={vi.fn()} />);
    const input = screen.getByLabelText('Search terminal');
    await userEvent.type(input, 'error{Enter}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'error', caseSensitive: false }, 'next');
    await userEvent.type(input, '{Shift>}{Enter}{/Shift}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'error', caseSensitive: false }, 'previous');
  });

  it('toggles case sensitivity', async () => {
    const onSearch = vi.fn();
    render(<TerminalSearchBar onSearch={onSearch} onClose={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /match case/i }));
    await userEvent.type(screen.getByLabelText('Search terminal'), 'X{Enter}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'X', caseSensitive: true }, 'next');
  });

  it('closes on Escape and close button', async () => {
    const onClose = vi.fn();
    render(<TerminalSearchBar onSearch={vi.fn()} onClose={onClose} />);
    await userEvent.type(screen.getByLabelText('Search terminal'), '{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: /close search/i }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
