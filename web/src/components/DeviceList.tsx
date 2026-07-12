import type { FormEvent } from 'react';
import { useState } from 'react';
import { Check, FolderOpen, Pencil, Terminal, X } from 'lucide-react';
import type { Device } from '../api';
import { useT } from '../i18n';

export function DeviceList({
  devices,
  onCreateSession,
  onRenameDevice,
  onOpenFiles,
  compact = false,
}: {
  devices: Device[];
  onCreateSession: (deviceId: string) => Promise<void>;
  onRenameDevice?: (deviceId: string, name: string) => Promise<void>;
  onOpenFiles?: (device: Device) => void;
  compact?: boolean;
}) {
  const [renaming, setRenaming] = useState<string | null>(null);
  const [draftName, setDraftName] = useState('');
  const [pendingDevice, setPendingDevice] = useState<string | null>(null);
  const { t } = useT();

  function startRename(device: Device) {
    setRenaming(device.id);
    setDraftName(device.name);
  }

  async function submitRename(event: FormEvent<HTMLFormElement>, device: Device) {
    event.preventDefault();
    const name = draftName.trim();
    if (!name || !onRenameDevice) return;
    setPendingDevice(device.id);
    try {
      await onRenameDevice(device.id, name);
      setRenaming(null);
    } finally {
      setPendingDevice(null);
    }
  }

  return (
    <section className={compact ? 'devicesPanel compact' : 'devices'}>
      <h2>{t('devices.title')}</h2>
      {devices.map((device) => {
        const isPending = pendingDevice === device.id;
        return (
          <section key={device.id} className="deviceRow">
            <div className="deviceIdentity">
              {renaming === device.id ? (
                <form className="renameForm deviceRenameForm" onSubmit={(event) => submitRename(event, device)}>
                  <label>
                    <span>{t('devices.name')}</span>
                    <input
                      autoFocus
                      value={draftName}
                      onChange={(event) => setDraftName(event.target.value)}
                      disabled={isPending}
                    />
                  </label>
                  <button
                    className="iconButton"
                    type="submit"
                    aria-label={t('devices.saveName')}
                    disabled={isPending || !draftName.trim()}
                  >
                    <Check aria-hidden="true" size={14} />
                  </button>
                  <button
                    className="iconButton"
                    type="button"
                    aria-label={t('devices.cancelRename', { name: device.name })}
                    disabled={isPending}
                    onClick={() => setRenaming(null)}
                  >
                    <X aria-hidden="true" size={14} />
                  </button>
                </form>
              ) : (
                <>
                  <strong>{device.name}</strong>
                  <span>{device.platform}</span>
                </>
              )}
            </div>
            <span className={device.online ? 'online' : 'offline'}>
              {device.online ? t('devices.online') : t('devices.offline')}
            </span>
            <div className="deviceActions">
              {onRenameDevice && renaming !== device.id && (
                <button
                  className="iconButton"
                  type="button"
                  aria-label={t('devices.rename', { name: device.name })}
                  disabled={isPending}
                  onClick={() => startRename(device)}
                >
                  <Pencil aria-hidden="true" size={14} />
                </button>
              )}
              {onOpenFiles && (
                <button
                  className="iconButton"
                  type="button"
                  aria-label={t('devices.browseFiles', { name: device.name })}
                  disabled={!device.online || isPending}
                  onClick={() => onOpenFiles(device)}
                >
                  <FolderOpen aria-hidden="true" size={14} />
                </button>
              )}
              <button disabled={!device.online || isPending} onClick={() => onCreateSession(device.id)}>
                <Terminal size={16} aria-hidden="true" />
                {t('devices.newTerminal')}
              </button>
            </div>
          </section>
        );
      })}
    </section>
  );
}
