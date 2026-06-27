import { useEffect, useRef } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';
import { encodeResize, encodeStdin, encodeSubscribe, webSocketURL, type TerminalEvent } from '../ws';

export function TerminalPane({ sessionId, readOnly }: { sessionId: string; readOnly: boolean }) {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    let socket: WebSocket | null = null;
    let terminal: Terminal | null = null;

    try {
      terminal = new Terminal({ cursorBlink: !readOnly, disableStdin: readOnly, convertEol: true });
      const fit = new FitAddon();
      terminal.loadAddon(fit);
      terminal.open(ref.current);
      fit.fit();
      terminal.writeln(`connected to ${sessionId}`);

      if (typeof WebSocket !== 'undefined') {
        socket = new WebSocket(webSocketURL());
        socket.addEventListener('open', () => {
          socket?.send(encodeSubscribe(sessionId));
          socket?.send(encodeResize(sessionId, terminal?.cols ?? 80, terminal?.rows ?? 24));
        });
        socket.addEventListener('message', (event) => {
          const message = JSON.parse(event.data) as TerminalEvent;
          if (message.type === 'stdout' && message.session_id === sessionId) {
            terminal?.write(message.payload.data);
          }
        });
        terminal.onData((data) => {
          if (!readOnly && socket?.readyState === WebSocket.OPEN) {
            socket.send(encodeStdin(sessionId, data));
          }
        });
      }
    } catch {
      ref.current.textContent = `connected to ${sessionId}`;
    }

    return () => {
      socket?.close();
      terminal?.dispose();
    };
  }, [sessionId, readOnly]);

  return <div className="terminalPane" ref={ref} />;
}
