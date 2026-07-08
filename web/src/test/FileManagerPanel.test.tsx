import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { FileManagerPanel } from '../components/FileManagerPanel';

vi.mock('../api', () => ({
  listDeviceFiles: vi.fn(),
  deviceFileURL: vi.fn(
    (deviceId: string, path: string) =>
      `/api/devices/${deviceId}/fs/file?path=${encodeURIComponent(path)}`
  ),
  uploadDeviceFile: vi.fn(),
  UploadError: class UploadError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

const mockedApi = vi.mocked(api);
const device = { id: 'dev-1', name: 'laptop', platform: 'linux', online: true };

beforeEach(() => {
  vi.clearAllMocks();
});

describe('FileManagerPanel', () => {
  it('loads the home listing and renders entries', async () => {
    mockedApi.listDeviceFiles.mockResolvedValueOnce({
      path: '/home/dev',
      entries: [
        { name: 'projects', is_dir: true, size: 0, mode: 0, modified_at: 1_750_000_000 },
        { name: 'notes.txt', is_dir: false, size: 2048, mode: 0, modified_at: 1_750_000_000 },
      ],
    });
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    expect(mockedApi.listDeviceFiles).toHaveBeenCalledWith('dev-1', '~');
    expect(await screen.findByText('projects')).toBeInTheDocument();
    expect(screen.getByText('notes.txt')).toBeInTheDocument();
    expect(screen.getByText('2.0 KiB')).toBeInTheDocument();
  });

  it('navigates into a directory on click', async () => {
    mockedApi.listDeviceFiles
      .mockResolvedValueOnce({
        path: '/home/dev',
        entries: [{ name: 'projects', is_dir: true, size: 0, mode: 0, modified_at: 0 }],
      })
      .mockResolvedValueOnce({ path: '/home/dev/projects', entries: [] });
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: /open projects/i }));
    await waitFor(() =>
      expect(mockedApi.listDeviceFiles).toHaveBeenLastCalledWith('dev-1', '/home/dev/projects')
    );
    expect(await screen.findByText('Empty directory')).toBeInTheDocument();
  });

  it('triggers a native download for files', async () => {
    mockedApi.listDeviceFiles.mockResolvedValueOnce({
      path: '/home/dev',
      entries: [{ name: 'notes.txt', is_dir: false, size: 3, mode: 0, modified_at: 0 }],
    });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: /download notes.txt/i }));
    expect(clickSpy).toHaveBeenCalled();
    expect(mockedApi.deviceFileURL).toHaveBeenCalledWith('dev-1', '/home/dev/notes.txt');
    clickSpy.mockRestore();
  });

  it('shows an error when listing fails', async () => {
    mockedApi.listDeviceFiles.mockRejectedValueOnce(new Error('503 agent offline'));
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    expect(await screen.findByRole('alert')).toHaveTextContent('503 agent offline');
  });

  it('uploads a chosen file into the current directory and reloads', async () => {
    mockedApi.listDeviceFiles.mockResolvedValue({ path: '/home/dev', entries: [] });
    mockedApi.uploadDeviceFile.mockResolvedValueOnce(undefined);
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    await screen.findByText('Empty directory');
    const input = screen.getByLabelText('Upload file') as HTMLInputElement;
    await userEvent.upload(input, new File(['data'], 'report.pdf'));
    await waitFor(() =>
      expect(mockedApi.uploadDeviceFile).toHaveBeenCalledWith(
        'dev-1',
        '/home/dev/report.pdf',
        expect.any(File),
        expect.objectContaining({ overwrite: false })
      )
    );
    await waitFor(() => expect(mockedApi.listDeviceFiles).toHaveBeenCalledTimes(2));
  });

  it('asks before overwriting on 409 and retries with overwrite', async () => {
    mockedApi.listDeviceFiles.mockResolvedValue({ path: '/home/dev', entries: [] });
    mockedApi.uploadDeviceFile
      .mockRejectedValueOnce(new api.UploadError(409, 'already exists'))
      .mockResolvedValueOnce(undefined);
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<FileManagerPanel device={device} onClose={vi.fn()} />);
    await screen.findByText('Empty directory');
    const input = screen.getByLabelText('Upload file') as HTMLInputElement;
    await userEvent.upload(input, new File(['data'], 'report.pdf'));
    await waitFor(() => expect(mockedApi.uploadDeviceFile).toHaveBeenCalledTimes(2));
    expect(mockedApi.uploadDeviceFile).toHaveBeenLastCalledWith(
      'dev-1',
      '/home/dev/report.pdf',
      expect.any(File),
      expect.objectContaining({ overwrite: true })
    );
    confirmSpy.mockRestore();
  });

  it('closes via the close button', async () => {
    mockedApi.listDeviceFiles.mockResolvedValueOnce({ path: '/', entries: [] });
    const onClose = vi.fn();
    render(<FileManagerPanel device={device} onClose={onClose} />);
    await userEvent.click(await screen.findByRole('button', { name: /close file manager/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
