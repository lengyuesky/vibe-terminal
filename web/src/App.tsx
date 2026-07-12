import { KeyRound, Monitor, Settings, Terminal } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { AgentToken, CreatedAgentToken, Device, LoginResult, Session, User } from './api';
import * as api from './api';
import { AgentTokenManager } from './components/AgentTokenManager';
import { DeviceList } from './components/DeviceList';
import { FileManagerPanel } from './components/FileManagerPanel';
import { LoginView } from './components/LoginView';
import { SettingsView } from './components/SettingsView';
import type { SecurityLoader } from './components/SettingsView';
import { TerminalTabs } from './components/TerminalTabs';
import { useT } from './i18n';

type SessionsByDevice = Record<string, Session[]>;
type ViewMode = 'terminals' | 'agentTokens' | 'settings' | 'devices';

type AgentTokenState = {
  userId: string | null;
  tokens: AgentToken[];
  createdToken: CreatedAgentToken | null;
  loading: boolean;
  error: string | null;
};

function scopeAgentTokenState(value: AgentTokenState, userId: string | null): AgentTokenState {
  return value.userId === userId
    ? value
    : { userId, tokens: [], createdToken: null, loading: false, error: null };
}

export function useAgentTokenState(user: User | null) {
  const userId = user?.id ?? null;
  const generationRef = useRef(0);
  const userIdRef = useRef(userId);
  const mountedRef = useRef(true);
  const [state, setState] = useState<AgentTokenState>({ userId, tokens: [], createdToken: null, loading: false, error: null });
  if (userIdRef.current !== userId) {
    userIdRef.current = userId;
    generationRef.current += 1;
  }
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      generationRef.current += 1;
    };
  }, []);
  const current = state.userId === userId ? state : { userId, tokens: [], createdToken: null, loading: false, error: null };
  const scopedStateRef = useRef(current);
  scopedStateRef.current = current;
  const begin = () => ({ generation: generationRef.current, userId });
  const valid = (request: { generation: number; userId: string | null }) =>
    mountedRef.current && request.generation === generationRef.current && request.userId === userIdRef.current;

  const load = useCallback(async () => {
    const request = { generation: generationRef.current, userId };
    if (!request.userId) return;
    setState({ ...scopedStateRef.current, userId: request.userId, loading: true, error: null });
    try {
      const tokens = await api.listAgentTokens();
      if (mountedRef.current && request.generation === generationRef.current && request.userId === userIdRef.current) {
        setState((value) => ({ ...scopeAgentTokenState(value, request.userId), tokens }));
      }
    } catch {
      if (mountedRef.current && request.generation === generationRef.current && request.userId === userIdRef.current) {
        setState((value) => ({ ...scopeAgentTokenState(value, request.userId), error: 'Failed to load agent tokens.' }));
      }
    } finally {
      if (mountedRef.current && request.generation === generationRef.current && request.userId === userIdRef.current) {
        setState((value) => ({ ...scopeAgentTokenState(value, request.userId), loading: false }));
      }
    }
  }, [userId]);

  async function mutate<T>(action: () => Promise<T>, failure: string, apply: (value: T, userId: string | null) => void) {
    const request = begin();
    if (!request.userId) return;
    setState((value) => ({ ...scopeAgentTokenState(value, request.userId), loading: true, error: null }));
    try {
      const result = await action();
      if (valid(request)) apply(result, request.userId);
    } catch (error) {
      if (valid(request)) setState((value) => ({ ...scopeAgentTokenState(value, request.userId), error: failure }));
      throw error;
    } finally {
      if (valid(request)) setState((value) => ({ ...scopeAgentTokenState(value, request.userId), loading: false }));
    }
  }
  const create = (name: string, ttlHours: number) => mutate(
    () => api.createAgentToken(name, ttlHours), 'Failed to create agent token.', (created, scopedUserId) =>
      setState((value) => {
        const scoped = scopeAgentTokenState(value, scopedUserId);
        return { ...scoped, createdToken: created, tokens: [created, ...scoped.tokens.filter((token) => token.id !== created.id)] };
      })
  );
  const revoke = (id: string) => mutate(
    () => api.revokeAgentToken(id), 'Failed to revoke agent token.', (revoked, scopedUserId) =>
      setState((value) => {
        const scoped = scopeAgentTokenState(value, scopedUserId);
        return { ...scoped, tokens: scoped.tokens.map((token) => token.id === id ? revoked : token) };
      })
  );
  const remove = (id: string) => mutate(
    () => api.deleteAgentToken(id), 'Failed to delete agent token.', (_, scopedUserId) =>
      setState((value) => {
        const scoped = scopeAgentTokenState(value, scopedUserId);
        return { ...scoped, tokens: scoped.tokens.filter((token) => token.id !== id), createdToken: scoped.createdToken?.id === id ? null : scoped.createdToken };
      })
  );
  return { tokens: current.tokens, createdToken: current.createdToken, loading: current.loading, error: current.error, load, create, revoke, remove };
}

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
  const tokenState = useAgentTokenState(user);
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
      agentTokens={tokenState.tokens}
      createdAgentToken={tokenState.createdToken}
      tokenLoading={tokenState.loading}
      tokenError={tokenState.error}
      onCreateAgentToken={tokenState.create}
      onRevokeAgentToken={tokenState.revoke}
      onDeleteAgentToken={tokenState.remove}
      onRefreshAgentTokens={tokenState.load}
    />
  );
}

