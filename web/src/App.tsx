import { KeyRound, Monitor, Terminal } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { AgentToken, CreatedAgentToken, Device, LoginResult, Session, User } from './api';
import * as api from './api';
import { AgentTokenManager } from './components/AgentTokenManager';
import { DeviceList } from './components/DeviceList';
import { FileManagerPanel } from './components/FileManagerPanel';
import { LoginView } from './components/LoginView';
import { TerminalTabs } from './components/TerminalTabs';

type SessionsByDevice = Record<string, Session[]>;
type ViewMode = 'terminals' | 'agentTokens';

function enrichSessionDevice(session: Session, deviceId: string, device?: Device): Session {
  return {
    ...session,
    device_id: session.device_id ?? deviceId,
    device_name: device?.name ?? session.device_name,
    device_platform: device?.platform ?? session.device_platform,
  };
}

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [sessions, setSessions] = useState<SessionsByDevice>({});
  const [agentTokens, setAgentTokens] = useState<AgentToken[]>([]);
  const [createdAgentToken, setCreatedAgentToken] = useState<CreatedAgentToken | null>(null);
  const [tokenLoading, setTokenLoading] = useState(false);
  const [tokenError, setTokenError] = useState<string | null>(null);
  const mountedRef = useRef(true);
  const authGenerationRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    const generation = ++authGenerationRef.current;
    api.me().then(
      (bootstrapUser) => {
        if (mountedRef.current && generation === authGenerationRef.current) setUser(bootstrapUser);
      },
      () => {
        if (mountedRef.current && generation === authGenerationRef.current) setUser(null);
      }
    );
    return () => {
      mountedRef.current = false;
      authGenerationRef.current += 1;
    };
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

  async function handleLogin(username: string, password: string): Promise<LoginResult> {
    const generation = ++authGenerationRef.current;
    const result = await api.login(username, password);
    if (mountedRef.current && generation === authGenerationRef.current && result.status === 'authenticated') {
      setUser(result.user);
    }
    return result;
  }

  async function handleVerifyTwoFactor(challengeToken: string, code: string) {
    const generation = ++authGenerationRef.current;
    const verifiedUser = await api.verifyTwoFactor(challengeToken, code);
    if (mountedRef.current && generation === authGenerationRef.current) setUser(verifiedUser);
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

  async function handleRenameDevice(deviceId: string, name: string) {
    const updated = await api.renameDevice(deviceId, name);
    setDevices((current) => current.map((device) => (device.id === deviceId ? updated : device)));
    return updated;
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

  async function handleDeleteAgentToken(id: string) {
    setTokenError(null);
    try {
      await api.deleteAgentToken(id);
      setAgentTokens((current) => current.filter((token) => token.id !== id));
      setCreatedAgentToken((current) => (current?.id === id ? null : current));
    } catch (error) {
      setTokenError('Failed to delete agent token.');
      throw error;
    }
  }

  return (
    <AppView
      user={user}
      devices={devices}
      sessions={sessions}
      onLogin={handleLogin}
      onVerifyTwoFactor={handleVerifyTwoFactor}
      onCloseSession={handleCloseSession}
      onCreateSession={handleCreateSession}
      onRenameDevice={handleRenameDevice}
      onRenameSession={handleRenameSession}
      agentTokens={agentTokens}
      createdAgentToken={createdAgentToken}
      tokenLoading={tokenLoading}
      tokenError={tokenError}
      onCreateAgentToken={handleCreateAgentToken}
      onRevokeAgentToken={handleRevokeAgentToken}
      onDeleteAgentToken={handleDeleteAgentToken}
      onRefreshAgentTokens={loadAgentTokens}
    />
  );
}

export function AppView({
  user,
  devices,
  sessions,
  onLogin,
  onVerifyTwoFactor,
  onCloseSession,
  onCreateSession,
  onRenameDevice = async () => undefined,
  onRenameSession,
  agentTokens,
  createdAgentToken,
  tokenLoading,
  tokenError,
  onCreateAgentToken,
  onRevokeAgentToken,
  onDeleteAgentToken = async () => {},
  onRefreshAgentTokens,
}: {
  user: User | null;
  devices: Device[];
  sessions: SessionsByDevice;
  onLogin: (username: string, password: string) => Promise<LoginResult>;
  onVerifyTwoFactor: (challengeToken: string, code: string) => Promise<void>;
  onCloseSession: (sessionId: string) => Promise<void>;
  onCreateSession: (deviceId: string) => Promise<Session | void>;
  onRenameDevice?: (deviceId: string, name: string) => Promise<Device | void>;
  onRenameSession: (sessionId: string, title: string) => Promise<Session | void>;
  agentTokens: AgentToken[];
  createdAgentToken: CreatedAgentToken | null;
  tokenLoading: boolean;
  tokenError: string | null;
  onCreateAgentToken: (name: string, ttlHours: number) => Promise<void>;
  onRevokeAgentToken: (id: string) => Promise<void>;
  onDeleteAgentToken?: (id: string) => Promise<void>;
  onRefreshAgentTokens: () => Promise<void>;
}) {
  const devicesById = useMemo(() => new Map(devices.map((device) => [device.id, device])), [devices]);
  const initialSessions = useMemo(
    () =>
      Object.entries(sessions).flatMap(([deviceId, deviceSessions]) => {
        const device = devicesById.get(deviceId);
        return deviceSessions.map((session) => enrichSessionDevice(session, deviceId, device));
      }),
    [devicesById, sessions]
  );
  const [localDevices, setLocalDevices] = useState<Device[]>(devices);
  const [localSessions, setLocalSessions] = useState<Session[]>(initialSessions);
  const [viewMode, setViewMode] = useState<ViewMode>('terminals');
  const [filesDevice, setFilesDevice] = useState<Device | null>(null);

  useEffect(() => {
    setLocalDevices(devices);
  }, [devices]);

  useEffect(() => {
    setLocalSessions(initialSessions);
  }, [initialSessions]);

  async function createAndAppend(deviceId: string) {
    const session = await onCreateSession(deviceId);
    if (session) {
      const device = localDevices.find((item) => item.id === deviceId);
      setLocalSessions((current) => [...current, enrichSessionDevice(session, deviceId, device)]);
    }
  }

  async function renameDeviceAndApply(deviceId: string, name: string) {
    const updated = await onRenameDevice(deviceId, name);
    const fallback = localDevices.find((device) => device.id === deviceId);
    const nextDevice = updated ?? (fallback ? { ...fallback, name } : undefined);
    if (!nextDevice) return;
    setLocalDevices((current) => current.map((device) => (device.id === deviceId ? nextDevice : device)));
    setLocalSessions((current) =>
      current.map((session) =>
        session.device_id === deviceId
          ? { ...session, device_name: nextDevice.name, device_platform: nextDevice.platform }
          : session
      )
    );
  }

  if (!user) return <LoginView onLogin={onLogin} onVerifyTwoFactor={onVerifyTwoFactor} />;
  return (
    <div className="shell">
      <aside className="devices">
        <div className="brand">
          <Terminal size={18} aria-hidden="true" />
          <span>vibe-terminal</span>
        </div>
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
        <DeviceList
          devices={localDevices}
          onCreateSession={createAndAppend}
          onRenameDevice={renameDeviceAndApply}
          onOpenFiles={setFilesDevice}
          compact
        />
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
          onDelete={onDeleteAgentToken}
          onRefresh={onRefreshAgentTokens}
        />
      )}
      {filesDevice && <FileManagerPanel device={filesDevice} onClose={() => setFilesDevice(null)} />}
    </div>
  );
}
