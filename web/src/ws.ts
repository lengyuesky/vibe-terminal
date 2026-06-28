export type TerminalEvent =
  | { type: 'stdout'; session_id: string; payload: { seq: number; data: string } }
  | { type: 'session_state'; session_id: string; payload: { session_id?: string; status: string; message?: string } }
  | { type: 'error'; payload: { code: string; message: string } };

export function encodeStdin(sessionId: string, data: string): string {
  return JSON.stringify({
    type: 'stdin',
    session_id: sessionId,
    payload: { session_id: sessionId, data },
  });
}

export function encodeResize(sessionId: string, cols: number, rows: number): string {
  return JSON.stringify({
    type: 'resize',
    session_id: sessionId,
    payload: { session_id: sessionId, cols, rows },
  });
}

export function encodeSubscribe(sessionId: string): string {
  return JSON.stringify({
    type: 'subscribe_session',
    session_id: sessionId,
    payload: { session_id: sessionId },
  });
}

export function webSocketURL(): string {
  const url = new URL('/ws/web', window.location.href);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.toString();
}
