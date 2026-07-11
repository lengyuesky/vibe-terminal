# 前端设置页、中英文切换与移动端布局 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `web/` 前端新增"设置"页(内含 2FA 安全分区与语言切换),实现全 UI 中英文国际化,并为移动端(≤760px)提供底部标签栏布局。

**Architecture:** 自研轻量 i18n(React Context + 中英扁平字典 + `useT()` hook,TypeScript 编译期保证字典对齐);新建 `SettingsView` 容器组件(通用/安全两个子分区,安全分区复用现有懒加载 SecurityView);`App.tsx` 用 `matchMedia` 驱动移动端底部 tab bar,桌面端布局不变。

**Tech Stack:** React 18 + TypeScript + Vite + Vitest + @testing-library/react + lucide-react。无新增第三方依赖。

**规格文档:** `docs/superpowers/specs/2026-07-12-frontend-settings-i18n-mobile-design.md`

## Global Constraints

- 不引入任何新 npm 依赖(不用 react-i18next)
- 英文字典值必须与现有界面文案**逐字一致**,保证现有英文测试断言不破坏
- 语言偏好存 `localStorage['vibe.lang']`;首次访问 `navigator.language` 以 `zh` 开头选中文,否则英文
- 不改后端;不改桌面端(>760px)布局
- 玻璃拟态风格延续:复用 `--glass-*` CSS 变量
- 所有命令在 `web/` 目录下执行;测试命令 `npx vitest run <file>`;全量 `npx vitest run`
- 每个任务完成后 `npx tsc --noEmit` 必须通过(注意:`npm run build` 中的 `tsc` 同样会做全量类型检查)
- 提交信息用英文 conventional commits;代码注释用中文

---

### Task 1: i18n 基础模块

**Files:**
- Create: `web/src/i18n/en.ts`
- Create: `web/src/i18n/zh.ts`
- Create: `web/src/i18n/index.tsx`
- Modify: `web/src/main.tsx`
- Test: `web/src/test/i18n.test.tsx`

**Interfaces:**
- Consumes: 无(基础模块)
- Produces(后续所有任务依赖):
  - `type Lang = 'en' | 'zh'`
  - `type TranslationKey`(en 字典 key 的联合类型)、`type Translation = Record<TranslationKey, string>`
  - `type TFunction = (key: TranslationKey, params?: Record<string, string | number>) => string`
  - `function detectInitialLang(): Lang`
  - `function translate(lang: Lang, key: TranslationKey, params?): string`
  - `function LanguageProvider({ children }): JSX.Element`
  - `function useT(): { t: TFunction; lang: Lang }`
  - `function useLang(): { lang: Lang; setLang: (lang: Lang) => void }`
  - 无 Provider 时 `useT`/`useLang` 降级为英文 + noop `setLang`(现有测试无需包 Provider)

- [ ] **Step 1: 写失败测试**

创建 `web/src/test/i18n.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LanguageProvider, detectInitialLang, translate, useLang, useT } from '../i18n';

// 探针组件:暴露当前语言、一段翻译文本和切换按钮
function LangProbe() {
  const { t } = useT();
  const { lang, setLang } = useLang();
  return (
    <div>
      <span data-testid="lang">{lang}</span>
      <span data-testid="text">{t('nav.settings')}</span>
      <button type="button" onClick={() => setLang('zh')}>to-zh</button>
    </div>
  );
}

describe('i18n', () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
    document.documentElement.lang = 'en';
  });

  it('localStorage 中的语言优先于浏览器语言', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('en-US');
    expect(detectInitialLang()).toBe('zh');
  });

  it('无存储时浏览器语言 zh-CN 检测为中文', () => {
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('zh-CN');
    expect(detectInitialLang()).toBe('zh');
  });

  it('无存储且非中文浏览器语言兜底为英文', () => {
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('fr-FR');
    expect(detectInitialLang()).toBe('en');
  });

  it('localStorage 值非法时忽略并按浏览器语言检测', () => {
    window.localStorage.setItem('vibe.lang', 'jp');
    vi.spyOn(window.navigator, 'language', 'get').mockReturnValue('zh-CN');
    expect(detectInitialLang()).toBe('zh');
  });

  it('translate 支持 {name} 插值', () => {
    expect(translate('zh', 'security.recoveryRemaining', { count: 3 })).toBe('剩余 3 个恢复码。');
    expect(translate('en', 'security.recoveryRemaining', { count: 3 })).toBe('3 recovery codes remaining.');
  });

  it('Provider 内切换语言即时生效并持久化', async () => {
    render(
      <LanguageProvider>
        <LangProbe />
      </LanguageProvider>
    );
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
    expect(screen.getByTestId('text')).toHaveTextContent('Settings');
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('zh');
    expect(screen.getByTestId('text')).toHaveTextContent('设置');
    expect(window.localStorage.getItem('vibe.lang')).toBe('zh');
    expect(document.documentElement.lang).toBe('zh-CN');
  });

  it('无 Provider 时降级为英文且 setLang 不抛错', async () => {
    render(<LangProbe />);
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
    expect(screen.getByTestId('text')).toHaveTextContent('Settings');
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('en');
  });

  it('localStorage 写入异常时切换仍生效(仅当次会话)', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded');
    });
    render(
      <LanguageProvider>
        <LangProbe />
      </LanguageProvider>
    );
    await userEvent.click(screen.getByRole('button', { name: 'to-zh' }));
    expect(screen.getByTestId('lang')).toHaveTextContent('zh');
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/djy/xiangmu/clouldcode/web && npx vitest run src/test/i18n.test.tsx`
Expected: FAIL(无法解析 `../i18n` 模块)

- [ ] **Step 3: 创建英文字典 `web/src/i18n/en.ts`**

注意:值必须与现有界面文案逐字一致。

```ts
// 英文字典:作为全部翻译 key 的类型基准。
// 注意:值必须与既有界面英文文案逐字一致,保证现有测试断言不受影响。
export const en = {
  'common.cancel': 'Cancel',
  'common.save': 'Save',
  'common.add': 'Add',
  'common.delete': 'Delete',
  'common.confirm': 'Confirm',
  'common.refresh': 'Refresh',
  'common.copy': 'Copy',
  'common.copied': 'Copied',
  'common.copyFailed': 'Copy failed',
  'common.loading': 'Loading...',
  'common.done': 'Done',
  'common.continue': 'Continue',

  'nav.primary': 'Primary',
  'nav.terminals': 'Terminals',
  'nav.agentTokens': 'Agent Tokens',
  'nav.settings': 'Settings',
  'nav.devices': 'Devices',

  'login.username': 'Username',
  'login.password': 'Password',
  'login.twoFactorTitle': 'Two-factor authentication',
  'login.authenticatorHint': 'Enter the code from your authenticator app.',
  'login.recoveryHint': 'Enter one of your saved recovery codes.',
  'login.authenticatorCode': 'Authenticator code',
  'login.recoveryCode': 'Recovery code',
  'login.useAuthenticator': 'Use an authenticator code',
  'login.useRecovery': 'Use a recovery code',
  'login.back': 'Back to login',
  'login.submit': 'Login',
  'login.verify': 'Verify',
  'login.failed': 'login failed',
  'login.twoFactorFailed': 'two-factor verification failed',
  'login.languageLabel': 'Language / 语言',

  'settings.title': 'Settings',
  'settings.tabGeneral': 'General',
  'settings.tabSecurity': 'Security',
  'settings.language': 'Language',
  'settings.languageHint': 'Choose the interface language.',

  'security.title': 'Two-factor security',
  'security.loadingLabel': 'Loading security settings',
  'security.loadingText': 'Loading security settings...',
  'security.loadFailed': 'Security settings failed to load.',
  'security.retryLoad': 'Retry loading security settings',
  'security.retryStatus': 'Retry loading status',
  'security.statusEnabled': 'Two-factor authentication is enabled.',
  'security.statusDisabled': 'Two-factor authentication is disabled.',
  'security.recoveryRemaining': '{count} recovery codes remaining.',
  'security.regenerate': 'Regenerate recovery codes',
  'security.disable': 'Disable two-factor authentication',
  'security.enable': 'Enable two-factor authentication',
  'security.currentPassword': 'Current password',
  'security.authenticatorCode': 'Authenticator code',
  'security.scanQr': 'Scan the QR code',
  'security.qrLabel': 'Two-factor setup QR code',
  'security.setupExpires': 'Setup expires {time}',
  'security.saveRecovery': 'Save your recovery codes',
  'security.recoveryNote': 'These recovery codes are shown only once. Store them safely before continuing.',
  'security.copyRecovery': 'Copy recovery codes',
  'security.downloadRecovery': 'Download recovery codes',
  'security.disableWarning': 'This removes all recovery codes.',
  'security.confirmDisable': 'Confirm disable two-factor authentication',
  'security.errStatus': 'Failed to load two-factor status.',
  'security.errSetup': 'Failed to start two-factor setup.',
  'security.errEnable': 'Failed to enable two-factor authentication.',
  'security.errRegenerate': 'Failed to regenerate recovery codes.',
  'security.errDisable': 'Failed to disable two-factor authentication.',
  'security.errCopy': 'Failed to copy recovery codes.',
  'security.errDownload': 'Failed to download recovery codes.',

  'tokens.title': 'Agent Tokens',
  'tokens.create': 'Create token',
  'tokens.name': 'Token name',
  'tokens.ttl': 'TTL hours',
  'tokens.createButton': 'Create',
  'tokens.token': 'Token',
  'tokens.agentCommand': 'Agent command',
  'tokens.list': 'Tokens',
  'tokens.empty': 'No agent tokens yet.',
  'tokens.colName': 'Name',
  'tokens.colStatus': 'Status',
  'tokens.colCreated': 'Created',
  'tokens.colExpires': 'Expires',
  'tokens.colUsed': 'Used',
  'tokens.colRevoked': 'Revoked',
  'tokens.colAction': 'Action',
  'tokens.statusAvailable': 'available',
  'tokens.statusUsed': 'used',
  'tokens.statusExpired': 'expired',
  'tokens.statusRevoked': 'revoked',
  'tokens.revoke': 'Revoke',
  'tokens.confirmDelete': 'Confirm delete',
  'tokens.errLoad': 'Failed to load agent tokens.',
  'tokens.errCreate': 'Failed to create agent token.',
  'tokens.errRevoke': 'Failed to revoke agent token.',
  'tokens.errDelete': 'Failed to delete agent token.',

  'devices.title': 'Devices',
  'devices.name': 'Device name',
  'devices.saveName': 'Save device name',
  'devices.cancelRename': 'Cancel rename {name}',
  'devices.rename': 'Rename {name}',
  'devices.browseFiles': 'Browse files on {name}',
  'devices.newTerminal': 'New terminal',
  'devices.online': 'online',
  'devices.offline': 'offline',

  'sessions.empty': 'No terminal session open',
  'sessions.title': 'Session title',
  'sessions.rename': 'Rename {label}',
  'sessions.confirmDelete': 'Confirm delete {label}',
  'sessions.cancelDelete': 'Cancel delete {label}',
  'sessions.delete': 'Delete {label}',
  'sessions.meta': 'session {id}',
  'sessions.unknownDevice': 'Unknown device',
  'sessions.statusRunning': 'running',
  'sessions.statusStarting': 'starting',
  'sessions.statusLost': 'lost',
  'sessions.statusExited': 'exited',
  'sessions.statusClosed': 'closed',

  'snippets.toggle': 'Quick commands',
  'snippets.errLoad': 'Failed to load snippets.',
  'snippets.empty': 'No snippets yet',
  'snippets.insert': 'Insert {name}',
  'snippets.edit': 'Edit {name}',
  'snippets.delete': 'Delete {name}',
  'snippets.namePlaceholder': 'Name',
  'snippets.commandPlaceholder': 'Command',
  'snippets.nameLabel': 'Snippet name',
  'snippets.commandLabel': 'Snippet command',
  'snippets.errSave': 'Failed to save snippet.',
  'snippets.errDelete': 'Failed to delete snippet.',
  'snippets.hint': 'Click a snippet to type it into the active terminal, then press Enter yourself.',

  'search.placeholder': 'Search',
  'search.terminal': 'Search terminal',
  'search.matchCase': 'Match case',
  'search.previous': 'Previous match',
  'search.next': 'Next match',
  'search.close': 'Close search',
  'search.output': 'Search terminal output',

  'files.dialog': 'Files on {name}',
  'files.title': 'Files',
  'files.close': 'Close file manager',
  'files.parent': 'Parent directory',
  'files.path': 'Current path',
  'files.upload': 'Upload',
  'files.uploadFile': 'Upload file',
  'files.open': 'Open {name}',
  'files.download': 'Download {name}',
  'files.empty': 'Empty directory',
  'files.errList': 'Failed to list directory',
  'files.errUpload': 'Upload failed',
  'files.overwrite': '{name} already exists. Overwrite?',
};

export type TranslationKey = keyof typeof en;
export type Translation = Record<TranslationKey, string>;
```