type AppViewProps = {
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
  securityLoader?: SecurityLoader;
};

export function AppView(props: AppViewProps) {
  if (!props.user) {
    return <LoginView onLogin={props.onLogin} onVerifyTwoFactor={props.onVerifyTwoFactor} />;
  }
  return <AuthenticatedAppView key={props.user.id} {...props} user={props.user} />;
}

function AuthenticatedAppView({
  devices,
  sessions,
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
  securityLoader,
}: Omit<AppViewProps, 'user'> & { user: User }) {
  const { t } = useT();
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
  const [securityDeliveryLocked, setSecurityDeliveryLocked] = useState(false);

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

  return (
    <div className="shell">
      <aside className="devices">
        <div className="brand">
          <Terminal size={18} aria-hidden="true" />
          <span>vibe-terminal</span>
        </div>
        <nav className="sideNav" aria-label={t('nav.primary')}>
          <button
            type="button"
            disabled={securityDeliveryLocked}
            aria-current={viewMode === 'terminals' ? 'page' : undefined}
            className={viewMode === 'terminals' ? 'active' : ''}
            onClick={() => setViewMode('terminals')}
          >
            <Monitor size={16} aria-hidden="true" />
            {t('nav.terminals')}
          </button>
          <button
            type="button"
            disabled={securityDeliveryLocked}
            aria-current={viewMode === 'agentTokens' ? 'page' : undefined}
            className={viewMode === 'agentTokens' ? 'active' : ''}
            onClick={() => setViewMode('agentTokens')}
          >
            <KeyRound size={16} aria-hidden="true" />
            {t('nav.agentTokens')}
          </button>
          <button
            type="button"
            aria-current={viewMode === 'settings' ? 'page' : undefined}
            className={viewMode === 'settings' ? 'active' : ''}
            onClick={() => setViewMode('settings')}
          >
            <Settings size={16} aria-hidden="true" />
            {t('nav.settings')}
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
      <div className="viewPane" hidden={viewMode !== 'terminals'} aria-hidden={viewMode !== 'terminals'}>
        <TerminalTabs
          sessions={localSessions}
          onSessionsChange={setLocalSessions}
          onCloseSession={onCloseSession}
          onRenameSession={onRenameSession}
        />
      </div>
      <div className="viewPane" hidden={viewMode !== 'agentTokens'} aria-hidden={viewMode !== 'agentTokens'}>
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
      </div>
      {viewMode === 'settings' && (
        <SettingsView
          securityLoader={securityLoader}
          onRecoveryDeliveryLockChange={setSecurityDeliveryLocked}
          deliveryLocked={securityDeliveryLocked}
        />
      )}
      {filesDevice && <FileManagerPanel device={filesDevice} onClose={() => setFilesDevice(null)} />}
    </div>
  );
}
