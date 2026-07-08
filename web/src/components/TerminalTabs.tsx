import type { Dispatch, FormEvent, SetStateAction } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, Pencil, X } from 'lucide-react';
import type { Session } from '../api';
import { SnippetsBar } from './SnippetsBar';
import { TerminalPane, type TerminalPaneHandle } from './TerminalPane';

type TerminalTabsProps = {
  sessions: Session[];
  onSessionsChange: Dispatch<SetStateAction<Session[]>>;
  onCloseSession: (sessionId: string) => Promise<void>;
  onRenameSession: (sessionId: string, title: string) => Promise<Session | void>;
};

function sessionDirectory(session: Session) {
  return session.working_directory || '$HOME';
}

function sessionDeviceName(session: Session) {
  return session.device_name || session.device_id || 'Unknown device';
}

function shellName(shellPath: string) {
  return shellPath.split('/').filter(Boolean).pop() || shellPath;
}

function shortSessionId(id: string) {
  return id.slice(0, 8);
}

function statusClass(status: string) {
  switch (status) {
    case 'running':
      return 'statusRunning';
    case 'starting':
      return 'statusStarting';
    case 'lost':
      return 'statusLost';
    case 'exited':
      return 'statusExited';
    case 'closed':
      return 'statusClosed';
    default:
      return 'statusUnknown';
  }
}

