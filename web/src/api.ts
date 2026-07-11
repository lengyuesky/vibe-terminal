import { jsonRequestHeaders } from './api-internals';

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

export type LoginResult =
  | { status: 'authenticated'; user: User }
  | { status: 'two_factor_required'; challengeToken: string; expiresIn: number };

export type TwoFactorStatus = { enabled: boolean; recoveryCodesRemaining: number };
export type TwoFactorSetup = { manualKey: string; otpauthURI: string; expiresAt: string };

export class APIError extends Error {
  status: number;
  code: string;
  retryAfter?: number;

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message);
    this.name = 'APIError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

function retryAfterSeconds(response: Response): number | undefined {
  const value = response.headers.get('Retry-After')?.trim();
  if (!value || !/^\d+$/.test(value)) return undefined;
  const seconds = Number(value);
  return Number.isSafeInteger(seconds) ? seconds : undefined;
}

async function toAPIError(response: Response): Promise<APIError> {
  const text = await response.text();
  let code = 'http_error';
  let message = text.trim() || `request failed with status ${response.status}`;
  if (text) {
    try {
      const body = JSON.parse(text) as unknown;
      if (body && typeof body === 'object') {
        const error = 'error' in body && body.error && typeof body.error === 'object' ? body.error : body;
        if ('code' in error && typeof error.code === 'string' && error.code) code = error.code;
        if ('message' in error && typeof error.message === 'string' && error.message) message = error.message;
      }
    } catch {
      // 非 JSON 错误仍保留服务端文本，避免丢失可诊断信息。
    }
  }
  return new APIError(response.status, code, message, retryAfterSeconds(response));
}

async function fetchResponse(path: string, init?: RequestInit): Promise<Response> {
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      headers: jsonRequestHeaders(init?.headers),
      credentials: 'include',
    });
  } catch (error) {
    throw new APIError(0, 'network_error', error instanceof Error ? error.message : 'network request failed');
  }
  if (!response.ok) throw await toAPIError(response);
  return response;
}

async function responseJSON(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) throw new APIError(response.status, 'invalid_response', 'server returned an empty response');
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new APIError(response.status, 'invalid_response', 'server returned invalid JSON');
  }
}

function parseUser(value: unknown, status: number): User {
  if (
    !value ||
    typeof value !== 'object' ||
    !('id' in value) ||
    typeof value.id !== 'string' ||
    !value.id ||
    !('username' in value) ||
    typeof value.username !== 'string' ||
    !value.username
  ) {
    throw new APIError(status, 'invalid_response', 'server returned an invalid user');
  }
  return { id: value.id, username: value.username };
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetchResponse(path, init);
  if (response.status === 204) {
    return undefined as T;
  }
  return (await responseJSON(response)) as T;
}

export async function login(username: string, password: string): Promise<LoginResult> {
  const response = await fetchResponse('/api/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
  const body = await responseJSON(response);
  if (response.status === 200) {
    return { status: 'authenticated', user: parseUser(body, response.status) };
  }
  if (
    response.status === 202 &&
    body &&
    typeof body === 'object' &&
    'two_factor_required' in body &&
    body.two_factor_required === true &&
    'challenge_token' in body &&
    typeof body.challenge_token === 'string' &&
    body.challenge_token &&
    'expires_in' in body &&
    typeof body.expires_in === 'number' &&
    Number.isSafeInteger(body.expires_in) &&
    body.expires_in > 0
  ) {
    return {
      status: 'two_factor_required',
      challengeToken: body.challenge_token,
      expiresIn: body.expires_in,
    };
  }
  throw new APIError(response.status, 'invalid_response', 'server returned an invalid login response');
}

export async function verifyTwoFactor(challengeToken: string, code: string): Promise<User> {
  const response = await fetchResponse('/api/login/2fa', {
    method: 'POST',
    body: JSON.stringify({ challenge_token: challengeToken, code }),
  });
  if (response.status !== 200) {
    throw new APIError(response.status, 'invalid_response', 'server returned an invalid verification response');
  }
  return parseUser(await responseJSON(response), response.status);
}

function requireManagementStatus(response: Response): void {
  if (response.status !== 200) {
    throw new APIError(response.status, 'invalid_response', 'server returned an unexpected success status');
  }
}

function responseObject(value: unknown, status: number): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new APIError(status, 'invalid_response', 'server returned an invalid two-factor response');
  }
  return value as Record<string, unknown>;
}

