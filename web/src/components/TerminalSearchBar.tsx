import type { KeyboardEvent } from 'react';
import { useEffect, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, X } from 'lucide-react';
import { useT } from '../i18n';

export type SearchQuery = { term: string; caseSensitive: boolean };

export function TerminalSearchBar({
  onSearch,
  onClose,
}: {
  onSearch: (query: SearchQuery, direction: 'next' | 'previous') => void;
  onClose: () => void;
}) {
  const { t } = useT();
  const [term, setTerm] = useState('');
  const [caseSensitive, setCaseSensitive] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter') {
      event.preventDefault();
      onSearch({ term, caseSensitive }, event.shiftKey ? 'previous' : 'next');
    } else if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
    }
  }

  return (
    <div className="terminalSearchBar" role="search">
      <input
        ref={inputRef}
        value={term}
        placeholder={t('search.placeholder')}
        aria-label={t('search.terminal')}
        onChange={(event) => setTerm(event.target.value)}
        onKeyDown={handleKeyDown}
      />
      <button
        className={caseSensitive ? 'iconButton searchCaseActive' : 'iconButton'}
        type="button"
        aria-label={t('search.matchCase')}
        aria-pressed={caseSensitive}
        onClick={() => setCaseSensitive((current) => !current)}
      >
        Aa
      </button>
      <button
        className="iconButton"
        type="button"
        aria-label={t('search.previous')}
        onClick={() => onSearch({ term, caseSensitive }, 'previous')}
      >
        <ArrowUp aria-hidden="true" size={14} />
      </button>
      <button
        className="iconButton"
        type="button"
        aria-label={t('search.next')}
        onClick={() => onSearch({ term, caseSensitive }, 'next')}
      >
        <ArrowDown aria-hidden="true" size={14} />
      </button>
      <button className="iconButton" type="button" aria-label={t('search.close')} onClick={onClose}>
        <X aria-hidden="true" size={14} />
      </button>
    </div>
  );
}
