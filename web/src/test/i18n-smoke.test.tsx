import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DeviceList } from '../components/DeviceList';
import { FileManagerPanel } from '../components/FileManagerPanel';
import { SnippetsBar } from '../components/SnippetsBar';
import { TerminalPane } from '../components/TerminalPane';
import { TerminalSearchBar } from '../components/TerminalSearchBar';
import { TerminalTabs } from '../components/TerminalTabs';
import { LanguageProvider } from '../i18n';
import * as api from '../api';

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>();
  return {
    ...actual,
    listDeviceFiles: vi.fn(),
    deviceFileURL: vi.fn(() => ''),
    listSnippets: vi.fn(() => Promise.resolve([])),
    listSessionOutput: vi.fn(() => Promise.resolve([])),
  };
});

// TerminalPane 依赖 xterm,jsdom 下按 TerminalPane.test.tsx 的模式以最小桩替代
vi.mock('xterm', () => ({
  Terminal: vi.fn(() => ({
    cols: 80,
    rows: 24,
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn((_data: string, callback?: () => void) => callback?.()),
    onData: vi.fn(),
    attachCustomKeyEventHandler: vi.fn(),
    dispose: vi.fn(),
  })),
}));
vi.mock('xterm-addon-fit', () => ({
  FitAddon: vi.fn(() => ({ fit: vi.fn() })),
}));
vi.mock('xterm-addon-search', () => ({
  SearchAddon: vi.fn(() => ({ findNext: vi.fn(), findPrevious: vi.fn(), clearDecorations: vi.fn() })),
}));

const mockedApi = vi.mocked(api);

// 各组件的中文冒烟断言:每个组件验证一个代表性文本
describe('组件中文冒烟', () => {
  beforeEach(() => {
    window.localStorage.setItem('vibe.lang', 'zh');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    window.localStorage.clear();
    document.documentElement.lang = 'en';
  });

  it('DeviceList 显示中文', () => {
    render(
      <LanguageProvider>
        <DeviceList
          devices={[{ id: 'dev-1', name: 'MacBook', platform: 'darwin', online: true }]}
          onCreateSession={vi.fn()}
        />
      </LanguageProvider>
    );
    expect(screen.getByRole('heading', { name: '设备' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '新建终端' })).toBeInTheDocument();
    expect(screen.getByText('在线')).toBeInTheDocument();
  });

  it('TerminalTabs 空状态显示中文', () => {
    render(
      <LanguageProvider>
        <TerminalTabs
          sessions={[]}
          onSessionsChange={vi.fn()}
          onCloseSession={vi.fn()}
          onRenameSession={vi.fn()}
        />
      </LanguageProvider>
    );
    expect(screen.getByText('没有打开的终端会话')).toBeInTheDocument();
  });

  it('SnippetsBar 显示中文', () => {
    render(
      <LanguageProvider>
        <SnippetsBar onInsert={vi.fn()} />
      </LanguageProvider>
    );
    expect(screen.getByRole('button', { name: '快捷命令' })).toBeInTheDocument();
  });

  it('TerminalSearchBar 显示中文', () => {
    render(
      <LanguageProvider>
        <TerminalSearchBar onSearch={vi.fn()} onClose={vi.fn()} />
      </LanguageProvider>
    );
    expect(screen.getByPlaceholderText('搜索')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '关闭搜索' })).toBeInTheDocument();
  });

  it('TerminalPane 显示中文', () => {
    // 惰性 WebSocket 桩:不发起真实连接,避免 jsdom 网络噪声
    vi.stubGlobal(
      'WebSocket',
      class {
        static OPEN = 1;
        readyState = 0;
        send() {}
        close() {}
        addEventListener() {}
      }
    );
    render(
      <LanguageProvider>
        <TerminalPane sessionId="sess-1" readOnly={false} />
      </LanguageProvider>
    );
    expect(screen.getByRole('button', { name: '搜索终端输出' })).toBeInTheDocument();
  });

  it('FileManagerPanel 显示中文', async () => {
    mockedApi.listDeviceFiles.mockResolvedValue({ path: '/home', entries: [] });
    render(
      <LanguageProvider>
        <FileManagerPanel
          device={{ id: 'dev-1', name: 'MacBook', platform: 'darwin', online: true }}
          onClose={vi.fn()}
        />
      </LanguageProvider>
    );
    expect(screen.getByRole('dialog', { name: 'MacBook 上的文件' })).toBeInTheDocument();
    expect(await screen.findByText('空目录')).toBeInTheDocument();
  });
});
