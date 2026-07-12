import { Check, Clipboard, KeyRound, RefreshCw, ShieldX, Trash2 } from 'lucide-react';
import { FormEvent, useEffect, useMemo, useState } from 'react';
import type { AgentToken, CreatedAgentToken } from '../api';
import { useT } from '../i18n';
import type { TranslationKey } from '../i18n';
import type { AgentTokenErrorKind } from '../App';

type TokenStatus = 'available' | 'used' | 'expired' | 'revoked';

// 错误类别与令牌状态到翻译 key 的映射
const tokenErrorKeys: Record<AgentTokenErrorKind, TranslationKey> = {
  load: 'tokens.errLoad',
  create: 'tokens.errCreate',
  revoke: 'tokens.errRevoke',
  delete: 'tokens.errDelete',
};

const tokenStatusKeys: Record<TokenStatus, TranslationKey> = {
  available: 'tokens.statusAvailable',
  used: 'tokens.statusUsed',
  expired: 'tokens.statusExpired',
  revoked: 'tokens.statusRevoked',
};

function getTokenStatus(token: AgentToken, now = new Date()): TokenStatus {
  if (token.revoked_at) return 'revoked';
  if (token.used_at) return 'used';
  if (new Date(token.expires_at).getTime() <= now.getTime()) return 'expired';
  return 'available';
}

