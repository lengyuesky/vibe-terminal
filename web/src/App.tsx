import { useEffect, useMemo, useState } from 'react';
import type { Device, Session, User } from './api';
import * as api from './api';
import { DeviceList } from './components/DeviceList';
import { LoginView } from './components/LoginView';
import { TerminalTabs } from './components/TerminalTabs';

type SessionsByDevice = Record<string, Session[]>;

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [sessions, setSessions] = useState<SessionsByDevice>({});

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null));
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

  return (
    <AppView
      user={user}
      devices={devices}
      sessions={sessions}
      onLogin={handleLogin}
      onCloseSession={handleCloseSession}
      onCreateSession={handleCreateSession}
      onRenameSession={handleRenameSession}
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
}: {
  user: User | null;
  devices: Device[];
  sessions: SessionsByDevice;
  onLogin: (username: string, password: string) => Promise<void>;
  onCloseSession: (sessionId: string) => Promise<void>;
  onCreateSession: (deviceId: string) => Promise<Session | void>;
  onRenameSession: (sessionId: string, title: string) => Promise<Session | void>;
}) {
  const initialSessions = useMemo(() => Object.values(sessions).flat(), [sessions]);
  const [localSessions, setLocalSessions] = useState<Session[]>(initialSessions);

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
      <DeviceList devices={devices} onCreateSession={createAndAppend} />
      <TerminalTabs
        sessions={localSessions}
        onSessionsChange={setLocalSessions}
        onCloseSession={onCloseSession}
        onRenameSession={onRenameSession}
      />
    </div>
  );
}
