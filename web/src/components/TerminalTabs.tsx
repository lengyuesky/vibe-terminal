import { useEffect, useState } from 'react';
import type { Session } from '../api';
import { TerminalPane } from './TerminalPane';

export function TerminalTabs({ sessions }: { sessions: Session[] }) {
  const [active, setActive] = useState<string | null>(sessions[0]?.id ?? null);

  useEffect(() => {
    if (!active && sessions[0]) {
      setActive(sessions[0].id);
    }
  }, [active, sessions]);

  const activeSession = sessions.find((session) => session.id === active) ?? sessions[0];
  if (sessions.length === 0) {
    return <main className="empty">No terminal session open</main>;
  }
  return (
    <main className="terminalArea">
      <div role="tablist" className="tabs">
        {sessions.map((session) => (
          <button
            role="tab"
            aria-selected={session.id === activeSession.id}
            key={session.id}
            onClick={() => setActive(session.id)}
          >
            {session.id} · {session.status}
          </button>
        ))}
      </div>
      <TerminalPane sessionId={activeSession.id} readOnly={activeSession.status === 'closed' || activeSession.status === 'exited' || activeSession.status === 'lost'} />
    </main>
  );
}
