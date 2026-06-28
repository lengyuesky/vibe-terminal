import { Terminal } from 'lucide-react';
import type { Device } from '../api';

export function DeviceList({
  devices,
  onCreateSession,
  compact = false,
}: {
  devices: Device[];
  onCreateSession: (deviceId: string) => Promise<void>;
  compact?: boolean;
}) {
  return (
    <section className={compact ? 'devicesPanel compact' : 'devices'}>
      <h2>Devices</h2>
      {devices.map((device) => (
        <section key={device.id} className="deviceRow">
          <div>
            <strong>{device.name}</strong>
            <span>{device.platform}</span>
          </div>
          <span className={device.online ? 'online' : 'offline'}>{device.online ? 'online' : 'offline'}</span>
          <button disabled={!device.online} onClick={() => onCreateSession(device.id)}>
            <Terminal size={16} aria-hidden="true" />
            New terminal
          </button>
        </section>
      ))}
    </section>
  );
}
