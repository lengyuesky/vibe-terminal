export type User = { id: string; username: string };
export type Device = { id: string; name: string; platform: string; online: boolean };
export type Session = {
  id: string;
  device_id?: string;
  title: string;
  status: string;
  last_output_seq?: number;
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    credentials: 'include',
  });
  if (!res.ok) {
    throw new Error(`${res.status} ${await res.text()}`);
  }
  return res.json() as Promise<T>;
}

export function login(username: string, password: string): Promise<User> {
  return request<User>('/api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
}

export function me(): Promise<User> {
  return request<User>('/api/me');
}

export function listDevices(): Promise<Device[]> {
  return request<Device[]>('/api/devices');
}

export function createSession(deviceId: string): Promise<Session> {
  return request<Session>(`/api/devices/${deviceId}/sessions`, {
    method: 'POST',
    body: JSON.stringify({ shell_path: '/bin/bash', working_directory: '$HOME', cols: 120, rows: 32 }),
  });
}
