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

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null));
  }, []);

  useEffect(() => {
    if (!user) return;
    api.listDevices().then(setDevices).catch(() => setDevices([]));
  }, [user]);

  async function handleLogin(username: string, password: string) {
    const loggedIn = await api.login(username, password);
    setUser(loggedIn);
    setDevices(await api.listDevices());
  }

  async function handleCreateSession(deviceId: string) {
    return api.createSession(deviceId);
  }

  return (
    <AppView
      user={user}
      devices={devices}
      sessions={{}}
      onLogin={handleLogin}
      onCreateSession={handleCreateSession}
    />
  );
}

export function AppView({
  user,
  devices,
  sessions,
  onLogin,
  onCreateSession,
}: {
  user: User | null;
  devices: Device[];
  sessions: SessionsByDevice;
  onLogin: (username: string, password: string) => Promise<void>;
  onCreateSession: (deviceId: string) => Promise<Session | void>;
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
      <TerminalTabs sessions={localSessions} />
    </div>
  );
}
