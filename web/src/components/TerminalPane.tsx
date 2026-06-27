import { useEffect, useRef } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';
import * as api from '../api';
import { encodeResize, encodeStdin, encodeSubscribe, webSocketURL, type TerminalEvent } from '../ws';

export function TerminalPane({ sessionId, readOnly }: { sessionId: string; readOnly: boolean }) {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    let socket: WebSocket | null = null;
    let terminal: Terminal | null = null;
    let resizeObserver: ResizeObserver | null = null;
    let cancelled = false;
    let restoring = true;
    let restoredSeq = 0;
    let suppressTerminalData = false;
    const pendingStdout: Array<{ seq: number; data: string }> = [];

    function writeTerminal(data: string, suppressData = false): Promise<void> {
      if (!terminal) return Promise.resolve();
      return new Promise((resolve) => {
        if (suppressData) {
          suppressTerminalData = true;
        }
        terminal?.write(data, () => {
          if (suppressData) {
            suppressTerminalData = false;
          }
          resolve();
        });
      });
    }

    async function restoreOutput() {
      try {
        const chunks = await api.listSessionOutput(sessionId);
        if (cancelled) return;
        for (const chunk of chunks) {
          await writeTerminal(chunk.data, true);
          restoredSeq = Math.max(restoredSeq, chunk.end_seq);
        }
      } catch {
        // 历史输出恢复失败时仍保持实时终端可用。
      } finally {
        restoring = false;
        if (cancelled) {
          pendingStdout.length = 0;
          return;
        }
        for (const frame of pendingStdout) {
          if (frame.seq > restoredSeq) {
            await writeTerminal(frame.data);
            restoredSeq = frame.seq;
          }
        }
        pendingStdout.length = 0;
      }
    }

    function sendResize() {
      if (socket?.readyState === WebSocket.OPEN && terminal) {
        socket.send(encodeResize(sessionId, terminal.cols, terminal.rows));
      }
    }

    try {
      terminal = new Terminal({ cursorBlink: !readOnly, disableStdin: readOnly, convertEol: true });
      const fit = new FitAddon();
      terminal.loadAddon(fit);
      terminal.open(ref.current);
      fit.fit();
      if (typeof ResizeObserver !== 'undefined') {
        resizeObserver = new ResizeObserver(() => {
          fit.fit();
          sendResize();
        });
        resizeObserver.observe(ref.current);
      }
      void restoreOutput();

      if (typeof WebSocket !== 'undefined') {
        socket = new WebSocket(webSocketURL());
        socket.addEventListener('open', () => {
          socket?.send(encodeSubscribe(sessionId));
          sendResize();
        });
        socket.addEventListener('message', (event) => {
          const message = JSON.parse(event.data) as TerminalEvent;
          if (message.type === 'stdout' && message.session_id === sessionId) {
            if (restoring) {
              pendingStdout.push({ seq: message.payload.seq, data: message.payload.data });
              return;
            }
            if (message.payload.seq > restoredSeq) {
              void writeTerminal(message.payload.data);
              restoredSeq = message.payload.seq;
            }
          }
        });
        terminal.onData((data) => {
          if (suppressTerminalData) {
            return;
          }
          if (!readOnly && socket?.readyState === WebSocket.OPEN) {
            socket.send(encodeStdin(sessionId, data));
          }
        });
      }
    } catch {
      ref.current.textContent = `connected to ${sessionId}`;
    }

    return () => {
      cancelled = true;
      resizeObserver?.disconnect();
      socket?.close();
      terminal?.dispose();
    };
  }, [sessionId, readOnly]);

  return <div className="terminalPane" ref={ref} />;
}
