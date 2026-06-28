import { Check, Clipboard, KeyRound, RefreshCw, ShieldX, Trash2 } from 'lucide-react';
import { FormEvent, useEffect, useMemo, useState } from 'react';
import type { AgentToken, CreatedAgentToken } from '../api';

type TokenStatus = 'available' | 'used' | 'expired' | 'revoked';

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
  onRefresh,
}: {
  tokens: AgentToken[];
  loading: boolean;
  error: string | null;
  createdToken: CreatedAgentToken | null;
  onCreate: (name: string, ttlHours: number) => Promise<void>;
  onRevoke: (id: string) => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const [name, setName] = useState('agent');
  const [ttlHours, setTtlHours] = useState('24');
  const [submitting, setSubmitting] = useState(false);
  const [copyTokenState, setCopyTokenState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [copyCommandState, setCopyCommandState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [pendingRevokeId, setPendingRevokeId] = useState<string | null>(null);
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
          <h1>Agent Tokens</h1>
        </div>
        <button type="button" className="secondaryButton" onClick={onRefresh} disabled={loading}>
          <RefreshCw size={16} aria-hidden="true" />
          Refresh
        </button>
      </header>

      <section className="tokenPanel" aria-labelledby="create-token-title">
        <h2 id="create-token-title">Create token</h2>
        <form className="tokenForm" onSubmit={handleCreate}>
          <label>
            Name
            <input value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label>
            TTL hours
            <input
              type="number"
              min={1}
              step={1}
              value={ttlHours}
              onChange={(event) => setTtlHours(event.target.value)}
            />
          </label>
          <button type="submit" disabled={submitting}>
            <KeyRound size={16} aria-hidden="true" />
            Create
          </button>
        </form>
        {createdToken && (
          <div className="newToken" role="status">
            <div className="newTokenItem">
              <span>Token</span>
              <code>{createdToken.token}</code>
              <button type="button" className="iconTextButton" onClick={copyToken}>
                {copyTokenState === 'copied' ? (
                  <Check size={16} aria-hidden="true" />
                ) : (
                  <Clipboard size={16} aria-hidden="true" />
                )}
                {copyTokenState === 'copied' ? 'Copied' : 'Copy token'}
              </button>
              {copyTokenState === 'failed' && <span className="error">Copy failed</span>}
            </div>
            <div className="newTokenItem">
              <span>Agent command</span>
              <pre className="agentCommand">
                <code>{registerCommand}</code>
                <code>{runCommand}</code>
              </pre>
              <button type="button" className="iconTextButton" onClick={copyAgentCommand}>
                {copyCommandState === 'copied' ? (
                  <Check size={16} aria-hidden="true" />
                ) : (
                  <Clipboard size={16} aria-hidden="true" />
                )}
                {copyCommandState === 'copied' ? 'Copied' : 'Copy command'}
              </button>
              {copyCommandState === 'failed' && <span className="error">Copy failed</span>}
            </div>
          </div>
        )}
      </section>

      <section className="tokenPanel" aria-labelledby="token-list-title">
        <div className="panelTitleRow">
          <h2 id="token-list-title">Tokens</h2>
          {loading && <span className="muted">Loading...</span>}
        </div>
        {error && <p className="error">{error}</p>}
        {!loading && sortedTokens.length === 0 ? (
          <p className="emptyState">No agent tokens yet.</p>
        ) : (
          <div className="tokenTableWrap">
            <table className="tokenTable">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th>Expires</th>
                  <th>Used</th>
                  <th>Revoked</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {sortedTokens.map((token) => {
                  const status = getTokenStatus(token);
                  const confirming = pendingRevokeId === token.id;
                  return (
                    <tr key={token.id}>
                      <td>
                        <strong>{token.name}</strong>
                        <span className="tokenId">{token.id.slice(0, 8)}</span>
                      </td>
                      <td>
                        <span className={`tokenStatus tokenStatus-${status}`}>{status}</span>
                      </td>
                      <td>{formatDate(token.created_at)}</td>
                      <td>{formatDate(token.expires_at)}</td>
                      <td>{formatDate(token.used_at)}</td>
                      <td>{formatDate(token.revoked_at)}</td>
                      <td>
                        {status === 'revoked' ? (
                          <span className="muted">Revoked</span>
                        ) : confirming ? (
                          <button type="button" className="dangerButton" onClick={() => handleRevoke(token.id)}>
                            <ShieldX size={16} aria-hidden="true" />
                            Confirm
                          </button>
                        ) : (
                          <button type="button" className="iconTextButton" onClick={() => setPendingRevokeId(token.id)}>
                            <Trash2 size={16} aria-hidden="true" />
                            Revoke
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