export function TerminalTabs({ sessions, onSessionsChange, onCloseSession, onRenameSession }: TerminalTabsProps) {
  const [active, setActive] = useState<string | null>(sessions[0]?.id ?? null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [confirmingClose, setConfirmingClose] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState('');
  const [pendingSession, setPendingSession] = useState<string | null>(null);
  const paneRef = useRef<TerminalPaneHandle | null>(null);
  const visibleSessions = sessions.filter((session) => session.status !== 'closed');

  const handleSessionStateChange = useCallback(
    (sessionId: string, status: string) => {
      onSessionsChange((current) =>
        current.map((session) => (session.id === sessionId ? { ...session, status } : session))
      );
    },
    [onSessionsChange]
  );

  useEffect(() => {
    if (!active && visibleSessions[0]) {
      setActive(visibleSessions[0].id);
      return;
    }
    if (active && visibleSessions.length > 0 && !visibleSessions.some((session) => session.id === active)) {
      setActive(visibleSessions[0].id);
      return;
    }
    if (active && visibleSessions.length === 0) {
      setActive(null);
    }
  }, [active, sessions]);

  function sessionLabel(session: Session) {
    if (session.title === 'shell' && session.shell_path) {
      return shellName(session.shell_path);
    }
    return session.title || session.id;
  }

  function startRename(session: Session) {
    setConfirmingClose(null);
    setRenaming(session.id);
    setDraftTitle(sessionLabel(session));
  }

  async function submitRename(event: FormEvent<HTMLFormElement>, session: Session) {
    event.preventDefault();
    const title = draftTitle.trim();
    if (!title) return;
    setPendingSession(session.id);
    try {
      const updated = await onRenameSession(session.id, title);
      onSessionsChange((current) =>
        current.map((item) => (item.id === session.id ? { ...item, ...(updated ?? {}), title } : item))
      );
      setRenaming(null);
    } finally {
      setPendingSession(null);
    }
  }

  function requestClose(session: Session) {
    setRenaming(null);
    setConfirmingClose(session.id);
  }

  async function confirmCloseSession(session: Session) {
    setPendingSession(session.id);
    try {
      await onCloseSession(session.id);
      onSessionsChange((current) => {
        const next = current.filter((item) => item.id !== session.id);
        if (active === session.id) {
          setActive(next[0]?.id ?? null);
        }
        return next;
      });
      if (renaming === session.id) {
        setRenaming(null);
      }
      if (confirmingClose === session.id) {
        setConfirmingClose(null);
      }
    } finally {
      setPendingSession(null);
    }
  }

  const activeSession = visibleSessions.find((session) => session.id === active) ?? visibleSessions[0];
  if (visibleSessions.length === 0) {
    return <main className="empty">No terminal session open</main>;
  }
  return (
    <main className="terminalArea">
      <div role="tablist" className="tabs">
        {visibleSessions.map((session) => {
          const label = sessionLabel(session);
          const isPending = pendingSession === session.id;
          const directory = sessionDirectory(session);
          const deviceName = sessionDeviceName(session);
          return (
            <div className="tabItem" key={session.id}>
              <button
                className="tabButton"
                role="tab"
                aria-selected={session.id === activeSession.id}
                onClick={() => setActive(session.id)}
              >
                <span className="tabTitleLine">
                  <span className="tabTitle">{label}</span>
                  <span className="tabDeviceBadge" title={deviceName}>
                    {deviceName}
                  </span>
                </span>
                <small className="tabMeta">
                  <span className="tabDirectory" title={directory}>
                    {directory}
                  </span>
                  <span className="tabSeparator" aria-hidden="true">
                    ·
                  </span>
                  <span className={`statusBadge ${statusClass(session.status)}`}>{session.status}</span>
                  <span className="tabSeparator" aria-hidden="true">
                    ·
                  </span>
                  <span className="tabSessionId" title={session.id}>
                    {shortSessionId(session.id)}
                  </span>
                </small>
              </button>
              <div className="tabActions">
                {renaming === session.id ? (
                  <form className="renameForm" onSubmit={(event) => submitRename(event, session)}>
                    <label>
                      <span>Session title</span>
                      <input
                        autoFocus
                        value={draftTitle}
                        onChange={(event) => setDraftTitle(event.target.value)}
                        disabled={isPending}
                      />
                    </label>
                    <button className="iconButton" type="submit" aria-label="Save" disabled={isPending || !draftTitle.trim()}>
                      <Check aria-hidden="true" size={14} />
                    </button>
                  </form>
                ) : (
                  <button
                    className="iconButton"
                    type="button"
                    aria-label={`Rename ${label}`}
                    disabled={isPending}
                    onClick={() => startRename(session)}
                  >
                    <Pencil aria-hidden="true" size={14} />
                  </button>
                )}
                {confirmingClose === session.id ? (
                  <>
                    <button
                      className="iconButton danger"
                      type="button"
                      aria-label={`Confirm delete ${label}`}
                      disabled={isPending}
                      onClick={() => confirmCloseSession(session)}
                    >
                      <Check aria-hidden="true" size={14} />
                    </button>
                    <button
                      className="iconButton"
                      type="button"
                      aria-label={`Cancel delete ${label}`}
                      disabled={isPending}
                      onClick={() => setConfirmingClose(null)}
                    >
                      <X aria-hidden="true" size={14} />
                    </button>
                  </>
                ) : (
                  <button
                    className="iconButton danger"
                    type="button"
                    aria-label={`Delete ${label}`}
                    disabled={isPending}
                    onClick={() => requestClose(session)}
                  >
                    <X aria-hidden="true" size={14} />
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>
      <header className="terminalHeader">
        <div>
          <h1>
            <span>{sessionLabel(activeSession)}</span>
            <span className="terminalDeviceBadge">{sessionDeviceName(activeSession)}</span>
          </h1>
          <p>
            {[activeSession.device_platform, sessionDirectory(activeSession), `session ${activeSession.id}`]
              .filter(Boolean)
              .join(' · ')}
          </p>
        </div>
        <SnippetsBar onInsert={(command) => paneRef.current?.sendText(command)} />
      </header>
      <TerminalPane
        ref={paneRef}
        sessionId={activeSession.id}
        readOnly={activeSession.status === 'closed' || activeSession.status === 'exited' || activeSession.status === 'lost'}
        onSessionStateChange={handleSessionStateChange}
      />
    </main>
  );
}
