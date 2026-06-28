import { KeyRound, Monitor } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { AgentToken, CreatedAgentToken, Device, Session, User } from './api';
import * as api from './api';
import { AgentTokenManager } from './components/AgentTokenManager';
import { DeviceList } from './components/DeviceList';
import { LoginView } from './components/LoginView';
import { TerminalTabs } from './components/TerminalTabs';

type SessionsByDevice = Record<string, Session[]>;
type ViewMode = 'terminals' | 'agentTokens';

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [sessions, setSessions] = useState<SessionsByDevice>({});
  const [agentTokens, setAgentTokens] = useState<AgentToken[]>([]);
  const [createdAgentToken, setCreatedAgentToken] = useState<CreatedAgentToken | null>(null);
  const [tokenLoading, setTokenLoading] = useState(false);
  const [tokenError, setTokenError] = useState<string | null>(null);

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null));
  }, []);

  useEffect(() => {
    if (!user) {
      setDevices([]);
      setSessions({});
      setAgentTokens([]);
      setCreatedAgentToken(null);
      setTokenError(null);
      return;
    }
    let cancelled = false;
    async function loadDevicesAndSessions() {
      try {
        const nextDevices = await api.listDevices();
        const entries = await Promise.all(
          nextDevices.map(async (device) => [device.id, await api.listSessions(device.id)] as const)
        );
        if (!cancelled) {
          setDevices(nextDevices);
          setSessions(Object.fromEntries(entries));
        }
      } catch {
        if (!cancelled) {
          setDevices([]);
          setSessions({});
        }
      }
    }
    loadDevicesAndSessions();
    return () => {
      cancelled = true;
    };
  }, [user]);

  async function handleLogin(username: string, password: string) {
    const loggedIn = await api.login(username, password);
    setUser(loggedIn);
  }

  async function handleCreateSession(deviceId: string) {
    return api.createSession(deviceId);
  }

  async function handleCloseSession(sessionId: string) {
    await api.closeSession(sessionId);
  }

  async function handleRenameSession(sessionId: string, title: string) {
    return api.renameSession(sessionId, title);
  }

  const loadAgentTokens = useCallback(async () => {
    setTokenLoading(true);
    setTokenError(null);
    try {
      setAgentTokens(await api.listAgentTokens());
    } catch {
      setTokenError('Failed to load agent tokens.');
    } finally {
      setTokenLoading(false);
    }
  }, []);

  async function handleCreateAgentToken(name: string, ttlHours: number) {
    setTokenError(null);
    try {
      const created = await api.createAgentToken(name, ttlHours);
      setCreatedAgentToken(created);
      setAgentTokens((current) => [created, ...current.filter((token) => token.id !== created.id)]);
    } catch (error) {
      setTokenError('Failed to create agent token.');
      throw error;
    }
  }

  async function handleRevokeAgentToken(id: string) {
    setTokenError(null);
    try {
      const revoked = await api.revokeAgentToken(id);
      setAgentTokens((current) => current.map((token) => (token.id === id ? revoked : token)));
    } catch (error) {
      setTokenError('Failed to revoke agent token.');
      throw error;
    }
  }

  return (
    <AppView
      user={user}
      devices={devices}
      sessions={sessions}
      onLogin={handleLogin}
      onCloseSession={handleCloseSession}
      onCreateSession={handleCreateSession}
      onRenameSession={handleRenameSession}
      agentTokens={agentTokens}
      createdAgentToken={createdAgentToken}
      tokenLoading={tokenLoading}
      tokenError={tokenError}
      onCreateAgentToken={handleCreateAgentToken}
      onRevokeAgentToken={handleRevokeAgentToken}
      onRefreshAgentTokens={loadAgentTokens}
    />
  );
}

export function AppView({
  user,
  devices,
  sessions,
  onLogin,
  onCloseSession,
  onCreateSession,
  onRenameSession,
  agentTokens,
  createdAgentToken,
  tokenLoading,
  tokenError,
  onCreateAgentToken,
  onRevokeAgentToken,
  onRefreshAgentTokens,
}: {
  user: User | null;
  devices: Device[];
  sessions: SessionsByDevice;
  onLogin: (username: string, password: string) => Promise<void>;
  onCloseSession: (sessionId: string) => Promise<void>;
  onCreateSession: (deviceId: string) => Promise<Session | void>;
  onRenameSession: (sessionId: string, title: string) => Promise<Session | void>;
  agentTokens: AgentToken[];
  createdAgentToken: CreatedAgentToken | null;
  tokenLoading: boolean;
  tokenError: string | null;
  onCreateAgentToken: (name: string, ttlHours: number) => Promise<void>;
  onRevokeAgentToken: (id: string) => Promise<void>;
  onRefreshAgentTokens: () => Promise<void>;
}) {
  const initialSessions = useMemo(() => Object.values(sessions).flat(), [sessions]);
  const [localSessions, setLocalSessions] = useState<Session[]>(initialSessions);
  const [viewMode, setViewMode] = useState<ViewMode>('terminals');

  useEffect(() => {
    setLocalSessions(initialSessions);
  }, [initialSessions]);

  async function createAndAppend(deviceId: string) {
    const session = await onCreateSession(deviceId);
    if (session) {
      setLocalSessions((current) => [...current, session]);
    }
  }

  if (!user) return <LoginView onLogin={onLogin} />;
  return (
    <div className="shell">
      <aside className="devices">
        <nav className="sideNav" aria-label="Primary">
          <button className={viewMode === 'terminals' ? 'active' : ''} onClick={() => setViewMode('terminals')}>
            <Monitor size={16} aria-hidden="true" />
            Terminals
          </button>
          <button className={viewMode === 'agentTokens' ? 'active' : ''} onClick={() => setViewMode('agentTokens')}>
            <KeyRound size={16} aria-hidden="true" />
            Agent Tokens
          </button>
        </nav>
        <DeviceList devices={devices} onCreateSession={createAndAppend} compact />
      </aside>
      {viewMode === 'terminals' ? (
        <TerminalTabs
          sessions={localSessions}
          onSessionsChange={setLocalSessions}
          onCloseSession={onCloseSession}
          onRenameSession={onRenameSession}
        />
      ) : (
        <AgentTokenManager
          tokens={agentTokens}
          loading={tokenLoading}
          error={tokenError}
          createdToken={createdAgentToken}
          onCreate={onCreateAgentToken}
          onRevoke={onRevokeAgentToken}
          onRefresh={onRefreshAgentTokens}
        />
      )}
    </div>
  );
}