- [ ] **Step 4: 创建中文字典 `web/src/i18n/zh.ts`**

```ts
import type { Translation } from './en';

// 中文字典:key 必须与英文字典完全一致,由 Translation 类型在编译期保证。
export const zh: Translation = {
  'common.cancel': '取消',
  'common.save': '保存',
  'common.add': '添加',
  'common.delete': '删除',
  'common.confirm': '确认',
  'common.refresh': '刷新',
  'common.copy': '复制',
  'common.copied': '已复制',
  'common.copyFailed': '复制失败',
  'common.loading': '加载中...',
  'common.done': '完成',
  'common.continue': '继续',

  'nav.primary': '主导航',
  'nav.terminals': '终端',
  'nav.agentTokens': 'Agent 令牌',
  'nav.settings': '设置',
  'nav.devices': '设备',

  'login.username': '用户名',
  'login.password': '密码',
  'login.twoFactorTitle': '双因素认证',
  'login.authenticatorHint': '输入验证器应用中的动态码。',
  'login.recoveryHint': '输入你保存的任一恢复码。',
  'login.authenticatorCode': '验证器动态码',
  'login.recoveryCode': '恢复码',
  'login.useAuthenticator': '使用验证器动态码',
  'login.useRecovery': '使用恢复码',
  'login.back': '返回登录',
  'login.submit': '登录',
  'login.verify': '验证',
  'login.failed': '登录失败',
  'login.twoFactorFailed': '双因素验证失败',
  'login.languageLabel': 'Language / 语言',

  'settings.title': '设置',
  'settings.tabGeneral': '通用',
  'settings.tabSecurity': '安全',
  'settings.language': '语言',
  'settings.languageHint': '选择界面显示语言。',

  'security.title': '双因素安全',
  'security.loadingLabel': '正在加载安全设置',
  'security.loadingText': '正在加载安全设置...',
  'security.loadFailed': '安全设置加载失败。',
  'security.retryLoad': '重试加载安全设置',
  'security.retryStatus': '重试加载状态',
  'security.statusEnabled': '双因素认证已启用。',
  'security.statusDisabled': '双因素认证已停用。',
  'security.recoveryRemaining': '剩余 {count} 个恢复码。',
  'security.regenerate': '重新生成恢复码',
  'security.disable': '停用双因素认证',
  'security.enable': '启用双因素认证',
  'security.currentPassword': '当前密码',
  'security.authenticatorCode': '验证器动态码',
  'security.scanQr': '扫描二维码',
  'security.qrLabel': '双因素设置二维码',
  'security.setupExpires': '设置有效期至 {time}',
  'security.saveRecovery': '保存你的恢复码',
  'security.recoveryNote': '恢复码仅显示一次,请先妥善保存再继续。',
  'security.copyRecovery': '复制恢复码',
  'security.downloadRecovery': '下载恢复码',
  'security.disableWarning': '这将删除所有恢复码。',
  'security.confirmDisable': '确认停用双因素认证',
  'security.errStatus': '加载双因素状态失败。',
  'security.errSetup': '启动双因素设置失败。',
  'security.errEnable': '启用双因素认证失败。',
  'security.errRegenerate': '重新生成恢复码失败。',
  'security.errDisable': '停用双因素认证失败。',
  'security.errCopy': '复制恢复码失败。',
  'security.errDownload': '下载恢复码失败。',

  'tokens.title': 'Agent 令牌',
  'tokens.create': '创建令牌',
  'tokens.name': '令牌名称',
  'tokens.ttl': '有效期(小时)',
  'tokens.createButton': '创建',
  'tokens.token': '令牌',
  'tokens.agentCommand': 'Agent 命令',
  'tokens.list': '令牌列表',
  'tokens.empty': '暂无 Agent 令牌。',
  'tokens.colName': '名称',
  'tokens.colStatus': '状态',
  'tokens.colCreated': '创建时间',
  'tokens.colExpires': '过期时间',
  'tokens.colUsed': '使用时间',
  'tokens.colRevoked': '吊销时间',
  'tokens.colAction': '操作',
  'tokens.statusAvailable': '可用',
  'tokens.statusUsed': '已使用',
  'tokens.statusExpired': '已过期',
  'tokens.statusRevoked': '已吊销',
  'tokens.revoke': '吊销',
  'tokens.confirmDelete': '确认删除',
  'tokens.errLoad': '加载 Agent 令牌失败。',
  'tokens.errCreate': '创建 Agent 令牌失败。',
  'tokens.errRevoke': '吊销 Agent 令牌失败。',
  'tokens.errDelete': '删除 Agent 令牌失败。',

  'devices.title': '设备',
  'devices.name': '设备名称',
  'devices.saveName': '保存设备名称',
  'devices.cancelRename': '取消重命名 {name}',
  'devices.rename': '重命名 {name}',
  'devices.browseFiles': '浏览 {name} 上的文件',
  'devices.newTerminal': '新建终端',
  'devices.online': '在线',
  'devices.offline': '离线',

  'sessions.empty': '没有打开的终端会话',
  'sessions.title': '会话标题',
  'sessions.rename': '重命名 {label}',
  'sessions.confirmDelete': '确认删除 {label}',
  'sessions.cancelDelete': '取消删除 {label}',
  'sessions.delete': '删除 {label}',
  'sessions.meta': '会话 {id}',
  'sessions.unknownDevice': '未知设备',
  'sessions.statusRunning': '运行中',
  'sessions.statusStarting': '启动中',
  'sessions.statusLost': '已断开',
  'sessions.statusExited': '已退出',
  'sessions.statusClosed': '已关闭',

  'snippets.toggle': '快捷命令',
  'snippets.errLoad': '加载快捷命令失败。',
  'snippets.empty': '暂无快捷命令',
  'snippets.insert': '插入 {name}',
  'snippets.edit': '编辑 {name}',
  'snippets.delete': '删除 {name}',
  'snippets.namePlaceholder': '名称',
  'snippets.commandPlaceholder': '命令',
  'snippets.nameLabel': '快捷命令名称',
  'snippets.commandLabel': '快捷命令内容',
  'snippets.errSave': '保存快捷命令失败。',
  'snippets.errDelete': '删除快捷命令失败。',
  'snippets.hint': '点击快捷命令可将其输入到当前终端,然后自行按回车执行。',

  'search.placeholder': '搜索',
  'search.terminal': '搜索终端',
  'search.matchCase': '区分大小写',
  'search.previous': '上一个匹配',
  'search.next': '下一个匹配',
  'search.close': '关闭搜索',
  'search.output': '搜索终端输出',

  'files.dialog': '{name} 上的文件',
  'files.title': '文件',
  'files.close': '关闭文件管理器',
  'files.parent': '上级目录',
  'files.path': '当前路径',
  'files.upload': '上传',
  'files.uploadFile': '上传文件',
  'files.open': '打开 {name}',
  'files.download': '下载 {name}',
  'files.empty': '空目录',
  'files.errList': '列出目录失败',
  'files.errUpload': '上传失败',
  'files.overwrite': '{name} 已存在,是否覆盖?',
};
```

- [ ] **Step 5: 创建 `web/src/i18n/index.tsx`**

