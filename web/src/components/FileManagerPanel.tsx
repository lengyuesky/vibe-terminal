import { useCallback, useEffect, useState } from 'react';
import { ArrowUp, Download, File as FileIcon, Folder, RefreshCw, X } from 'lucide-react';
import type { Device, FsEntry } from '../api';
import * as api from '../api';

function formatSize(size: number): string {
  if (size < 1024) return `${size} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = size;
  let unit = -1;
  do {
    value /= 1024;
    unit += 1;
  } while (value >= 1024 && unit < units.length - 1);
  return `${value.toFixed(1)} ${units[unit]}`;
}

function formatTime(unixSeconds: number): string {
  if (!unixSeconds) return '';
  return new Date(unixSeconds * 1000).toLocaleString();
}

function joinPath(dir: string, name: string): string {
  return dir.endsWith('/') ? `${dir}${name}` : `${dir}/${name}`;
}

function parentPath(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const index = trimmed.lastIndexOf('/');
  return index <= 0 ? '/' : trimmed.slice(0, index);
}

function breadcrumbSegments(path: string): Array<{ label: string; target: string }> {
  const segments = [{ label: '/', target: '/' }];
  let acc = '';
  for (const part of path.split('/').filter(Boolean)) {
    acc += `/${part}`;
    segments.push({ label: part, target: acc });
  }
  return segments;
}

export function FileManagerPanel({ device, onClose }: { device: Device; onClose: () => void }) {
  const [path, setPath] = useState('~');
  const [entries, setEntries] = useState<FsEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (target: string) => {
      setLoading(true);
      setError(null);
      try {
        const listing = await api.listDeviceFiles(device.id, target);
        setPath(listing.path);
        setEntries(listing.entries ?? []);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to list directory');
      } finally {
        setLoading(false);
      }
    },
    [device.id]
  );

  useEffect(() => {
    void load('~');
  }, [load]);

  function downloadEntry(entry: FsEntry) {
    const anchor = document.createElement('a');
    anchor.href = api.deviceFileURL(device.id, joinPath(path, entry.name));
    anchor.download = entry.name;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
  }

  return (
    <div className="filePanelOverlay" role="dialog" aria-label={`Files on ${device.name}`}>
      <section className="filePanel">
        <header className="filePanelHeader">
          <h2>
            <Folder size={16} aria-hidden="true" />
            <span>Files</span>
            <span className="filePanelDevice">{device.name}</span>
          </h2>
          <button className="iconButton" type="button" aria-label="Close file manager" onClick={onClose}>
            <X aria-hidden="true" size={16} />
          </button>
        </header>
        <div className="filePanelToolbar">
          <button
            className="iconButton"
            type="button"
            aria-label="Parent directory"
            disabled={loading || path === '/'}
            onClick={() => void load(parentPath(path))}
          >
            <ArrowUp aria-hidden="true" size={14} />
          </button>
          <nav className="fileBreadcrumbs" aria-label="Current path">
            {breadcrumbSegments(path).map((segment) => (
              <button
                key={segment.target}
                type="button"
                disabled={loading}
                onClick={() => void load(segment.target)}
              >
                {segment.label}
              </button>
            ))}
          </nav>
          <button
            className="iconButton"
            type="button"
            aria-label="Refresh"
            disabled={loading}
            onClick={() => void load(path)}
          >
            <RefreshCw aria-hidden="true" size={14} />
          </button>
        </div>
        {error && (
          <div className="filePanelError" role="alert">
            {error}
          </div>
        )}
        <div className="fileList">
          {entries.map((entry) => (
            <div className="fileRow" key={entry.name}>
              {entry.is_dir ? (
                <button
                  className="fileName"
                  type="button"
                  aria-label={`Open ${entry.name}`}
                  onClick={() => void load(joinPath(path, entry.name))}
                >
                  <Folder aria-hidden="true" size={14} />
                  <span>{entry.name}</span>
                </button>
              ) : (
                <span className="fileName">
                  <FileIcon aria-hidden="true" size={14} />
                  <span>{entry.name}</span>
                </span>
              )}
              <span className="fileSize">{entry.is_dir ? '' : formatSize(entry.size)}</span>
              <span className="fileTime">{formatTime(entry.modified_at)}</span>
              <span className="fileActions">
                {!entry.is_dir && (
                  <button
                    className="iconButton"
                    type="button"
                    aria-label={`Download ${entry.name}`}
                    onClick={() => downloadEntry(entry)}
                  >
                    <Download aria-hidden="true" size={14} />
                  </button>
                )}
              </span>
            </div>
          ))}
          {!loading && !error && entries.length === 0 && <div className="fileEmpty">Empty directory</div>}
        </div>
      </section>
    </div>
  );
}
