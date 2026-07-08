import type { FormEvent } from 'react';
import { useEffect, useState } from 'react';
import { ChevronDown, Pencil, Plus, Trash2, Zap } from 'lucide-react';
import type { Snippet } from '../api';
import * as api from '../api';

export function SnippetsBar({ onInsert }: { onInsert: (command: string) => void }) {
  const [open, setOpen] = useState(false);
  const [snippets, setSnippets] = useState<Snippet[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Snippet | null>(null);
  const [draftName, setDraftName] = useState('');
  const [draftCommand, setDraftCommand] = useState('');
  const [pending, setPending] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .listSnippets()
      .then((items) => {
        if (!cancelled) {
          setSnippets(items ?? []);
          setError(null);
        }
      })
      .catch(() => {
        if (!cancelled) setError('Failed to load snippets.');
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  function insert(snippet: Snippet) {
    onInsert(snippet.command);
    setOpen(false);
  }

  function startEdit(snippet: Snippet) {
    setEditing(snippet);
    setDraftName(snippet.name);
    setDraftCommand(snippet.command);
  }

  function resetForm() {
    setEditing(null);
    setDraftName('');
    setDraftCommand('');
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = draftName.trim();
    if (!name || !draftCommand.trim()) return;
    setPending(true);
    setError(null);
    try {
      if (editing) {
        const updated = await api.updateSnippet(editing.id, name, draftCommand);
        setSnippets((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      } else {
        const created = await api.createSnippet(name, draftCommand);
        setSnippets((current) => [...current, created]);
      }
      resetForm();
    } catch {
      setError('Failed to save snippet.');
    } finally {
      setPending(false);
    }
  }

  async function remove(snippet: Snippet) {
    setError(null);
    try {
      await api.deleteSnippet(snippet.id);
      setSnippets((current) => current.filter((item) => item.id !== snippet.id));
    } catch {
      setError('Failed to delete snippet.');
    }
  }

  return (
    <div className="snippetsBar">
      <button
        className="snippetsToggle"
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <Zap size={14} aria-hidden="true" />
        <span>Quick commands</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <div className="snippetsPopover">
          {error && (
            <div className="snippetsError" role="alert">
              {error}
            </div>
          )}
          {!error && snippets.length === 0 && <div className="snippetsEmpty">No snippets yet</div>}
          <ul className="snippetsList">
            {snippets.map((snippet) => (
              <li key={snippet.id} className="snippetRow">
                <button
                  className="snippetInsert"
                  type="button"
                  aria-label={`Insert ${snippet.name}`}
                  title={snippet.command}
                  onClick={() => insert(snippet)}
                >
                  <strong>{snippet.name}</strong>
                  <code>{snippet.command}</code>
                </button>
                <button
                  className="iconButton"
                  type="button"
                  aria-label={`Edit ${snippet.name}`}
                  onClick={() => startEdit(snippet)}
                >
                  <Pencil aria-hidden="true" size={13} />
                </button>
                <button
                  className="iconButton danger"
                  type="button"
                  aria-label={`Delete ${snippet.name}`}
                  onClick={() => void remove(snippet)}
                >
                  <Trash2 aria-hidden="true" size={13} />
                </button>
              </li>
            ))}
          </ul>
          <form className="snippetForm" onSubmit={submit}>
            <input
              value={draftName}
              placeholder="Name"
              aria-label="Snippet name"
              disabled={pending}
              onChange={(event) => setDraftName(event.target.value)}
            />
            <input
              value={draftCommand}
              placeholder="Command"
              aria-label="Snippet command"
              disabled={pending}
              onChange={(event) => setDraftCommand(event.target.value)}
            />
            <button type="submit" disabled={pending || !draftName.trim() || !draftCommand.trim()}>
              <Plus size={14} aria-hidden="true" />
              {editing ? 'Save' : 'Add'}
            </button>
            {editing && (
              <button type="button" disabled={pending} onClick={resetForm}>
                Cancel
              </button>
            )}
          </form>
          <p className="snippetsHint">Click a snippet to type it into the active terminal, then press Enter yourself.</p>
        </div>
      )}
    </div>
  );
}
