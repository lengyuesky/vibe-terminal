export type User = { id: string; username: string };
export type Device = { id: string; name: string; platform: string; online: boolean };
export type Session = {
  id: string;
  device_id?: string;
  device_name?: string;
  device_platform?: string;
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

export function renameDevice(deviceId: string, name: string): Promise<Device> {
  return request<Device>(`/api/devices/${deviceId}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  });
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

export type FsEntry = {
  name: string;
  is_dir: boolean;
  size: number;
  mode: number;
  modified_at: number;
};
export type FsListing = { path: string; entries: FsEntry[] | null };

export function listDeviceFiles(deviceId: string, path: string): Promise<FsListing> {
  return request<FsListing>(`/api/devices/${deviceId}/fs?path=${encodeURIComponent(path)}`);
}

export function deviceFileURL(deviceId: string, path: string): string {
  return `/api/devices/${deviceId}/fs/file?path=${encodeURIComponent(path)}`;
}

export class UploadError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message || `upload failed with status ${status}`);
    this.status = status;
  }
}

export function uploadDeviceFile(
  deviceId: string,
  filePath: string,
  file: Blob,
  options: { overwrite?: boolean; onProgress?: (percent: number) => void } = {}
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const overwrite = options.overwrite ? '&overwrite=true' : '';
    xhr.open('POST', `/api/devices/${deviceId}/fs/file?path=${encodeURIComponent(filePath)}${overwrite}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        options.onProgress?.(Math.round((event.loaded / event.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new UploadError(xhr.status, `${xhr.status} ${xhr.responseText}`));
      }
    };
    xhr.onerror = () => reject(new UploadError(0, 'network error'));
    xhr.send(file);
  });
}
