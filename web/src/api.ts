export type User = { id: string; username: string };
export type Device = { id: string; name: string; platform: string; online: boolean };
export type Session = {
  id: string;
  device_id?: string;
  title: string;
  status: string;
  shell_path?: string;
  working_directory?: string;
  agent_pid?: number;
  last_output_seq?: number;
};
export type SessionOutputChunk = {
  id: string;
  session_id: string;
  start_seq: number;
  end_seq: number;
  data: string;
};
export type AgentToken = {
  id: string;
  name: string;
  expires_at: string;
  used_at?: string;
  revoked_at?: string;
  created_at: string;
};
export type CreatedAgentToken = AgentToken & {
  token: string;
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
  if (res.status === 204) {
    return undefined as T;
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

export function listAgentTokens(): Promise<AgentToken[]> {
  return request<AgentToken[]>('/api/agent-tokens');
}

export function createAgentToken(name: string, ttlHours: number): Promise<CreatedAgentToken> {
  return request<CreatedAgentToken>('/api/agent-tokens', {
    method: 'POST',
    body: JSON.stringify({ name, ttl_hours: ttlHours }),
  });
}

export function revokeAgentToken(id: string): Promise<AgentToken> {
  return request<AgentToken>(`/api/agent-tokens/${id}`, {
    method: 'DELETE',
  });
}

export function deleteAgentToken(id: string): Promise<void> {
  return request<void>(`/api/agent-tokens/${id}/permanent`, {
    method: 'DELETE',
  });
}

export function listDevices(): Promise<Device[]> {
  return request<Device[]>('/api/devices');
}

export function listSessions(deviceId: string): Promise<Session[]> {
  return request<Session[]>(`/api/devices/${deviceId}/sessions`);
}

export function listSessionOutput(sessionId: string): Promise<SessionOutputChunk[]> {
  return request<SessionOutputChunk[]>(`/api/sessions/${sessionId}/output`);
}

export function createSession(deviceId: string): Promise<Session> {
  return request<Session>(`/api/devices/${deviceId}/sessions`, {
    method: 'POST',
    body: JSON.stringify({ shell_path: '/bin/bash', working_directory: '$HOME', cols: 120, rows: 32 }),
  });
}

export function closeSession(sessionId: string): Promise<void> {
  return request<void>(`/api/sessions/${sessionId}/close`, {
    method: 'POST',
  });
}

export function renameSession(sessionId: string, title: string): Promise<Session> {
  return request<Session>(`/api/sessions/${sessionId}`, {
    method: 'PATCH',
    body: JSON.stringify({ title }),
  });
}