function parseRecoveryCodes(value: unknown, status: number): string[] {
  const body = responseObject(value, status);
  if (
    !Array.isArray(body.recovery_codes) ||
    body.recovery_codes.length !== 10 ||
    body.recovery_codes.some((code) => typeof code !== 'string' || !code.trim())
  ) {
    throw new APIError(status, 'invalid_response', 'server returned invalid recovery codes');
  }
  return body.recovery_codes.map((code) => code.trim());
}

export async function getTwoFactorStatus(): Promise<TwoFactorStatus> {
  const response = await fetchResponse('/api/security/2fa', { method: 'GET' });
  requireManagementStatus(response);
  const body = responseObject(await responseJSON(response), response.status);
  if (
    typeof body.enabled !== 'boolean' ||
    !Number.isSafeInteger(body.recovery_codes_remaining) ||
    (body.recovery_codes_remaining as number) < 0 ||
    (body.recovery_codes_remaining as number) > 10 ||
    (!body.enabled && body.recovery_codes_remaining !== 0)
  ) {
    throw new APIError(response.status, 'invalid_response', 'server returned an invalid two-factor status');
  }
  return {
    enabled: body.enabled,
    recoveryCodesRemaining: body.recovery_codes_remaining as number,
  };
}

export async function startTwoFactorSetup(password: string): Promise<TwoFactorSetup> {
  const response = await fetchResponse('/api/security/2fa/setup', {
    method: 'POST',
    body: JSON.stringify({ password }),
  });
  requireManagementStatus(response);
  const body = responseObject(await responseJSON(response), response.status);
  if (
    typeof body.manual_key !== 'string' ||
    !body.manual_key.trim() ||
    typeof body.otpauth_uri !== 'string' ||
    !body.otpauth_uri.startsWith('otpauth://') ||
    typeof body.expires_at !== 'string' ||
    !body.expires_at ||
    !Number.isFinite(Date.parse(body.expires_at))
  ) {
    throw new APIError(response.status, 'invalid_response', 'server returned an invalid two-factor setup');
  }
  return {
    manualKey: body.manual_key.trim(),
    otpauthURI: body.otpauth_uri,
    expiresAt: body.expires_at,
  };
}

export async function enableTwoFactor(code: string): Promise<string[]> {
  const response = await fetchResponse('/api/security/2fa/enable', {
    method: 'POST',
    body: JSON.stringify({ code }),
  });
  requireManagementStatus(response);
  return parseRecoveryCodes(await responseJSON(response), response.status);
}

export async function regenerateRecoveryCodes(password: string, code: string): Promise<string[]> {
  const response = await fetchResponse('/api/security/2fa/recovery-codes', {
    method: 'POST',
    body: JSON.stringify({ password, code }),
  });
  requireManagementStatus(response);
  return parseRecoveryCodes(await responseJSON(response), response.status);
}

export async function disableTwoFactor(password: string): Promise<void> {
  const response = await fetchResponse('/api/security/2fa/disable', {
    method: 'POST',
    body: JSON.stringify({ password }),
  });
  requireManagementStatus(response);
  const body = responseObject(await responseJSON(response), response.status);
  if (body.ok !== true) {
    throw new APIError(response.status, 'invalid_response', 'server did not confirm two-factor disable');
  }
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

export type Snippet = {
  id: string;
  name: string;
  command: string;
  created_at: string;
  updated_at: string;
};

export function listSnippets(): Promise<Snippet[]> {
  return request<Snippet[]>('/api/snippets');
}

export function createSnippet(name: string, command: string): Promise<Snippet> {
  return request<Snippet>('/api/snippets', {
    method: 'POST',
    body: JSON.stringify({ name, command }),
  });
}

export function updateSnippet(id: string, name: string, command: string): Promise<Snippet> {
  return request<Snippet>(`/api/snippets/${id}`, {
    method: 'PUT',
    body: JSON.stringify({ name, command }),
  });
}

export function deleteSnippet(id: string): Promise<void> {
  return request<void>(`/api/snippets/${id}`, {
    method: 'DELETE',
  });
}