```tsx
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { en } from './en';
import type { Translation, TranslationKey } from './en';
import { zh } from './zh';

export type Lang = 'en' | 'zh';
export type TFunction = (key: TranslationKey, params?: Record<string, string | number>) => string;
export type { Translation, TranslationKey };

const STORAGE_KEY = 'vibe.lang';
const dictionaries: Record<Lang, Translation> = { en, zh };

function readStoredLang(): Lang | null {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === 'en' || value === 'zh' ? value : null;
  } catch {
    // localStorage 不可用(如隐私模式)时忽略
    return null;
  }
}

// 初始语言判定:localStorage 优先,其次浏览器语言,兜底英文
export function detectInitialLang(): Lang {
  const stored = readStoredLang();
  if (stored) return stored;
  const language = typeof navigator !== 'undefined' ? navigator.language : 'en';
  return language?.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

function htmlLang(lang: Lang): string {
  return lang === 'zh' ? 'zh-CN' : 'en';
}

// 翻译并做 {name} 占位符插值;未提供的占位符原样保留
export function translate(lang: Lang, key: TranslationKey, params?: Record<string, string | number>): string {
  const template = dictionaries[lang][key] ?? en[key] ?? key;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match
  );
}

type LanguageContextValue = {
  lang: Lang;
  setLang: (lang: Lang) => void;
};

const LanguageContext = createContext<LanguageContextValue | null>(null);

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(detectInitialLang);

  // 同步 <html lang>,便于辅助技术与字体渲染
  useEffect(() => {
    document.documentElement.lang = htmlLang(lang);
  }, [lang]);

  const setLang = useCallback((next: Lang) => {
    setLangState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // 写入失败时仅当次会话生效
    }
  }, []);

  const value = useMemo(() => ({ lang, setLang }), [lang, setLang]);
  return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>;
}

// 无 Provider 时降级为英文 + noop,让单测无需强制包裹
export function useLang(): LanguageContextValue {
  return useContext(LanguageContext) ?? { lang: 'en', setLang: () => undefined };
}

export function useT(): { t: TFunction; lang: Lang } {
  const { lang } = useLang();
  const t = useCallback<TFunction>((key, params) => translate(lang, key, params), [lang]);
  return { t, lang };
}
```

- [ ] **Step 6: 更新 `web/src/main.tsx`**

```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import { LanguageProvider } from './i18n';
import './styles.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <LanguageProvider>
      <App />
    </LanguageProvider>
  </React.StrictMode>
);
```

- [ ] **Step 7: 运行测试确认通过**

Run: `npx vitest run src/test/i18n.test.tsx`
Expected: PASS(8 个用例全绿)

- [ ] **Step 8: 类型检查与全量回归**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过(现有测试不受影响)

- [ ] **Step 9: 提交**

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/i18n web/src/main.tsx web/src/test/i18n.test.tsx
git commit -m "feat(web): add i18n foundation with zh/en dictionaries"
```

---

### Task 2: SettingsView 设置容器组件

**Files:**
- Create: `web/src/components/SettingsView.tsx`
- Modify: `web/src/styles.css`(追加设置页样式)
- Test: `web/src/test/SettingsView.test.tsx`

**Interfaces:**
- Consumes: Task 1 的 `useT` / `useLang`
- Produces(Task 3 依赖):
  - `type SecurityViewProps = { onRecoveryDeliveryLockChange?: (locked: boolean) => void }`
  - `type SecurityLoader = () => Promise<{ default: ComponentType<SecurityViewProps> }>`
  - `const defaultSecurityLoader: SecurityLoader`
  - `function SettingsView(props: { securityLoader?: SecurityLoader; onRecoveryDeliveryLockChange: (locked: boolean) => void; deliveryLocked: boolean }): JSX.Element`
- 说明:`SecurityPanel`/`SecurityErrorBoundary` 逻辑从 `App.tsx` **复制**到本文件(Task 3 再删除 App.tsx 中的原件)

- [ ] **Step 1: 写失败测试**

创建 `web/src/test/SettingsView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LanguageProvider } from '../i18n';
import { SettingsView } from '../components/SettingsView';
import type { SecurityLoader } from '../components/SettingsView';

// 安全分区懒加载 stub:渲染一个可断言的标题
const stubSecurityLoader: SecurityLoader = async () => ({
  default: () => (
    <section aria-labelledby="security-title">
      <h1 id="security-title">Two-factor security</h1>
    </section>
  ),
});

function renderSettings(overrides: Partial<Parameters<typeof SettingsView>[0]> = {}) {
  return render(
    <LanguageProvider>
      <SettingsView
        securityLoader={stubSecurityLoader}
        onRecoveryDeliveryLockChange={vi.fn()}
        deliveryLocked={false}
        {...overrides}
      />
    </LanguageProvider>
  );
}

afterEach(() => {
  window.localStorage.clear();
  document.documentElement.lang = 'en';
});

