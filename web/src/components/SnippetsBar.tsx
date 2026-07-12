import type { FormEvent } from 'react';
import { useEffect, useState } from 'react';
import { ChevronDown, Pencil, Plus, Trash2, Zap } from 'lucide-react';
import type { Snippet } from '../api';
import * as api from '../api';
import { useT } from '../i18n';
import type { TranslationKey } from '../i18n';

export function SnippetsBar({ onInsert }: { onInsert: (command: string) => void }) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [snippets, setSnippets] = useState<Snippet[]>([]);
  const [error, setError] = useState<TranslationKey | null>(null);
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
        if (!cancelled) setError('snippets.errLoad');
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
      setError('snippets.errSave');
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
      setError('snippets.errDelete');
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
        <span>{t('snippets.toggle')}</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <div className="snippetsPopover">
          {error && (
            <div className="snippetsError" role="alert">
              {t(error)}
            </div>
          )}
          {!error && snippets.length === 0 && <div className="snippetsEmpty">{t('snippets.empty')}</div>}
          <ul className="snippetsList">
            {snippets.map((snippet) => (
              <li key={snippet.id} className="snippetRow">
                <button
                  className="snippetInsert"
                  type="button"
                  aria-label={t('snippets.insert', { name: snippet.name })}
                  title={snippet.command}
                  onClick={() => insert(snippet)}
                >
                  <strong>{snippet.name}</strong>
                  <code>{snippet.command}</code>
                </button>
                <button
                  className="iconButton"
                  type="button"
                  aria-label={t('snippets.edit', { name: snippet.name })}
                  onClick={() => startEdit(snippet)}
                >
                  <Pencil aria-hidden="true" size={13} />
                </button>
                <button
                  className="iconButton danger"
                  type="button"
                  aria-label={t('snippets.delete', { name: snippet.name })}
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
              placeholder={t('snippets.namePlaceholder')}
              aria-label={t('snippets.nameLabel')}
              disabled={pending}
              onChange={(event) => setDraftName(event.target.value)}
            />
            <input
              value={draftCommand}
              placeholder={t('snippets.commandPlaceholder')}
              aria-label={t('snippets.commandLabel')}
              disabled={pending}
              onChange={(event) => setDraftCommand(event.target.value)}
            />
            <button type="submit" disabled={pending || !draftName.trim() || !draftCommand.trim()}>
              <Plus size={14} aria-hidden="true" />
              {editing ? t('common.save') : t('common.add')}
            </button>
            {editing && (
              <button type="button" disabled={pending} onClick={resetForm}>
                {t('common.cancel')}
              </button>
            )}
          </form>
          <p className="snippetsHint">{t('snippets.hint')}</p>
        </div>
      )}
    </div>
  );
}
