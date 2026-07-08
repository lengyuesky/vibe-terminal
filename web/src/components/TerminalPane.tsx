import { useEffect, useRef, useState } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { SearchAddon } from 'xterm-addon-search';
import { Search } from 'lucide-react';
import 'xterm/css/xterm.css';
import * as api from '../api';
import { encodeResize, encodeStdin, encodeSubscribe, webSocketURL, type TerminalEvent } from '../ws';
import { TerminalSearchBar, type SearchQuery } from './TerminalSearchBar';

type TerminalPaneProps = {
  sessionId: string;
  readOnly: boolean;
  onSessionStateChange?: (sessionId: string, status: string, message?: string) => void;
};

export function TerminalPane({ sessionId, readOnly, onSessionStateChange }: TerminalPaneProps) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [connectionMessage, setConnectionMessage] = useState<string | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const searchAddonRef = useRef<SearchAddon | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    setConnectionMessage(null);
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
      terminal = new Terminal({
        allowProposedApi: true,
        cursorBlink: !readOnly,
        disableStdin: readOnly,
        convertEol: true,
        fontFamily:
          "'JetBrains Mono', ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace",
        fontSize: 13,
        lineHeight: 1.3,
        theme: {
          background: '#0b0d16',
          foreground: '#e8eaf2',
          cursor: '#a78bfa',
          cursorAccent: '#0b0d16',
          selectionBackground: 'rgba(167, 139, 250, 0.35)',
          black: '#1a1d29',
          red: '#fb7185',
          green: '#34d399',
          yellow: '#fbbf24',
          blue: '#60a5fa',
          magenta: '#c084fc',
          cyan: '#22d3ee',
          white: '#e8eaf2',
          brightBlack: '#4b5163',
          brightRed: '#fda4af',
          brightGreen: '#6ee7b7',
          brightYellow: '#fde68a',
          brightBlue: '#93c5fd',
          brightMagenta: '#d8b4fe',
          brightCyan: '#67e8f9',
          brightWhite: '#f8fafc',
        },
      });
      const fit = new FitAddon();
      terminal.loadAddon(fit);
      const search = new SearchAddon();
      terminal.loadAddon(search);
      searchAddonRef.current = search;
      terminal.attachCustomKeyEventHandler((event) => {
        if (event.type === 'keydown' && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') {
          setSearchOpen(true);
          return false;
        }
        return true;
      });
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
          if (message.type === 'error') {
            setConnectionMessage(message.payload.message);
            return;
          }
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
          if (message.type === 'session_state' && message.session_id === sessionId) {
            onSessionStateChange?.(sessionId, message.payload.status, message.payload.message);
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
        socket.addEventListener('close', () => {
          if (cancelled) {
            return;
          }
          setConnectionMessage('Terminal connection closed.');
        });
        socket.addEventListener('error', () => {
          if (cancelled) {
            return;
          }
          setConnectionMessage('Terminal connection error.');
        });
      }
    } catch {
      ref.current.textContent = `connected to ${sessionId}`;
    }

    return () => {
      cancelled = true;
      resizeObserver?.disconnect();
      socket?.close();
      searchAddonRef.current = null;
      terminal?.dispose();
    };
  }, [sessionId, readOnly, onSessionStateChange]);

  function runSearch(query: SearchQuery, direction: 'next' | 'previous') {
    const addon = searchAddonRef.current;
    if (!addon || !query.term) return;
    const options = {
      caseSensitive: query.caseSensitive,
      decorations: {
        matchBackground: '#4c3a78',
        matchOverviewRuler: '#a78bfa',
        activeMatchBackground: '#7c5cbf',
        activeMatchColorOverviewRuler: '#c4b5fd',
      },
    };
    if (direction === 'next') {
      addon.findNext(query.term, options);
    } else {
      addon.findPrevious(query.term, options);
    }
  }

  function closeSearch() {
    searchAddonRef.current?.clearDecorations();
    setSearchOpen(false);
  }

  return (
    <div className="terminalPaneShell">
      <div className="terminalPaneTools">
        {searchOpen ? (
          <TerminalSearchBar onSearch={runSearch} onClose={closeSearch} />
        ) : (
          <button
            className="iconButton"
            type="button"
            aria-label="Search terminal output"
            onClick={() => setSearchOpen(true)}
          >
            <Search aria-hidden="true" size={14} />
          </button>
        )}
      </div>
      {connectionMessage && (
        <div className="terminalStatus" role="status">
          {connectionMessage}
        </div>
      )}
      <div className="terminalPane" ref={ref} />
    </div>
  );
}