function formatDate(value?: string) {
  if (!value) return '-';
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

export function AgentTokenManager({
  tokens,
  loading,
  error,
  createdToken,
  onCreate,
  onRevoke,
  onDelete,
  onRefresh,
}: {
  tokens: AgentToken[];
  loading: boolean;
  error: AgentTokenErrorKind | null;
  createdToken: CreatedAgentToken | null;
  onCreate: (name: string, ttlHours: number) => Promise<void>;
  onRevoke: (id: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const { t } = useT();
  const [name, setName] = useState('agent');
  const [ttlHours, setTtlHours] = useState('24');
  const [submitting, setSubmitting] = useState(false);
  const [copyTokenState, setCopyTokenState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [copyCommandState, setCopyCommandState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [pendingRevokeId, setPendingRevokeId] = useState<string | null>(null);
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
  const registerCommand = createdToken
    ? `vibe-agent register --server ${window.location.origin} --token ${createdToken.token}`
    : '';
  const runCommand = 'vibe-agent run';
  const agentCommand = `${registerCommand}\n${runCommand}`;
  const sortedTokens = useMemo(
    () => [...tokens].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()),
    [tokens]
  );

  useEffect(() => {
    onRefresh();
  }, [onRefresh]);

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    try {
      await onCreate(name.trim() || 'agent', Math.max(1, Number(ttlHours) || 1));
      setName('agent');
      setTtlHours('24');
      setCopyTokenState('idle');
      setCopyCommandState('idle');
    } catch {
      return;
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRevoke(id: string) {
    try {
      await onRevoke(id);
      setPendingRevokeId(null);
    } catch {
      return;
    }
  }

  async function handleDelete(id: string) {
    try {
      await onDelete(id);
      setPendingDeleteId(null);
    } catch {
      return;
    }
  }

  async function copyToken() {
    if (!createdToken) return;
    try {
      await navigator.clipboard.writeText(createdToken.token);
      setCopyTokenState('copied');
    } catch {
      setCopyTokenState('failed');
    }
  }

  async function copyAgentCommand() {
    if (!createdToken) return;
    try {
      await navigator.clipboard.writeText(agentCommand);
      setCopyCommandState('copied');
    } catch {
      setCopyCommandState('failed');
    }
  }

  return (
    <main className="tokenPage">
      <header className="tokenHeader">
        <div>
          <h1>{t('tokens.title')}</h1>
        </div>
        <button type="button" className="secondaryButton" onClick={onRefresh} disabled={loading}>
          <RefreshCw size={16} aria-hidden="true" />
          {t('common.refresh')}
        </button>
      </header>

      <section className="tokenPanel createTokenPanel" aria-labelledby="create-token-title">
        <div className="panelTitleRow">
          <h2 id="create-token-title">{t('tokens.create')}</h2>
        </div>
        <form className="tokenForm" onSubmit={handleCreate}>
          <label>
            <span>{t('tokens.name')}</span>
            <input value={name} placeholder="office-mac" onChange={(event) => setName(event.target.value)} />
          </label>
          <label>
            <span>{t('tokens.ttl')}</span>
            <input
              type="number"
              min={1}
              step={1}
              value={ttlHours}
              onChange={(event) => setTtlHours(event.target.value)}
            />
          </label>
          <button type="submit" className="primaryButton tokenSubmitButton" disabled={submitting}>
            <KeyRound size={16} aria-hidden="true" />
            {t('tokens.createButton')}
          </button>
        </form>
        {createdToken && (
          <div className="newToken" role="status">
            <div className="newTokenSummary">
              <span>{t('tokens.name')}</span>
              <strong>{createdToken.name}</strong>
            </div>
            <div className="newTokenGrid">
              <div className="newTokenBlock">
                <div className="newTokenBlockHeader">
                  <span>{t('tokens.token')}</span>
                  <button type="button" className="iconTextButton" onClick={copyToken}>
                    {copyTokenState === 'copied' ? (
                      <Check size={16} aria-hidden="true" />
                    ) : (
                      <Clipboard size={16} aria-hidden="true" />
                    )}
                    {copyTokenState === 'copied' ? t('common.copied') : t('common.copy')}
                  </button>
                </div>
                <code className="tokenValue">{createdToken.token}</code>
                {copyTokenState === 'failed' && <span className="error">{t('common.copyFailed')}</span>}
              </div>
              <div className="newTokenBlock">
                <div className="newTokenBlockHeader">
                  <span>{t('tokens.agentCommand')}</span>
                  <button type="button" className="iconTextButton" onClick={copyAgentCommand}>
                    {copyCommandState === 'copied' ? (
                      <Check size={16} aria-hidden="true" />
                    ) : (
                      <Clipboard size={16} aria-hidden="true" />
                    )}
                    {copyCommandState === 'copied' ? t('common.copied') : t('common.copy')}
                  </button>
                </div>
                <pre className="agentCommand">
                  <code>{registerCommand}</code>
                  <code>{runCommand}</code>
                </pre>
                {copyCommandState === 'failed' && <span className="error">{t('common.copyFailed')}</span>}
              </div>
            </div>
          </div>
        )}
      </section>

      <section className="tokenPanel" aria-labelledby="token-list-title">
        <div className="panelTitleRow">
          <h2 id="token-list-title">{t('tokens.list')}</h2>
          {loading && <span className="muted">{t('common.loading')}</span>}
        </div>
        {error && <p className="error">{t(tokenErrorKeys[error])}</p>}
        {!loading && sortedTokens.length === 0 ? (
          <p className="emptyState">{t('tokens.empty')}</p>
        ) : (
          <div className="tokenTableWrap">
            <table className="tokenTable">
              <thead>
                <tr>
                  <th>{t('tokens.colName')}</th>
                  <th>{t('tokens.colStatus')}</th>
                  <th>{t('tokens.colCreated')}</th>
                  <th>{t('tokens.colExpires')}</th>
                  <th>{t('tokens.colUsed')}</th>
                  <th>{t('tokens.colRevoked')}</th>
                  <th>{t('tokens.colAction')}</th>
                </tr>
              </thead>
              <tbody>
                {sortedTokens.map((token) => {
                  const status = getTokenStatus(token);
                  const confirming = pendingRevokeId === token.id;
                  const confirmingDelete = pendingDeleteId === token.id;
                  return (
                    <tr key={token.id}>
                      <td>
                        <strong>{token.name}</strong>
                        <span className="tokenId">{token.id.slice(0, 8)}</span>
                      </td>
                      <td>
                        <span className={`tokenStatus tokenStatus-${status}`}>{t(tokenStatusKeys[status])}</span>
                      </td>
                      <td>{formatDate(token.created_at)}</td>
                      <td>{formatDate(token.expires_at)}</td>
                      <td>{formatDate(token.used_at)}</td>
                      <td>{formatDate(token.revoked_at)}</td>
                      <td>
                        {status === 'revoked' && confirmingDelete ? (
                          <button type="button" className="dangerButton" onClick={() => handleDelete(token.id)}>
                            <Trash2 size={16} aria-hidden="true" />
                            {t('tokens.confirmDelete')}
                          </button>
                        ) : status === 'revoked' ? (
                          <button type="button" className="iconTextButton" onClick={() => setPendingDeleteId(token.id)}>
                            <Trash2 size={16} aria-hidden="true" />
                            {t('common.delete')}
                          </button>
                        ) : confirming ? (
                          <button type="button" className="dangerButton" onClick={() => handleRevoke(token.id)}>
                            <ShieldX size={16} aria-hidden="true" />
                            {t('common.confirm')}
                          </button>
                        ) : (
                          <button type="button" className="iconTextButton" onClick={() => setPendingRevokeId(token.id)}>
                            <Trash2 size={16} aria-hidden="true" />
                            {t('tokens.revoke')}
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </main>
  );
}