describe('SettingsView', () => {
  it('默认显示通用分区和语言选项', () => {
    renderSettings();
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'General', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Security', selected: false })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '中文' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'English' })).toBeInTheDocument();
  });

  it('切换到安全分区后懒加载 SecurityView', async () => {
    renderSettings();
    await userEvent.click(screen.getByRole('tab', { name: 'Security' }));
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
  });

  it('点击中文立即切换界面语言并持久化', async () => {
    renderSettings();
    await userEvent.click(screen.getByRole('button', { name: '中文' }));
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '通用' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '安全' })).toBeInTheDocument();
    expect(window.localStorage.getItem('vibe.lang')).toBe('zh');
    expect(document.documentElement.lang).toBe('zh-CN');
  });

  it('当前语言选项标记为按下状态', async () => {
    renderSettings();
    expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: '中文' }));
    expect(screen.getByRole('button', { name: '中文' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('deliveryLocked 时禁用分区切换', () => {
    renderSettings({ deliveryLocked: true });
    expect(screen.getByRole('tab', { name: 'General' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'Security' })).toBeDisabled();
  });

  it('安全分包加载失败时显示错误并可重试', async () => {
    let fail = true;
    const loader: SecurityLoader = vi.fn(async () => {
      if (fail) throw new Error('chunk load failed');
      return stubSecurityLoader();
    });
    renderSettings({ securityLoader: loader });
    await userEvent.click(screen.getByRole('tab', { name: 'Security' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Security settings failed to load.');
    fail = false;
    await userEvent.click(screen.getByRole('button', { name: 'Retry loading security settings' }));
    expect(await screen.findByRole('heading', { name: 'Two-factor security' })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/SettingsView.test.tsx`
Expected: FAIL(无法解析 `../components/SettingsView`)

- [ ] **Step 3: 创建 `web/src/components/SettingsView.tsx`**

```tsx
import { Component, lazy, Suspense, useMemo, useState } from 'react';
import type { ComponentType, ErrorInfo, ReactNode } from 'react';
import { useLang, useT } from '../i18n';

export type SecurityViewProps = { onRecoveryDeliveryLockChange?: (locked: boolean) => void };
export type SecurityLoader = () => Promise<{ default: ComponentType<SecurityViewProps> }>;

export const defaultSecurityLoader: SecurityLoader = () =>
  import('./SecurityView').then((module) => ({ default: module.SecurityView }));

// 懒加载失败的可重试错误提示(函数组件才能使用 useT)
function SecurityLoadError({ onRetry }: { onRetry: () => void }) {
  const { t } = useT();
  return (
    <section className="securityLoading" role="alert">
      <p>{t('security.loadFailed')}</p>
      <button type="button" onClick={onRetry}>
        {t('security.retryLoad')}
      </button>
    </section>
  );
}

function SecurityLoading() {
  const { t } = useT();
  return (
    <p className="securityLoading" role="status" aria-label={t('security.loadingLabel')}>
      {t('security.loadingText')}
    </p>
  );
}

class SecurityErrorBoundary extends Component<
  { children: ReactNode; onRetry: () => void },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(_error: Error, _info: ErrorInfo) {}
  render() {
    if (this.state.failed) {
      return <SecurityLoadError onRetry={this.props.onRetry} />;
    }
    return this.props.children;
  }
}

export function SecurityPanel({
  loader,
  onRecoveryDeliveryLockChange,
}: {
  loader: SecurityLoader;
  onRecoveryDeliveryLockChange: (locked: boolean) => void;
}) {
  const [attempt, setAttempt] = useState(0);
  const LazySecurityView = useMemo(() => lazy(loader), [loader, attempt]);
  return (
    <SecurityErrorBoundary key={attempt} onRetry={() => setAttempt((value) => value + 1)}>
      <Suspense fallback={<SecurityLoading />}>
        <LazySecurityView onRecoveryDeliveryLockChange={onRecoveryDeliveryLockChange} />
      </Suspense>
    </SecurityErrorBoundary>
  );
}

type SettingsSection = 'general' | 'security';

export function SettingsView({
  securityLoader = defaultSecurityLoader,
  onRecoveryDeliveryLockChange,
  deliveryLocked,
}: {
  securityLoader?: SecurityLoader;
  onRecoveryDeliveryLockChange: (locked: boolean) => void;
  deliveryLocked: boolean;
}) {
  const { t } = useT();
  const { lang, setLang } = useLang();
  const [section, setSection] = useState<SettingsSection>('general');

  return (
    <main className="settingsPage">
      <h1>{t('settings.title')}</h1>
      <div role="tablist" className="settingsTabs" aria-label={t('settings.title')}>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'general'}
          className={section === 'general' ? 'active' : ''}
          disabled={deliveryLocked}
          onClick={() => setSection('general')}
        >
          {t('settings.tabGeneral')}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={section === 'security'}
          className={section === 'security' ? 'active' : ''}
          disabled={deliveryLocked}
          onClick={() => setSection('security')}
        >
          {t('settings.tabSecurity')}
        </button>
      </div>
      {section === 'general' ? (
        <section className="settingsPanel" aria-labelledby="language-setting-title">
          <h2 id="language-setting-title">{t('settings.language')}</h2>
          <p className="muted">{t('settings.languageHint')}</p>
          {/* 语言名称按惯例以各自语言原文显示,不随界面语言翻译 */}
          <div className="languageOptions" role="group" aria-label={t('settings.language')}>
            <button
              type="button"
              aria-pressed={lang === 'zh'}
              className={lang === 'zh' ? 'active' : ''}
              onClick={() => setLang('zh')}
            >
              中文
            </button>
            <button
              type="button"
              aria-pressed={lang === 'en'}
              className={lang === 'en' ? 'active' : ''}
              onClick={() => setLang('en')}
            >
              English
            </button>
          </div>
        </section>
      ) : (
        /* 复用 .securityPage 既有后代样式,settingsSecurityHost 去掉页面级留白 */
        <div className="securityPage settingsSecurityHost">
          <SecurityPanel loader={securityLoader} onRecoveryDeliveryLockChange={onRecoveryDeliveryLockChange} />
        </div>
      )}
    </main>
  );
}
```

- [ ] **Step 4: 在 `web/src/styles.css` 末尾追加设置页样式**

```css
/* ===== 设置页 ===== */
.settingsPage {
  min-width: 0;
  overflow: auto;
  padding: 28px;
  display: grid;
  align-content: start;
  gap: 18px;
}

.settingsPage > h1 {
  margin: 0;
  font-size: 22px;
  color: var(--text);
}

.settingsTabs {
  display: flex;
  gap: 8px;
}

.settingsTabs button {
  padding: 8px 18px;
  border-radius: var(--r-pill);
}

.settingsTabs button.active {
  background: var(--accent-soft);
  border-color: rgba(167, 139, 250, 0.45);
  color: #ddd6fe;
  box-shadow: 0 0 18px rgba(167, 139, 250, 0.18);
}

.settingsPanel {
  display: grid;
  align-content: start;
  gap: 14px;
  width: min(820px, 100%);
  padding: 24px;
  border: 1px solid var(--glass-border);
  border-radius: var(--r-card);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}

.settingsPanel h2 {
  margin: 0;
  font-size: 16px;
  color: var(--text);
}

.settingsPanel p {
  margin: 0;
}

.languageOptions {
  display: flex;
  gap: 10px;
}

.languageOptions button {
  min-width: 110px;
  justify-content: center;
}

.languageOptions button.active {
  background: var(--accent-soft);
  border-color: rgba(167, 139, 250, 0.45);
  color: #ddd6fe;
}

/* 设置页内嵌安全分区:复用 .securityPage 全部后代样式,去掉页面级留白 */
.securityPage.settingsSecurityHost {
  padding: 0;
  overflow: visible;
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/test/SettingsView.test.tsx`
Expected: PASS(6 个用例全绿;懒加载失败用例会打印 console.error,属预期)

- [ ] **Step 6: 类型检查**

Run: `npx tsc --noEmit`
Expected: 无错误(App.tsx 中的同名 SecurityPanel 仍在,但互不引用,不冲突)

- [ ] **Step 7: 提交**

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/components/SettingsView.tsx web/src/styles.css web/src/test/SettingsView.test.tsx
git commit -m "feat(web): add SettingsView with language and security sections"
```

---

### Task 3: App.tsx 集成设置页(Security 导航 → Settings)

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/test/App.test.tsx`

**Interfaces:**
- Consumes: Task 1 `useT`;Task 2 `SettingsView` / `SecurityLoader`
- Produces:
  - `ViewMode = 'terminals' | 'agentTokens' | 'settings' | 'devices'`(`'devices'` 本任务只加类型,渲染留给 Task 8)
  - `AppViewProps.securityLoader?: SecurityLoader`(类型改从 `./components/SettingsView` 导入)
  - 左侧导航:Terminals / Agent Tokens / Settings(齿轮图标)

- [ ] **Step 1: 更新 `web/src/test/App.test.tsx` 中的导航断言(先改测试)**

3 处修改模式:

(a) 文件顶部 `deferred` 函数之后添加 helper:

```tsx
// 通过 设置 → 安全 分区打开 2FA 界面(替代原独立 Security 导航项)
async function openSecuritySettings() {
  await userEvent.click(screen.getByRole('button', { name: 'Settings' }));
  await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
}
```

(b) 把所有 `await userEvent.click(screen.getByRole('button', { name: 'Security' }));` 替换为 `await openSecuritySettings();`(约 10 处,包括循环往返测试中第二次点击)。往返类测试(如"标记当前导航并在Security往返时保留终端和Token状态")中获取按钮引用的:

```tsx
const security = screen.getByRole('button', { name: 'Security' });
```
改为:
```tsx
const settings = screen.getByRole('button', { name: 'Settings' });
```
并把后续 `security` 变量引用改为 `settings`(`aria-current` 断言对象变为 Settings 按钮);原先"点击 security 按钮"的动作改为 `await userEvent.click(settings);` 后跟 `await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));`。

(c) 特例:
- `'未登录时不显示Security导航'` 用例:`queryByRole('button', { name: 'Security' })` → `queryByRole('button', { name: 'Settings' })`,用例名改为 `'未登录时不显示Settings导航'`
- `'启用请求和恢复码交付期间阻止离开Security，Done后恢复导航'` 用例:在两处 `expect(screen.getByRole('button', { name: 'Agent Tokens' })).toBeDisabled();` 附近各加一行:

```tsx
expect(screen.getByRole('tab', { name: 'General' })).toBeDisabled();
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/App.test.tsx`
Expected: FAIL(找不到 `Settings` 按钮——App 还是 `Security`)

- [ ] **Step 3: 修改 `web/src/App.tsx`**

(a) imports 区:

```tsx
import { KeyRound, Monitor, Settings, Terminal } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { AgentToken, CreatedAgentToken, Device, LoginResult, Session, User } from './api';
import * as api from './api';
import { AgentTokenManager } from './components/AgentTokenManager';
import { DeviceList } from './components/DeviceList';
import { FileManagerPanel } from './components/FileManagerPanel';
import { LoginView } from './components/LoginView';
import { SettingsView } from './components/SettingsView';
import type { SecurityLoader } from './components/SettingsView';
import { TerminalTabs } from './components/TerminalTabs';
import { useT } from './i18n';
```

(移除 `ShieldCheck`、`Component`/`lazy`/`Suspense` 及 `ComponentType`/`ErrorInfo`/`ReactNode` 类型导入。)

(b) `ViewMode` 类型:

```tsx
type ViewMode = 'terminals' | 'agentTokens' | 'settings' | 'devices';
```

(c) **删除** App.tsx 中的整段:`type SecurityViewProps`、`type SecurityLoader`、`defaultSecurityLoader`、`class SecurityErrorBoundary`、`function SecurityPanel`(已迁至 SettingsView.tsx)。

(d) `AuthenticatedAppView` 解构参数中 `securityLoader = defaultSecurityLoader` 改为 `securityLoader`(不设默认值,SettingsView 内部有默认);函数体开头加 `const { t } = useT();`;`viewMode` 初始值仍为 `'terminals'`。

(e) sideNav 三个按钮改为:

```tsx
<nav className="sideNav" aria-label={t('nav.primary')}>
  <button
    type="button"
    disabled={securityDeliveryLocked}
    aria-current={viewMode === 'terminals' ? 'page' : undefined}
    className={viewMode === 'terminals' ? 'active' : ''}
    onClick={() => setViewMode('terminals')}
  >
    <Monitor size={16} aria-hidden="true" />
    {t('nav.terminals')}
  </button>
  <button
    type="button"
    disabled={securityDeliveryLocked}
    aria-current={viewMode === 'agentTokens' ? 'page' : undefined}
    className={viewMode === 'agentTokens' ? 'active' : ''}
    onClick={() => setViewMode('agentTokens')}
  >
    <KeyRound size={16} aria-hidden="true" />
    {t('nav.agentTokens')}
  </button>
  <button
    type="button"
    aria-current={viewMode === 'settings' ? 'page' : undefined}
    className={viewMode === 'settings' ? 'active' : ''}
    onClick={() => setViewMode('settings')}
  >
    <Settings size={16} aria-hidden="true" />
    {t('nav.settings')}
  </button>
</nav>
```

(f) 原 `{viewMode === 'security' && (<main className="securityPage">…</main>)}` 整块替换为:

```tsx
{viewMode === 'settings' && (
  <SettingsView
    securityLoader={securityLoader}
    onRecoveryDeliveryLockChange={setSecurityDeliveryLocked}
    deliveryLocked={securityDeliveryLocked}
  />
)}
```

(g) `AppViewProps` 中 `securityLoader?: SecurityLoader;` 保留(类型来源已改为 import)。

- [ ] **Step 4: 运行 App 测试确认通过**

Run: `npx vitest run src/test/App.test.tsx`
Expected: PASS(全部用例,包括锁定导航、懒加载失败重试、往返保留状态)

- [ ] **Step 5: 类型检查与全量回归**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过

- [ ] **Step 6: 提交**

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/App.tsx web/src/test/App.test.tsx
git commit -m "feat(web): move security into settings navigation"
```

---

### Task 4: LoginView 国际化 + 登录页语言切换

**Files:**
- Modify: `web/src/components/LoginView.tsx`
- Modify: `web/src/styles.css`(追加语言切换按钮样式)
- Test: `web/src/test/LoginView.test.tsx`(追加用例)

**Interfaces:**
- Consumes: Task 1 `useT` / `useLang`
- Produces: 登录页右上角固定定位的语言切换(`中文` / `EN` 两个按钮,`aria-pressed` 标记当前项)

- [ ] **Step 1: 在 `web/src/test/LoginView.test.tsx` 追加失败测试**

在文件已有 imports 基础上补充 `import { LanguageProvider } from '../i18n';`,文件末尾追加:

```tsx
describe('登录页语言切换', () => {
  afterEach(() => {
    window.localStorage.clear();
    document.documentElement.lang = 'en';
  });

  it('默认英文,点击中文后界面即时切换', async () => {
    render(
      <LanguageProvider>
        <LoginView onLogin={vi.fn()} onVerifyTwoFactor={vi.fn()} />
      </LanguageProvider>
    );
    expect(screen.getByRole('button', { name: 'Login' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '中文' }));
    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument();
    expect(screen.getByLabelText('用户名')).toBeInTheDocument();
    expect(screen.getByLabelText('密码')).toBeInTheDocument();
    expect(window.localStorage.getItem('vibe.lang')).toBe('zh');
  });

  it('切回英文恢复原文案', async () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    render(
      <LanguageProvider>
        <LoginView onLogin={vi.fn()} onVerifyTwoFactor={vi.fn()} />
      </LanguageProvider>
    );
    expect(screen.getByRole('button', { name: '登录' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'EN' }));
    expect(screen.getByRole('button', { name: 'Login' })).toBeInTheDocument();
  });
});
```

(若文件未导入 `afterEach`/`describe`,在顶部 vitest import 中补上。)

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/LoginView.test.tsx`
Expected: FAIL(找不到 `中文` 按钮)

- [ ] **Step 3: 修改 `web/src/components/LoginView.tsx`**

(a) imports 补充:

```tsx
import { useLang, useT } from '../i18n';
```

(b) 组件顶部(state 声明之前)加:

```tsx
const { t } = useT();
```

(c) 新增语言切换子组件(文件内、`LoginView` 之前):

```tsx
// 登录页右上角语言切换:未登录用户也能切换界面语言
function LoginLanguageSwitch() {
  const { t } = useT();
  const { lang, setLang } = useLang();
  return (
    <div className="loginLangSwitch" role="group" aria-label={t('login.languageLabel')}>
      <button
        type="button"
        aria-pressed={lang === 'zh'}
        className={lang === 'zh' ? 'active' : ''}
        onClick={() => setLang('zh')}
      >
        中文
      </button>
      <button
        type="button"
        aria-pressed={lang === 'en'}
        className={lang === 'en' ? 'active' : ''}
        onClick={() => setLang('en')}
      >
        EN
      </button>
    </div>
  );
}
```

(d) JSX 文本替换(`<main className="login">` 打开标签后第一行插入 `<LoginLanguageSwitch />`):

| 原文 | 替换为 |
|------|--------|
| `Username`(label 文本) | `{t('login.username')}` |
| `Password`(label 文本) | `{t('login.password')}` |
| `<h2>Two-factor authentication</h2>` | `<h2>{t('login.twoFactorTitle')}</h2>` |
| `'Enter one of your saved recovery codes.'` | `t('login.recoveryHint')` |
| `'Enter the code from your authenticator app.'` | `t('login.authenticatorHint')` |
| `{recoveryMode ? 'Recovery code' : 'Authenticator code'}` | `{recoveryMode ? t('login.recoveryCode') : t('login.authenticatorCode')}` |
| `{recoveryMode ? 'Use an authenticator code' : 'Use a recovery code'}` | `{recoveryMode ? t('login.useAuthenticator') : t('login.useRecovery')}` |
| `Back to login` | `{t('login.back')}` |
| `{step === 'password' ? 'Login' : 'Verify'}` | `{step === 'password' ? t('login.submit') : t('login.verify')}` |
| `errorMessage(caught, 'login failed')` | `errorMessage(caught, t('login.failed'))` |
| `errorMessage(caught, 'two-factor verification failed')` | `errorMessage(caught, t('login.twoFactorFailed'))` |

(e) `web/src/styles.css` 末尾追加:

```css
/* ===== 登录页语言切换 ===== */
.loginLangSwitch {
  position: fixed;
  top: 16px;
  right: 16px;
  display: flex;
  gap: 6px;
  z-index: 10;
}

.loginLangSwitch button {
  padding: 6px 12px;
  border-radius: var(--r-pill);
  font-size: 12px;
}

.loginLangSwitch button.active {
  background: var(--accent-soft);
  border-color: rgba(167, 139, 250, 0.45);
  color: #ddd6fe;
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/test/LoginView.test.tsx`
Expected: PASS(原有用例 + 新增 2 例;原有用例裸 render 无 Provider,useT 降级英文,不受影响)

- [ ] **Step 5: 类型检查与全量回归后提交**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/components/LoginView.tsx web/src/styles.css web/src/test/LoginView.test.tsx
git commit -m "feat(web): localize login view and add language switch"
```

---

### Task 5: SecurityView 国际化

**Files:**
- Modify: `web/src/components/SecurityView.tsx`
- Test: `web/src/test/SecurityView.test.tsx`(追加中文用例)

**Interfaces:**
- Consumes: Task 1 `useT`
- Produces: 无新接口(纯文本替换;组件签名不变)

- [ ] **Step 1: 在 `web/src/test/SecurityView.test.tsx` 追加失败的中文用例**

补充 import `LanguageProvider`(from `'../i18n'`),文件末尾追加(参照该文件已有的 api mock 方式;它 mock 了 `../api`,`getTwoFactorStatus` 可控):

```tsx
it('中文环境下概览界面显示中文文案', async () => {
  window.localStorage.setItem('vibe.lang', 'zh');
  mockedApi.getTwoFactorStatus.mockResolvedValue({ enabled: false, recoveryCodesRemaining: 0 });
  render(
    <LanguageProvider>
      <SecurityView />
    </LanguageProvider>
  );
  expect(await screen.findByRole('heading', { name: '双因素安全' })).toBeInTheDocument();
  expect(screen.getByText('双因素认证已停用。')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '启用双因素认证' })).toBeInTheDocument();
  window.localStorage.clear();
  document.documentElement.lang = 'en';
});
```

(若该文件的 api mock 变量名不同,按现有命名对齐;核心是 mock `getTwoFactorStatus` 返回未启用状态。)

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/SecurityView.test.tsx`
Expected: FAIL(找不到 `双因素安全` 标题)

- [ ] **Step 3: 修改 `web/src/components/SecurityView.tsx`**

(a) import 补 `import { useT } from '../i18n';`,组件函数体首行(state 之前)加 `const { t } = useT();`

(b) 逻辑区错误 fallback 替换(6 处,模式一致):

| 原文 | 替换为 |
|------|--------|
| `: 'Failed to load two-factor status.'` | `: t('security.errStatus')` |
| `: 'Failed to start two-factor setup.'` | `: t('security.errSetup')` |
| `: 'Failed to enable two-factor authentication.'` | `: t('security.errEnable')` |
| `: 'Failed to regenerate recovery codes.'` | `: t('security.errRegenerate')` |
| `: 'Failed to disable two-factor authentication.'` | `: t('security.errDisable')` |
| `setError('Failed to copy recovery codes.')` | `setError(t('security.errCopy'))` |
| `setError('Failed to download recovery codes.')` | `setError(t('security.errDownload'))` |

(`throw new Error('Clipboard is unavailable.')` 保持原样——该 message 仅用于中断流程,从不展示。)

(c) JSX 区替换:

| 原文 | 替换为 |
|------|--------|
| `Two-factor security`(h1 内) | `{t('security.title')}` |
| `Retry loading status` | `{t('security.retryStatus')}` |
| `Two-factor authentication is {status.enabled ? 'enabled' : 'disabled'}.` | `{status.enabled ? t('security.statusEnabled') : t('security.statusDisabled')}` |
| `{status.recoveryCodesRemaining} recovery codes remaining.` | `{t('security.recoveryRemaining', { count: status.recoveryCodesRemaining })}` |
| `Regenerate recovery codes`(按钮+h2,共 3 处) | `{t('security.regenerate')}` |
| `Disable two-factor authentication`(按钮+h2,共 2 处) | `{t('security.disable')}` |
| `Enable two-factor authentication`(2 处) | `{t('security.enable')}` |
| `Current password`(3 处 label) | `{t('security.currentPassword')}` |
| `Authenticator code`(2 处 label) | `{t('security.authenticatorCode')}` |
| `<h2>Scan the QR code</h2>` | `<h2>{t('security.scanQr')}</h2>` |
| `aria-label="Two-factor setup QR code"` | `aria-label={t('security.qrLabel')}` |
| `Setup expires {setup.expiresAt}` | `{t('security.setupExpires', { time: setup.expiresAt })}` |
| `Save your recovery codes` | `{t('security.saveRecovery')}` |
| `These recovery codes are shown only once. Store them safely before continuing.` | `{t('security.recoveryNote')}` |
| `Copy recovery codes` | `{t('security.copyRecovery')}` |
| `Download recovery codes` | `{t('security.downloadRecovery')}` |
| `Done` | `{t('common.done')}` |
| `This removes all recovery codes.` | `{t('security.disableWarning')}` |
| `Confirm disable two-factor authentication` | `{t('security.confirmDisable')}` |
| `Continue` | `{t('common.continue')}` |
| `Cancel`(4 处) | `{t('common.cancel')}` |

- [ ] **Step 4: 运行测试确认通过**

Run: `npx vitest run src/test/SecurityView.test.tsx src/test/App.test.tsx`
Expected: PASS(英文值逐字未变,现有断言全部保持;新增中文用例通过)

- [ ] **Step 5: 类型检查与提交**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/components/SecurityView.tsx web/src/test/SecurityView.test.tsx
git commit -m "feat(web): localize security view"
```

---

### Task 6: AgentTokenManager 国际化 + token 错误改类别

**Files:**
- Modify: `web/src/App.tsx`(`useAgentTokenState` 错误类型)
- Modify: `web/src/components/AgentTokenManager.tsx`
- Test: `web/src/test/AgentTokenManager.test.tsx`(新建)

**Interfaces:**
- Consumes: Task 1 `useT` / `TranslationKey`
- Produces:
  - `export type AgentTokenErrorKind = 'load' | 'create' | 'revoke' | 'delete'`(App.tsx 导出)
  - `AgentTokenState.error: AgentTokenErrorKind | null`;`AppViewProps.tokenError: AgentTokenErrorKind | null`
  - `AgentTokenManager` props `error: AgentTokenErrorKind | null`(渲染时映射到翻译 key)

- [ ] **Step 1: 新建失败测试 `web/src/test/AgentTokenManager.test.tsx`**

```tsx
import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentTokenManager } from '../components/AgentTokenManager';
import { LanguageProvider } from '../i18n';

function renderManager(overrides: Partial<Parameters<typeof AgentTokenManager>[0]> = {}) {
  return render(
    <LanguageProvider>
      <AgentTokenManager
        tokens={[]}
        loading={false}
        error={null}
        createdToken={null}
        onCreate={vi.fn()}
        onRevoke={vi.fn()}
        onDelete={vi.fn()}
        onRefresh={vi.fn().mockResolvedValue(undefined)}
        {...overrides}
      />
    </LanguageProvider>
  );
}

afterEach(() => {
  window.localStorage.clear();
  document.documentElement.lang = 'en';
});

describe('AgentTokenManager i18n', () => {
  it('中文环境显示中文文案', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    renderManager();
    expect(screen.getByRole('heading', { name: 'Agent 令牌' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '创建令牌' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '刷新' })).toBeInTheDocument();
    expect(screen.getByText('暂无 Agent 令牌。')).toBeInTheDocument();
  });

  it('错误类别渲染为本地化消息', () => {
    renderManager({ error: 'load' });
    expect(screen.getByText('Failed to load agent tokens.')).toBeInTheDocument();
  });

  it('令牌状态值本地化', () => {
    window.localStorage.setItem('vibe.lang', 'zh');
    renderManager({
      tokens: [
        {
          id: 'tok-0001-abcd',
          name: 'desk',
          created_at: '2026-07-01T00:00:00Z',
          expires_at: '2099-01-01T00:00:00Z',
        },
      ],
    });
    expect(screen.getByText('可用')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/AgentTokenManager.test.tsx`
Expected: FAIL(`error: 'load'` 类型不符/渲染为原字符串;中文文案缺失)

- [ ] **Step 3: 修改 `web/src/App.tsx` 的 `useAgentTokenState`**

(a) 类型与状态:

```tsx
// Agent 令牌操作失败的类别;由展示层映射为本地化消息
export type AgentTokenErrorKind = 'load' | 'create' | 'revoke' | 'delete';

type AgentTokenState = {
  userId: string | null;
  tokens: AgentToken[];
  createdToken: CreatedAgentToken | null;
  loading: boolean;
  error: AgentTokenErrorKind | null;
};
```

(b) `load` 中 `error: 'Failed to load agent tokens.'` → `error: 'load'`

(c) `mutate` 签名 `failure: string` → `failure: AgentTokenErrorKind`;调用处:
- `'Failed to create agent token.'` → `'create'`
- `'Failed to revoke agent token.'` → `'revoke'`
- `'Failed to delete agent token.'` → `'delete'`

(d) `AppViewProps.tokenError: string | null` → `tokenError: AgentTokenErrorKind | null`

- [ ] **Step 4: 修改 `web/src/components/AgentTokenManager.tsx`**

(a) imports:

```tsx
import { useT } from '../i18n';
import type { TranslationKey } from '../i18n';
import type { AgentTokenErrorKind } from '../App';
```

组件函数体首行加 `const { t } = useT();`,props 类型中 `error: string | null` → `error: AgentTokenErrorKind | null`。

(b) 文件顶部(组件外)加映射表:

```tsx
// 错误类别与令牌状态到翻译 key 的映射
const tokenErrorKeys: Record<AgentTokenErrorKind, TranslationKey> = {
  load: 'tokens.errLoad',
  create: 'tokens.errCreate',
  revoke: 'tokens.errRevoke',
  delete: 'tokens.errDelete',
};

const tokenStatusKeys: Record<TokenStatus, TranslationKey> = {
  available: 'tokens.statusAvailable',
  used: 'tokens.statusUsed',
  expired: 'tokens.statusExpired',
  revoked: 'tokens.statusRevoked',
};
```

(c) JSX 替换:

| 原文 | 替换为 |
|------|--------|
| `<h1>Agent Tokens</h1>` | `<h1>{t('tokens.title')}</h1>` |
| `Refresh` | `{t('common.refresh')}` |
| `Create token` | `{t('tokens.create')}` |
| `<span>Token name</span>`(表单 + newTokenSummary,2 处) | `<span>{t('tokens.name')}</span>` |
| `<span>TTL hours</span>` | `<span>{t('tokens.ttl')}</span>` |
| `Create`(提交按钮) | `{t('tokens.createButton')}` |
| `<span>Token</span>` | `<span>{t('tokens.token')}</span>` |
| `{copyTokenState === 'copied' ? 'Copied' : 'Copy'}`(2 处,另一处是 copyCommandState) | `{copyTokenState === 'copied' ? t('common.copied') : t('common.copy')}`(对应变量) |
| `Copy failed`(2 处) | `{t('common.copyFailed')}` |
| `<span>Agent command</span>` | `<span>{t('tokens.agentCommand')}</span>` |
| `<h2 id="token-list-title">Tokens</h2>` | `<h2 id="token-list-title">{t('tokens.list')}</h2>` |
| `Loading...` | `{t('common.loading')}` |
| `{error && <p className="error">{error}</p>}` | `{error && <p className="error">{t(tokenErrorKeys[error])}</p>}` |
| `No agent tokens yet.` | `{t('tokens.empty')}` |
| 表头 `Name/Status/Created/Expires/Used/Revoked/Action` | `{t('tokens.colName')}` 等 7 个 |
| `<span className={\`tokenStatus tokenStatus-${status}\`}>{status}</span>` | `<span className={\`tokenStatus tokenStatus-${status}\`}>{t(tokenStatusKeys[status])}</span>`(className 保留原始英文状态值) |
| `Confirm delete` | `{t('tokens.confirmDelete')}` |
| `Delete` | `{t('common.delete')}` |
| `Confirm` | `{t('common.confirm')}` |
| `Revoke` | `{t('tokens.revoke')}` |

(placeholder `office-mac` 与默认令牌名 `agent` 为示例值,不翻译。)

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/test/AgentTokenManager.test.tsx src/test/App.test.tsx`
Expected: PASS(App.test 中所有 token 英文文案断言不变;`tokenError` 现有测试均传 `null`,类型兼容)

- [ ] **Step 6: 类型检查与全量回归后提交**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/App.tsx web/src/components/AgentTokenManager.tsx web/src/test/AgentTokenManager.test.tsx
git commit -m "feat(web): localize agent token manager"
```

---

### Task 7: 其余组件国际化(DeviceList / TerminalTabs / SnippetsBar / TerminalSearchBar / TerminalPane / FileManagerPanel)

**Files:**
- Modify: `web/src/components/DeviceList.tsx`
- Modify: `web/src/components/TerminalTabs.tsx`
- Modify: `web/src/components/SnippetsBar.tsx`
- Modify: `web/src/components/TerminalSearchBar.tsx`
- Modify: `web/src/components/TerminalPane.tsx`
- Modify: `web/src/components/FileManagerPanel.tsx`
- Test: `web/src/test/i18n-smoke.test.tsx`(新建,中文冒烟)

**Interfaces:**
- Consumes: Task 1 `useT` / `TFunction` / `TranslationKey`
- Produces: 无新接口(纯文本替换)

- [ ] **Step 1: 新建失败测试 `web/src/test/i18n-smoke.test.tsx`**

```tsx
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DeviceList } from '../components/DeviceList';
import { FileManagerPanel } from '../components/FileManagerPanel';
import { SnippetsBar } from '../components/SnippetsBar';
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
  };
});

const mockedApi = vi.mocked(api);

// 各组件的中文冒烟断言:每个组件验证一个代表性文本
describe('组件中文冒烟', () => {
  beforeEach(() => {
    window.localStorage.setItem('vibe.lang', 'zh');
  });
  afterEach(() => {
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
```

(注意:`Device`/`FsEntry` 字段以 `web/src/api.ts` 实际类型为准,若有必填字段缺失按类型报错补齐。)

- [ ] **Step 2: 运行测试确认失败**

Run: `npx vitest run src/test/i18n-smoke.test.tsx`
Expected: FAIL(全部组件仍是英文)

- [ ] **Step 3: 逐组件替换文本(每个组件:import `useT`,函数体加 `const { t } = useT();`)**

**DeviceList.tsx:**

| 原文 | 替换为 |
|------|--------|
| `<h2>Devices</h2>` | `<h2>{t('devices.title')}</h2>` |
| `<span>Device name</span>` | `<span>{t('devices.name')}</span>` |
| `aria-label="Save device name"` | `aria-label={t('devices.saveName')}` |
| `aria-label={\`Cancel rename ${device.name}\`}` | `aria-label={t('devices.cancelRename', { name: device.name })}` |
| `{device.online ? 'online' : 'offline'}` | `{device.online ? t('devices.online') : t('devices.offline')}`(className 保留原值) |
| `aria-label={\`Rename ${device.name}\`}` | `aria-label={t('devices.rename', { name: device.name })}` |
| `aria-label={\`Browse files on ${device.name}\`}` | `aria-label={t('devices.browseFiles', { name: device.name })}` |
| `New terminal` | `{t('devices.newTerminal')}` |

**TerminalTabs.tsx:**

组件外的纯函数 `sessionDeviceName` 依赖翻译,改为接收翻译值:

```tsx
function sessionDeviceName(session: Session, unknownDevice: string) {
  return session.device_name || session.device_id || unknownDevice;
}
```

组件内调用处(2 处)改为 `sessionDeviceName(session, t('sessions.unknownDevice'))` / `sessionDeviceName(activeSession, t('sessions.unknownDevice'))`。

新增状态映射(文件顶部,组件外):

```tsx
import type { TFunction, TranslationKey } from '../i18n';

const sessionStatusKeys: Partial<Record<string, TranslationKey>> = {
  running: 'sessions.statusRunning',
  starting: 'sessions.statusStarting',
  lost: 'sessions.statusLost',
  exited: 'sessions.statusExited',
  closed: 'sessions.statusClosed',
};

// 已知状态翻译,未知状态原样显示
function statusLabel(t: TFunction, status: string) {
  const key = sessionStatusKeys[status];
  return key ? t(key) : status;
}
```

| 原文 | 替换为 |
|------|--------|
| `<main className="empty">No terminal session open</main>` | `<main className="empty">{t('sessions.empty')}</main>` |
| `<span>Session title</span>` | `<span>{t('sessions.title')}</span>` |
| `aria-label="Save"` | `aria-label={t('common.save')}` |
| `aria-label={\`Rename ${label}\`}` | `aria-label={t('sessions.rename', { label })}` |
| `aria-label={\`Confirm delete ${label}\`}` | `aria-label={t('sessions.confirmDelete', { label })}` |
| `aria-label={\`Cancel delete ${label}\`}` | `aria-label={t('sessions.cancelDelete', { label })}` |
| `aria-label={\`Delete ${label}\`}` | `aria-label={t('sessions.delete', { label })}` |
| `{session.status}`(statusBadge 内) | `{statusLabel(t, session.status)}` |
| `` `session ${activeSession.id}` `` | `t('sessions.meta', { id: activeSession.id })` |

**SnippetsBar.tsx:**

| 原文 | 替换为 |
|------|--------|
| `setError('Failed to load snippets.')` | `setError('snippets.errLoad')` |
| `setError('Failed to save snippet.')` | `setError('snippets.errSave')` |
| `setError('Failed to delete snippet.')` | `setError('snippets.errDelete')` |
| state 类型 `useState<string \| null>` | `useState<TranslationKey \| null>`(import type `TranslationKey`) |
| `{error}`(snippetsError 内) | `{t(error)}` |
| `<span>Quick commands</span>` | `<span>{t('snippets.toggle')}</span>` |
| `No snippets yet` | `{t('snippets.empty')}` |
| `aria-label={\`Insert ${snippet.name}\`}` | `aria-label={t('snippets.insert', { name: snippet.name })}` |
| `aria-label={\`Edit ${snippet.name}\`}` | `aria-label={t('snippets.edit', { name: snippet.name })}` |
| `aria-label={\`Delete ${snippet.name}\`}` | `aria-label={t('snippets.delete', { name: snippet.name })}` |
| `placeholder="Name"` | `placeholder={t('snippets.namePlaceholder')}` |
| `placeholder="Command"` | `placeholder={t('snippets.commandPlaceholder')}` |
| `aria-label="Snippet name"` | `aria-label={t('snippets.nameLabel')}` |
| `aria-label="Snippet command"` | `aria-label={t('snippets.commandLabel')}` |
| `{editing ? 'Save' : 'Add'}` | `{editing ? t('common.save') : t('common.add')}` |
| `Cancel` | `{t('common.cancel')}` |
| snippetsHint 段落文本 | `{t('snippets.hint')}` |

(注意:SnippetsBar 现有测试 `SnippetsBar.test.tsx` 若断言英文错误文本,`t()` 渲染值与原文一致,不受影响。)

**TerminalSearchBar.tsx:**

| 原文 | 替换为 |
|------|--------|
| `placeholder="Search"` | `placeholder={t('search.placeholder')}` |
| `aria-label="Search terminal"` | `aria-label={t('search.terminal')}` |
| `aria-label="Match case"` | `aria-label={t('search.matchCase')}` |
| `aria-label="Previous match"` | `aria-label={t('search.previous')}` |
| `aria-label="Next match"` | `aria-label={t('search.next')}` |
| `aria-label="Close search"` | `aria-label={t('search.close')}` |

**TerminalPane.tsx:**

| 原文 | 替换为 |
|------|--------|
| `aria-label="Search terminal output"` | `aria-label={t('search.output')}` |

**FileManagerPanel.tsx:**

错误 state 处理:`setError` 的两个 fallback 改为翻译值(组件内已有 `t`,直接存翻译后的字符串——服务端 `err.message` 保持原样):

| 原文 | 替换为 |
|------|--------|
| `: 'Failed to list directory'` | `: t('files.errList')` |
| `: 'Upload failed'` | `: t('files.errUpload')` |
| `` window.confirm(\`${file.name} already exists. Overwrite?\`) `` | `window.confirm(t('files.overwrite', { name: file.name }))` |
| `aria-label={\`Files on ${device.name}\`}` | `aria-label={t('files.dialog', { name: device.name })}` |
| `<span>Files</span>` | `<span>{t('files.title')}</span>` |
| `aria-label="Close file manager"` | `aria-label={t('files.close')}` |
| `aria-label="Parent directory"` | `aria-label={t('files.parent')}` |
| `aria-label="Current path"` | `aria-label={t('files.path')}` |
| `aria-label="Refresh"` | `aria-label={t('common.refresh')}` |
| `aria-label="Upload"` | `aria-label={t('files.upload')}` |
| `aria-label="Upload file"` | `aria-label={t('files.uploadFile')}` |
| `aria-label={\`Open ${entry.name}\`}` | `aria-label={t('files.open', { name: entry.name })}` |
| `aria-label={\`Download ${entry.name}\`}` | `aria-label={t('files.download', { name: entry.name })}` |
| `Empty directory` | `{t('files.empty')}` |

注意:`load` 是 `useCallback`,依赖数组**保持 `[device.id]` 不变**——若把 `t` 加入依赖,语言切换会使 `load` 变化并触发 `useEffect(() => void load('~'))`,把文件管理器重置回主目录。`t` 以闭包捕获即可;代价仅是语言切换前发生的错误消息保持旧语言,可接受。`uploadFile` 非 memoized,直接用 `t`。

- [ ] **Step 4: 运行冒烟与相关组件测试确认通过**

Run: `npx vitest run src/test/i18n-smoke.test.tsx src/test/SnippetsBar.test.tsx src/test/TerminalSearchBar.test.tsx src/test/FileManagerPanel.test.tsx src/test/TerminalPane.test.tsx`
Expected: PASS

- [ ] **Step 5: 类型检查与全量回归后提交**

Run: `npx tsc --noEmit && npx vitest run`
Expected: 全部通过(App.test.tsx 断言英文文本,值未变)

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/components web/src/test/i18n-smoke.test.tsx
git commit -m "feat(web): localize remaining components"
```

---

### Task 8: 移动端布局(底部标签栏)

**Files:**
- Modify: `web/src/test/setup.ts`(默认 matchMedia mock)
- Modify: `web/src/App.tsx`(useIsMobile、MobileTabBar、devices 视图)
- Modify: `web/src/styles.css`(移动端样式)
- Test: `web/src/test/App.test.tsx`(追加移动端 describe)

**Interfaces:**
- Consumes: Task 3 的 `ViewMode`(含 `'devices'`)、`SettingsView`
- Produces:
  - 移动端(`matchMedia('(max-width: 760px)')` 命中)渲染底部 `nav.mobileTabBar`(设备/终端/令牌/设置),桌面侧栏不渲染
  - `'devices'` 视图 = brand + DeviceList;新建会话成功后自动切到 `'terminals'`
  - 视口变宽时 `'devices'` 自动回退 `'terminals'`

- [ ] **Step 1: 在 `web/src/test/setup.ts` 末尾追加默认 matchMedia mock**

```ts
// jsdom 不实现 matchMedia;默认按桌面(不匹配移动断点)
if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }),
  });
}
```

- [ ] **Step 2: 在 `web/src/test/App.test.tsx` 追加失败的移动端测试**

文件末尾追加:

```tsx
describe('移动端布局', () => {
  const realMatchMedia = window.matchMedia;
  afterEach(() => {
    window.matchMedia = realMatchMedia;
  });

  type MediaListener = (event: MediaQueryListEvent) => void;

  // 可控的 matchMedia 替身:setMatches 可模拟视口宽窄切换
  function installMatchMedia(initialMatches: boolean) {
    const listeners = new Set<MediaListener>();
    let matches = initialMatches;
    const mediaQueryList = {
      get matches() {
        return matches;
      },
      media: '(max-width: 760px)',
      onchange: null,
      addEventListener: (_type: string, listener: MediaListener) => {
        listeners.add(listener);
      },
      removeEventListener: (_type: string, listener: MediaListener) => {
        listeners.delete(listener);
      },
      addListener: (listener: MediaListener) => {
        listeners.add(listener);
      },
      removeListener: (listener: MediaListener) => {
        listeners.delete(listener);
      },
      dispatchEvent: () => false,
    };
    window.matchMedia = (() => mediaQueryList) as unknown as typeof window.matchMedia;
    return {
      setMatches(next: boolean) {
        matches = next;
        listeners.forEach((listener) => listener({ matches: next } as MediaQueryListEvent));
      },
    };
  }

  const device = { id: 'dev-1', name: 'MacBook', platform: 'darwin', online: true };

  it('移动端渲染底部标签栏,桌面侧栏不渲染', () => {
    installMatchMedia(true);
    render(<AppView {...loggedInAppViewProps({ devices: [device] })} />);
    const tabBar = screen.getByRole('navigation', { name: 'Primary' });
    expect(tabBar).toHaveClass('mobileTabBar');
    expect(within(tabBar).getByRole('button', { name: 'Devices' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Agent Tokens' })).toBeInTheDocument();
    expect(within(tabBar).getByRole('button', { name: 'Settings' })).toBeInTheDocument();
    // 桌面侧栏的品牌区不渲染(移动端品牌在设备视图内,初始隐藏)
    expect(screen.queryByRole('heading', { name: 'Devices' })).not.toBeInTheDocument();
  });

  it('设备 tab 显示设备列表,新建会话后自动切回终端视图', async () => {
    installMatchMedia(true);
    const session = {
      id: 'sess-1',
      title: 'shell',
      status: 'running',
      device_id: 'dev-1',
    };
    const onCreateSession = vi.fn().mockResolvedValue(session);
    render(<AppView {...loggedInAppViewProps({ devices: [device], onCreateSession })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Devices' }));
    expect(screen.getByRole('heading', { name: 'Devices' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'New terminal' }));
    expect(onCreateSession).toHaveBeenCalledWith('dev-1');
    // 自动切回终端视图:激活的 tab 是 Terminals,且会话标签出现
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');
    expect(await screen.findByRole('tab', { selected: true })).toBeInTheDocument();
  });

  it('视口变宽时设备视图回退为终端视图并恢复侧栏', async () => {
    const media = installMatchMedia(true);
    render(<AppView {...loggedInAppViewProps({ devices: [device] })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Devices' }));
    act(() => media.setMatches(false));
    // 桌面侧栏出现(设备标题可见),Terminals 导航激活,tab bar 消失
    expect(screen.getByRole('heading', { name: 'Devices' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Terminals' })).toHaveAttribute('aria-current', 'page');
    expect(screen.queryByRole('button', { name: 'Devices' })).not.toBeInTheDocument();
  });

  it('恢复码交付锁定期间移动端 tab 禁用(设置除外)', async () => {
    installMatchMedia(true);
    const recoveryRequest = deferred<string[]>();
    mockedApi.startTwoFactorSetup.mockResolvedValue({
      manualKey: 'MOBILE-SECRET',
      otpauthURI: 'otpauth://totp/example?secret=MOBILE-SECRET',
      expiresAt: '',
    });
    mockedApi.enableTwoFactor.mockReturnValue(recoveryRequest.promise);
    render(<AppView {...loggedInAppViewProps()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Settings' }));
    await userEvent.click(await screen.findByRole('tab', { name: 'Security' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Enable two-factor authentication' }));
    await userEvent.type(screen.getByLabelText('Current password'), 'secret');
    await userEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await userEvent.type(await screen.findByLabelText('Authenticator code'), '123456');
    await userEvent.click(screen.getByRole('button', { name: 'Enable two-factor authentication' }));
    const tabBar = screen.getByRole('navigation', { name: 'Primary' });
    expect(within(tabBar).getByRole('button', { name: 'Devices' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Agent Tokens' })).toBeDisabled();
    expect(within(tabBar).getByRole('button', { name: 'Settings' })).toBeEnabled();
    await act(async () => recoveryRequest.resolve(['MOBILE-CODE']));
    await userEvent.click(await screen.findByRole('button', { name: 'Done' }));
    expect(within(tabBar).getByRole('button', { name: 'Terminals' })).toBeEnabled();
  });
});
```

(`session` 对象字段若与 `Session` 类型不符,按 `web/src/api.ts` 的类型补齐必填字段。)

- [ ] **Step 3: 运行测试确认失败**

Run: `npx vitest run src/test/App.test.tsx`
Expected: FAIL(无 mobileTabBar)

- [ ] **Step 4: 修改 `web/src/App.tsx`**

(a) imports 补充 `Server` 图标与 `ReactNode` 类型:

```tsx
import { KeyRound, Monitor, Server, Settings, Terminal } from 'lucide-react';
import type { ReactNode } from 'react';
```

(b) 文件内(App 组件之前)加 hook 与常量:

```tsx
const MOBILE_QUERY = '(max-width: 760px)';

// 监听移动断点;与 CSS 的 760px 保持一致
function useIsMobile() {
  const [isMobile, setIsMobile] = useState(() => window.matchMedia(MOBILE_QUERY).matches);
  useEffect(() => {
    const query = window.matchMedia(MOBILE_QUERY);
    const onChange = (event: MediaQueryListEvent) => setIsMobile(event.matches);
    query.addEventListener('change', onChange);
    setIsMobile(query.matches);
    return () => query.removeEventListener('change', onChange);
  }, []);
  return isMobile;
}
```

(c) 新增 MobileTabBar 组件(AuthenticatedAppView 之前):

```tsx
// 移动端底部导航:设备 / 终端 / 令牌 / 设置
function MobileTabBar({
  viewMode,
  locked,
  onSelect,
}: {
  viewMode: ViewMode;
  locked: boolean;
  onSelect: (mode: ViewMode) => void;
}) {
  const { t } = useT();
  const items: Array<{ mode: ViewMode; label: string; icon: ReactNode }> = [
    { mode: 'devices', label: t('nav.devices'), icon: <Server size={18} aria-hidden="true" /> },
    { mode: 'terminals', label: t('nav.terminals'), icon: <Monitor size={18} aria-hidden="true" /> },
    { mode: 'agentTokens', label: t('nav.agentTokens'), icon: <KeyRound size={18} aria-hidden="true" /> },
    { mode: 'settings', label: t('nav.settings'), icon: <Settings size={18} aria-hidden="true" /> },
  ];
  return (
    <nav className="mobileTabBar" aria-label={t('nav.primary')}>
      {items.map((item) => (
        <button
          key={item.mode}
          type="button"
          disabled={locked && item.mode !== 'settings'}
          aria-current={viewMode === item.mode ? 'page' : undefined}
          className={viewMode === item.mode ? 'active' : ''}
          onClick={() => onSelect(item.mode)}
        >
          {item.icon}
          <span>{item.label}</span>
        </button>
      ))}
    </nav>
  );
}
```

(d) `AuthenticatedAppView` 内:

```tsx
const isMobile = useIsMobile();

// 离开移动断点时,设备视图在桌面端不存在,回退到终端
useEffect(() => {
  if (!isMobile && viewMode === 'devices') setViewMode('terminals');
}, [isMobile, viewMode]);
```

`createAndAppend` 成功分支末尾加:

```tsx
if (isMobile) setViewMode('terminals'); // 移动端:新建会话后跳回终端视图
```

(e) return 的 JSX 调整:

```tsx
return (
  <div className={isMobile ? 'shell mobileShell' : 'shell'}>
    {!isMobile && (
      <aside className="devices">
        {/* …现有 brand + sideNav + DeviceList 内容原样保留… */}
      </aside>
    )}
    <div className="viewPane" hidden={viewMode !== 'terminals'} aria-hidden={viewMode !== 'terminals'}>
      {/* …现有 TerminalTabs… */}
    </div>
    <div className="viewPane" hidden={viewMode !== 'agentTokens'} aria-hidden={viewMode !== 'agentTokens'}>
      {/* …现有 AgentTokenManager… */}
    </div>
    {isMobile && (
      <div
        className="viewPane mobileDevicesPane"
        hidden={viewMode !== 'devices'}
        aria-hidden={viewMode !== 'devices'}
      >
        <div className="brand">
          <Terminal size={18} aria-hidden="true" />
          <span>vibe-terminal</span>
        </div>
        <DeviceList
          devices={localDevices}
          onCreateSession={createAndAppend}
          onRenameDevice={renameDeviceAndApply}
          onOpenFiles={setFilesDevice}
        />
      </div>
    )}
    {viewMode === 'settings' && (
      <SettingsView
        securityLoader={securityLoader}
        onRecoveryDeliveryLockChange={setSecurityDeliveryLocked}
        deliveryLocked={securityDeliveryLocked}
      />
    )}
    {isMobile && (
      <MobileTabBar viewMode={viewMode} locked={securityDeliveryLocked} onSelect={setViewMode} />
    )}
    {filesDevice && <FileManagerPanel device={filesDevice} onClose={() => setFilesDevice(null)} />}
  </div>
);
```

(移动端 DeviceList 不传 `compact`,渲染为 `section.devices` 玻璃卡片。)

- [ ] **Step 5: 修改 `web/src/styles.css`**

(a) **删除** 1246 行附近的旧移动规则:

```css
@media (max-width: 760px) {
  .shell {
    grid-template-columns: 1fr;
    grid-template-rows: auto minmax(0, 1fr);
    height: 100dvh;
  }

  .devices {
    overflow-y: visible;
  }
}
```

(b) 文件末尾追加:

```css
/* ===== 移动端布局 ===== */
/* 底部标签栏(仅移动端由 JS 渲染) */
.mobileTabBar {
  display: flex;
  align-items: stretch;
  gap: 4px;
  padding: 6px 8px calc(6px + env(safe-area-inset-bottom));
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg-strong);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}

.mobileTabBar button {
  flex: 1;
  min-height: 48px;
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  font-size: 11px;
  border-radius: 10px;
}

.mobileTabBar button.active {
  background: var(--accent-soft);
  color: #ddd6fe;
}

/* 移动端整体两行布局:内容区 + 底部标签栏 */
.shell.mobileShell {
  grid-template-columns: 1fr;
  grid-template-rows: minmax(0, 1fr) auto;
  height: 100dvh;
  padding: 8px;
  gap: 8px;
}

.shell.mobileShell .settingsPage {
  padding: 16px;
}

/* 移动端设备视图:品牌 + 设备列表,可纵向滚动 */
.mobileDevicesPane {
  min-height: 0;
  overflow-y: auto;
  display: block;
}

.mobileDevicesPane .brand {
  padding: 4px 4px 12px;
}

/* 移动端触控目标:设备操作按钮加高 */
.mobileDevicesPane .deviceActions button {
  min-height: 44px;
}

.mobileDevicesPane .iconButton {
  min-height: 44px;
  min-width: 44px;
}

/* 移动端文件管理器抽屉全宽 */
@media (max-width: 760px) {
  .filePanel {
    width: 100%;
    margin: 0;
    border-radius: 0;
  }
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `npx vitest run src/test/App.test.tsx`
Expected: PASS(新增移动端 4 例 + 原有全部用例)

- [ ] **Step 7: 类型检查、全量回归与构建验证**

Run: `npx tsc --noEmit && npx vitest run && npm run build`
Expected: 全部通过,`dist/` 构建成功

- [ ] **Step 8: 提交**

```bash
cd /home/djy/xiangmu/clouldcode
git add web/src/App.tsx web/src/styles.css web/src/test/setup.ts web/src/test/App.test.tsx
git commit -m "feat(web): add mobile bottom tab bar layout"
```

---

## 任务依赖

- Task 1 → 全部后续任务的基础
- Task 2 依赖 Task 1;Task 3 依赖 Task 2
- Task 4、5、6、7 依赖 Task 1(4-7 相互独立,但 5、6 的测试引用 Task 3 后的 App 结构,建议按序执行)
- Task 8 依赖 Task 3(Settings 导航)与 Task 7(nav 字典 key 已在 Task 1 就绪)

## 验收核对(对照规格)

- [x] 设置容器页:Task 2/3(通用 + 安全子分区,Security 导航项移除)
- [x] 2FA 迁入设置:Task 2/3(懒加载 + 错误边界 + 锁定逻辑保留)
- [x] 中英切换全 UI 覆盖:Task 1(字典)+ 3/4/5/6/7(组件)
- [x] localStorage + 浏览器语言检测:Task 1
- [x] 登录页语言切换:Task 4
- [x] 移动端底部标签栏 + 设备 tab + 自动回退:Task 8
- [x] 触控目标 44px / safe-area / 100dvh / 文件抽屉全宽:Task 8
- [x] 桌面端布局不变:Task 3/8(仅替换导航项文案与图标)
