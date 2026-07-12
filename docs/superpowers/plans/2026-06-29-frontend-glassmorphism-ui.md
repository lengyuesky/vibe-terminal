# vibe-terminal 毛玻璃 UI 美化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `web/` 前端全部界面统一升级为毛玻璃通透风（悬浮卡片布局 + 适度动效），不改任何业务逻辑。

**Architecture:** ~90% 改动集中在 `web/src/styles.css`，重写为 token 驱动的玻璃设计系统并保留全部现有类名；`web/index.html` 引入 Google Fonts；`web/src/components/TerminalPane.tsx` 给 xterm 加主题/字体；登录页与侧边栏增量加 `.brand` 装饰元素。背景用 `body::before` 实现。

**Tech Stack:** React 18 + TypeScript + Vite + xterm.js，纯 CSS（无新增依赖）。

## Global Constraints

- 不改动任何业务逻辑、状态管理、API 调用、WebSocket 行为。
- 保留全部现有 JSX 类名与 DOM 结构；组件改动仅限纯外观且为增量式。
- 必须保留测试依赖的类名：`statusRunning` / `statusStarting` / `statusLost` / `statusExited` / `statusClosed` / `statusUnknown` / `tabDeviceBadge`，以及 `role`、`aria-label`、标题文案。
- 登录按钮文案保持 `Login`；标题文案保持不变。
- 终端画面区域保持深色，保证可读性，不破坏 xterm `FitAddon` resize/fit。
- 新增的品牌装饰元素**不得使用 heading 角色**（避免污染 `getByRole('heading')` 查询），用 `div`/`span`。
- 可访问性：保留 `focus-visible` 焦点环；动效统一用 `@media (prefers-reduced-motion: reduce)` 兜底关闭。
- 验收门槛（每个任务结束都要过）：`cd web && npm run build` 成功 + `cd web && npm test` 全绿。
- 颜色/尺寸 token 精确值见各任务，统一来自 `:root` 变量。

> **关于测试策略**：纯展示性 CSS 不适合写单元测试，强行编造断言属于劣质测试设计。本计划以「现有测试套件 + 构建」作为回归护栏，每个任务通过 `npm run build` + `npm test` 验证未破坏行为，视觉效果由人工确认。

---

### Task 1: 设计基座（tokens / 字体 / 背景 / 全局基础）

**Files:**
- Modify: `web/index.html`（`<head>` 加 Google Fonts）
- Modify: `web/src/styles.css:1-15`（替换开头的 `*` / `body` / `button,input` 基础块）
- Test: 复用 `web/src/test/*`（回归护栏）

**Interfaces:**
- Produces: `:root` 下全套 CSS 变量（`--bg` `--glass-bg` `--glass-border` `--glass-blur` `--glass-shadow` `--glass-highlight` `--accent` `--accent-grad` `--accent-glow` `--text` `--text-dim` `--text-faint` `--ok` `--info` `--warn` `--danger` `--r-card` `--r-ctrl` `--r-pill` `--font-sans` `--font-mono` `--gap` `--t-fast` `--t`），后续所有任务复用。

- [ ] **Step 1: 在 `web/index.html` 的 `<head>` 内、`<title>` 之前加入字体引用**

```html
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link
      href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600&display=swap"
      rel="stylesheet"
    />
```

- [ ] **Step 2: 替换 `web/src/styles.css` 开头的基础块（原第 1–15 行）为 tokens + 基础 + 背景 + 滚动条 + reduced-motion**

```css
:root {
  --bg: #0a0b12;

  --glass-bg: rgba(255, 255, 255, 0.05);
  --glass-bg-strong: rgba(255, 255, 255, 0.08);
  --glass-border: rgba(255, 255, 255, 0.10);
  --glass-border-strong: rgba(255, 255, 255, 0.16);
  --glass-blur: blur(20px) saturate(140%);
  --glass-shadow: 0 8px 32px rgba(0, 0, 0, 0.35);
  --glass-highlight: inset 0 1px 0 rgba(255, 255, 255, 0.08);

  --accent: #a78bfa;
  --accent-2: #818cf8;
  --accent-blue: #60a5fa;
  --accent-grad: linear-gradient(135deg, #818cf8 0%, #a78bfa 100%);
  --accent-soft: rgba(167, 139, 250, 0.16);
  --accent-glow: 0 0 0 1px rgba(167, 139, 250, 0.45), 0 8px 24px rgba(129, 140, 248, 0.35);

  --text: #e8eaf2;
  --text-dim: #a5acbd;
  --text-faint: #8b93a7;

  --ok: #34d399;
  --info: #60a5fa;
  --warn: #fbbf24;
  --danger: #fb7185;

  --r-card: 16px;
  --r-ctrl: 10px;
  --r-pill: 999px;

  --font-sans: 'Inter', ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'JetBrains Mono', ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace;

  --gap: 14px;
  --t-fast: 120ms ease;
  --t: 200ms cubic-bezier(0.4, 0, 0.2, 1);
}

* {
  box-sizing: border-box;
}

html,
body {
  height: 100%;
}

body {
  margin: 0;
  min-height: 100vh;
  font-family: var(--font-sans);
  color: var(--text);
  background: var(--bg);
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

body::before {
  content: '';
  position: fixed;
  inset: -12%;
  z-index: -1;
  background:
    radial-gradient(58% 48% at 18% 12%, rgba(124, 58, 237, 0.30), transparent 70%),
    radial-gradient(54% 44% at 85% 16%, rgba(37, 99, 235, 0.28), transparent 70%),
    radial-gradient(50% 50% at 78% 88%, rgba(6, 182, 212, 0.18), transparent 70%),
    radial-gradient(46% 40% at 10% 86%, rgba(167, 139, 250, 0.20), transparent 70%),
    var(--bg);
  animation: bgDrift 28s ease-in-out infinite alternate;
}

@keyframes bgDrift {
  0% {
    transform: translate3d(0, 0, 0) scale(1);
  }
  100% {
    transform: translate3d(0, -1.5%, 0) scale(1.05);
  }
}

button,
input {
  font: inherit;
}

* {
  scrollbar-width: thin;
  scrollbar-color: rgba(255, 255, 255, 0.18) transparent;
}

*::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

*::-webkit-scrollbar-track {
  background: transparent;
}

*::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.14);
  border-radius: var(--r-pill);
  border: 2px solid transparent;
  background-clip: padding-box;
}

*::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.26);
  background-clip: padding-box;
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.001ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.001ms !important;
  }
}
```

- [ ] **Step 3: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功，无 TS/vite 报错。

- [ ] **Step 4: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add web/index.html web/src/styles.css
git commit -m "feat(web): add glassmorphism design tokens, fonts and background"
```

---

### Task 2: 全局控件系统（按钮 / 输入框 / 焦点环）

**Files:**
- Modify: `web/src/styles.css`（替换原第 53–88 行的按钮组规则 + 原 `.primaryButton`）

**Interfaces:**
- Consumes: Task 1 的 tokens。
- Produces: 统一控件外观类 `.primaryButton` / `.secondaryButton` / `.dangerButton`，以及基础 `button` / `input` 玻璃样式与 `:focus-visible` 焦点环，后续任务直接复用。

- [ ] **Step 1: 替换原按钮组规则（原第 53–88 行整段，含 `.primaryButton` 与 `.deviceRow button:disabled`）为统一控件系统**

```css
.loginForm button,
.deviceRow button,
.tabs button,
.sideNav button,
.tokenForm button,
.secondaryButton,
.iconTextButton,
.dangerButton {
  min-height: 36px;
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border-strong);
  background: var(--glass-bg-strong);
  color: var(--text);
  padding: 0 14px;
  cursor: pointer;
  transition: transform var(--t-fast), background var(--t), border-color var(--t), box-shadow var(--t);
}

.deviceRow button,
.sideNav button,
.tokenForm button,
.secondaryButton,
.iconTextButton,
.dangerButton {
  display: inline-flex;
  gap: 8px;
  align-items: center;
  justify-content: center;
}

.loginForm button:hover:not(:disabled),
.deviceRow button:hover:not(:disabled),
.tabs button:hover:not(:disabled),
.sideNav button:hover:not(:disabled),
.tokenForm button:hover:not(:disabled),
.secondaryButton:hover:not(:disabled),
.iconTextButton:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.12);
  border-color: var(--glass-border-strong);
  transform: translateY(-1px);
}

.loginForm button:active:not(:disabled),
.deviceRow button:active:not(:disabled),
.sideNav button:active:not(:disabled),
.tokenForm button:active:not(:disabled),
.secondaryButton:active:not(:disabled),
.iconTextButton:active:not(:disabled),
.dangerButton:active:not(:disabled) {
  transform: translateY(0);
}

.primaryButton {
  border: 1px solid transparent;
  background: var(--accent-grad);
  color: #0b0d16;
  font-weight: 600;
  box-shadow: 0 6px 18px rgba(129, 140, 248, 0.30);
}

.primaryButton:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: var(--accent-glow);
}

.dangerButton {
  border-color: rgba(251, 113, 133, 0.45);
  background: rgba(251, 113, 133, 0.16);
  color: #ffb3bf;
}

.dangerButton:hover:not(:disabled) {
  background: rgba(251, 113, 133, 0.24);
  transform: translateY(-1px);
}

button:disabled,
.deviceRow button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
  border-radius: var(--r-ctrl);
}

input {
  transition: border-color var(--t), box-shadow var(--t), background var(--t);
}

input:focus-visible {
  outline: none;
  border-color: var(--accent) !important;
  box-shadow: 0 0 0 3px var(--accent-soft);
}
```

- [ ] **Step 2: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 3: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS。

- [ ] **Step 4: Commit**

```bash
git add web/src/styles.css
git commit -m "feat(web): unify glass button, input and focus styles"
```

---

### Task 3: 登录页

**Files:**
- Modify: `web/src/styles.css`（原第 17–51 行的 `.login` / `.loginForm` 等）
- Modify: `web/src/components/LoginView.tsx`（标题加终端图标，增量）

**Interfaces:**
- Consumes: Task 1 tokens、Task 2 控件样式。

- [ ] **Step 1: `LoginView.tsx` 给标题加图标（增量，不改逻辑）**

顶部 import 改为：

```tsx
import { Terminal } from 'lucide-react';
import { FormEvent, useState } from 'react';
```

把 `<h1>vibe-terminal</h1>` 改为：

```tsx
        <h1 className="loginBrand">
          <Terminal size={22} aria-hidden="true" />
          vibe-terminal
        </h1>
```

- [ ] **Step 2: 替换 `web/src/styles.css` 原 `.login` / `.loginForm` 区块（原第 17–51 行）**

```css
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
}

.loginForm {
  width: min(380px, calc(100vw - 32px));
  display: grid;
  gap: 16px;
  padding: 28px;
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
  animation: cardIn 420ms cubic-bezier(0.4, 0, 0.2, 1) both;
}

@keyframes cardIn {
  from {
    opacity: 0;
    transform: translateY(12px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.loginBrand {
  margin: 0 0 8px;
  display: inline-flex;
  gap: 10px;
  align-items: center;
  font-size: 26px;
  font-weight: 700;
  letter-spacing: -0.02em;
  background: linear-gradient(135deg, #c4b5fd 0%, #93c5fd 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.loginBrand svg {
  color: var(--accent);
}

.loginForm label {
  display: grid;
  gap: 6px;
  color: var(--text-dim);
  font-size: 14px;
}

.loginForm input {
  min-height: 40px;
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border-strong);
  background: rgba(8, 9, 16, 0.55);
  color: var(--text);
  padding: 0 12px;
}
```

- [ ] **Step 3: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 4: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS（`getByRole('button', { name: /login/i })` 仍命中）。

- [ ] **Step 5: Commit**

```bash
git add web/src/styles.css web/src/components/LoginView.tsx
git commit -m "feat(web): redesign login page with glass card and gradient brand"
```

---

### Task 4: 外壳布局 + 侧边栏

**Files:**
- Modify: `web/src/styles.css`（`.shell` / `.devices` / `.sideNav` / `.deviceRow` / `.online` / `.offline` 等，原第 90–175 行相关段）
- Modify: `web/src/App.tsx`（侧边栏顶部加 `.brand` 装饰，增量）

**Interfaces:**
- Consumes: Task 1 tokens、Task 2 控件样式。
- Produces: 悬浮卡片外壳布局；`.brand` 装饰元素（非 heading）。

- [ ] **Step 1: `App.tsx` 在侧边栏 `<nav className="sideNav">` 之前加品牌块（增量）**

顶部 import 补充 `Terminal`：

```tsx
import { KeyRound, Monitor, Terminal } from 'lucide-react';
```

在 `<aside className="devices">` 内、`<nav ...>` 之前插入：

```tsx
        <div className="brand">
          <Terminal size={18} aria-hidden="true" />
          <span>vibe-terminal</span>
        </div>
```

- [ ] **Step 2: 替换 `.shell` / `.devices` 等布局规则（原第 90–101 行）**

```css
.shell {
  height: 100vh;
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: var(--gap);
  padding: var(--gap);
}

.devices {
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
  padding: 16px;
  overflow-y: auto;
}

.brand {
  display: inline-flex;
  gap: 10px;
  align-items: center;
  padding: 4px 4px 16px;
  font-size: 16px;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--text);
}

.brand svg {
  color: var(--accent);
}
```

- [ ] **Step 3: 替换 `.sideNav` / `.deviceRow` / 状态色相关规则（原第 118–175 行的对应段）**

```css
.devices h2,
.devicesPanel h2 {
  margin: 0 0 12px;
  font-size: 13px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-faint);
}

.sideNav {
  display: grid;
  gap: 8px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--glass-border);
}

.sideNav button {
  justify-content: flex-start;
  position: relative;
}

.sideNav button.active {
  background: var(--accent-soft);
  border-color: rgba(167, 139, 250, 0.45);
  color: #ddd6fe;
  box-shadow: 0 0 18px rgba(167, 139, 250, 0.18);
}

.sideNav button.active::before {
  content: '';
  position: absolute;
  left: -1px;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: var(--r-pill);
  background: var(--accent-grad);
}

.deviceRow {
  display: grid;
  gap: 8px;
  padding: 12px 0;
  border-bottom: 1px solid var(--glass-border);
}

.deviceRow span {
  color: var(--text-dim);
}

.online,
.offline {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
}

.online {
  color: var(--ok);
}

.online::before,
.offline::before {
  content: '';
  width: 8px;
  height: 8px;
  border-radius: var(--r-pill);
  background: currentColor;
}

.online::before {
  box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.20);
  animation: dotPulse 2.4s ease-in-out infinite;
}

.offline,
.error {
  color: var(--danger);
}

@keyframes dotPulse {
  0%, 100% {
    box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.20);
  }
  50% {
    box-shadow: 0 0 0 5px rgba(52, 211, 153, 0.08);
  }
}
```

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 5: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS（rename device、new terminal 等查询仍命中；`.brand` 为 div 不影响 heading 查询）。

- [ ] **Step 6: Commit**

```bash
git add web/src/styles.css web/src/App.tsx
git commit -m "feat(web): floating glass shell layout and sidebar with status dots"
```

---

### Task 5: 终端标签 + 头部

**Files:**
- Modify: `web/src/styles.css`（`.terminalArea` / `.tabs` / `.tabButton` / `.statusBadge` 系列 / `.tabDeviceBadge` / `.terminalHeader`，原第 176–343、633–666 行相关段）

**Interfaces:**
- Consumes: Task 1 tokens、Task 2 控件样式。
- 约束：保留 `.statusRunning/.statusStarting/.statusLost/.statusExited/.statusClosed/.statusUnknown` 与 `.tabDeviceBadge` 类名。

- [ ] **Step 1: 替换 `.terminalArea` 容器为玻璃卡片（原第 176–180 行）**

```css
.terminalArea {
  min-width: 0;
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
  overflow: hidden;
}
```

- [ ] **Step 2: 替换标签栏样式（原第 182–189 行的 `.tabs` + 第 321–323 行选中态 + 第 330–352 行 iconButton/hover）**

```css
.tabs {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 10px;
  border-bottom: 1px solid var(--glass-border);
  overflow-x: auto;
}

.tabItem {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: stretch;
  min-width: 180px;
  max-width: min(360px, calc(100vw - 24px));
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border);
  background: rgba(255, 255, 255, 0.03);
  overflow: hidden;
  transition: border-color var(--t), background var(--t);
}

.tabs .tabButton {
  border: 0;
  background: transparent;
  border-radius: 0;
}

.tabs .tabButton[aria-selected="true"] {
  background: var(--accent-soft);
  position: relative;
}

.tabs .tabButton[aria-selected="true"]::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
  height: 2px;
  background: var(--accent-grad);
}

.tabItem:has(.tabButton[aria-selected="true"]) {
  border-color: rgba(167, 139, 250, 0.45);
  box-shadow: 0 0 16px rgba(167, 139, 250, 0.16);
}

.tabs .iconButton {
  display: inline-grid;
  place-items: center;
  min-width: 36px;
  width: 36px;
  padding: 0;
  border: 0;
  border-left: 1px solid var(--glass-border);
  border-radius: 0;
  background: transparent;
}

.tabs .iconButton:hover:not(:disabled),
.tabs .tabButton:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.10);
}

.tabs .danger:hover:not(:disabled) {
  background: rgba(251, 113, 133, 0.20);
  color: #ffb3bf;
}

.tabs button:disabled,
.tabs input:disabled {
  opacity: 0.55;
}
```

- [ ] **Step 3: 替换状态徽章配色为半透发光（原第 248–319 行的 badge/status 段；保留全部类名）**

```css
.tabDeviceBadge,
.terminalDeviceBadge {
  flex: 0 0 auto;
  max-width: 150px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  border: 1px solid rgba(129, 140, 248, 0.45);
  border-radius: 6px;
  background: linear-gradient(135deg, rgba(129, 140, 248, 0.22), rgba(167, 139, 250, 0.18));
  color: #e5edf7;
  font-weight: 650;
  line-height: 1.35;
}

.tabDeviceBadge {
  padding: 1px 6px;
  font-size: 12px;
}

.terminalDeviceBadge {
  padding: 2px 8px;
  font-size: 13px;
}

.tabSeparator,
.tabSessionId {
  flex: 0 0 auto;
  color: var(--text-faint);
}

.tabMeta {
  display: inline-flex;
  min-width: 0;
  gap: 5px;
  align-items: center;
  color: var(--text-dim);
  font-size: 11px;
}

.statusBadge {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  min-height: 18px;
  border-radius: var(--r-pill);
  padding: 0 7px;
  border: 1px solid transparent;
  line-height: 1;
}

.statusRunning {
  border-color: rgba(52, 211, 153, 0.45);
  background: rgba(52, 211, 153, 0.14);
  color: var(--ok);
  animation: dotPulse 2.6s ease-in-out infinite;
}

.statusStarting {
  border-color: rgba(96, 165, 250, 0.45);
  background: rgba(96, 165, 250, 0.16);
  color: var(--info);
  animation: dotPulse 2.6s ease-in-out infinite;
}

.statusLost {
  border-color: rgba(251, 146, 108, 0.5);
  background: rgba(251, 146, 108, 0.17);
  color: #ffb089;
}

.statusExited {
  border-color: rgba(148, 163, 184, 0.45);
  background: rgba(148, 163, 184, 0.16);
  color: #cbd5e1;
}

.statusClosed,
.statusUnknown {
  border-color: rgba(148, 163, 184, 0.40);
  background: rgba(148, 163, 184, 0.14);
  color: var(--text-dim);
}
```

> 说明：`.statusRunning/.statusStarting` 的 `animation` 用 `dotPulse`（Task 4 定义的 box-shadow 关键帧）只改阴影，不影响布局；reduced-motion 下自动停。

- [ ] **Step 4: 替换终端头部样式（原第 633–666 行）**

```css
.terminalHeader {
  min-width: 0;
  display: flex;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid var(--glass-border);
  background: rgba(255, 255, 255, 0.03);
}

.terminalHeader div {
  min-width: 0;
}

.terminalHeader h1 {
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 650;
  display: inline-flex;
  gap: 10px;
  align-items: center;
  max-width: 100%;
}

.terminalHeader p {
  margin: 3px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-dim);
  font-size: 12px;
}
```

- [ ] **Step 5: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 6: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS（`toHaveClass('statusRunning')` / `tabDeviceBadge` 等断言仍成立）。

- [ ] **Step 7: Commit**

```bash
git add web/src/styles.css
git commit -m "feat(web): glass terminal tabs, glowing status badges and header"
```

---

### Task 6: 终端画面 + xterm 主题

**Files:**
- Modify: `web/src/styles.css`（`.terminalPaneShell` / `.terminalPane` / `.terminalStatus` / `.empty`，原第 668–697 行）
- Modify: `web/src/components/TerminalPane.tsx:78`（`new Terminal({...})` 加 theme/字体）

**Interfaces:**
- Consumes: Task 1 tokens。
- 约束：保留 `.terminalStatus` 的 `role="status"`；不破坏 `FitAddon`。

- [ ] **Step 1: `TerminalPane.tsx` 给终端实例加主题与字体（原第 78 行）**

把：

```tsx
      terminal = new Terminal({ cursorBlink: !readOnly, disableStdin: readOnly, convertEol: true });
```

替换为：

```tsx
      terminal = new Terminal({
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
```

- [ ] **Step 2: 替换终端画面区样式（原第 668–697 行）**

```css
.terminalPaneShell {
  position: relative;
  min-height: 0;
  margin: 10px;
  border-radius: var(--r-ctrl);
  border: 1px solid rgba(255, 255, 255, 0.06);
  background: #0b0d16;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.04), 0 8px 24px rgba(0, 0, 0, 0.30);
  overflow: hidden;
}

.terminalStatus {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 2;
  max-width: min(420px, calc(100% - 24px));
  padding: 10px 12px;
  border: 1px solid rgba(251, 113, 133, 0.55);
  border-radius: var(--r-ctrl);
  background: rgba(24, 16, 20, 0.85);
  -webkit-backdrop-filter: blur(10px);
  backdrop-filter: blur(10px);
  color: #ffb3bf;
  font-size: 13px;
  overflow-wrap: anywhere;
  box-shadow: var(--glass-shadow);
}

.terminalPane {
  min-height: 0;
  height: 100%;
  padding: 10px;
}

.empty {
  display: grid;
  place-items: center;
  color: var(--text-dim);
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}
```

- [ ] **Step 3: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 4: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS（TerminalPane 测试 mock 了 xterm，构造参数不被断言）。

- [ ] **Step 5: Commit**

```bash
git add web/src/styles.css web/src/components/TerminalPane.tsx
git commit -m "feat(web): themed xterm terminal screen with monospace font"
```

---

### Task 7: Agent Token 页

**Files:**
- Modify: `web/src/styles.css`（`.tokenPage` / `.tokenPanel` / `.tokenForm` / `.newToken` / `.tokenTable` / `.tokenStatus-*`，原第 359–573 行相关段）

**Interfaces:**
- Consumes: Task 1 tokens、Task 2 控件样式。
- 约束：保留 `.tokenStatus-available/used/expired/revoked` 类名与 `role="status"`。

- [ ] **Step 1: 替换面板与表单样式（原第 387–421 行的 `.tokenPanel` / `.tokenForm input`）**

```css
.tokenPanel {
  display: grid;
  gap: 14px;
  padding: 18px;
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  -webkit-backdrop-filter: var(--glass-blur);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}

.tokenHeader p,
.muted,
.emptyState {
  color: var(--text-dim);
}

.tokenForm label {
  display: grid;
  gap: 6px;
  color: var(--text-dim);
}

.tokenForm input {
  width: 100%;
  min-height: 40px;
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border-strong);
  background: rgba(8, 9, 16, 0.55);
  color: var(--text);
  padding: 0 12px;
}
```

- [ ] **Step 2: 替换新建成功卡片 + 代码块（原第 428–505 行的 `.newToken` / `.newTokenBlock` / `.tokenValue` / `.agentCommand`）**

```css
.newToken {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid rgba(52, 211, 153, 0.45);
  border-radius: var(--r-card);
  background: linear-gradient(180deg, rgba(52, 211, 153, 0.12), rgba(52, 211, 153, 0.05));
  box-shadow: 0 0 24px rgba(52, 211, 153, 0.12);
}

.newTokenSummary span,
.newTokenBlockHeader span {
  color: var(--text-dim);
  font-weight: 600;
}

.newTokenSummary strong {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--text);
}

.newTokenBlock {
  min-width: 0;
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--glass-border);
  border-radius: var(--r-ctrl);
  background: rgba(8, 9, 16, 0.55);
}

.tokenValue,
.agentCommand {
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: 13px;
  color: #c7f9dd;
}

.tokenValue {
  padding: 10px;
  border-radius: 8px;
  background: rgba(4, 5, 9, 0.8);
}

.agentCommand {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 10px;
  border-radius: 8px;
  background: rgba(4, 5, 9, 0.8);
  white-space: pre-wrap;
}
```

- [ ] **Step 3: 替换表格样式（原第 511–573 行的 `.tokenTable` / `.tokenStatus*`；保留全部类名）**

```css
.tokenTable {
  width: 100%;
  min-width: 760px;
  border-collapse: collapse;
}

.tokenTable th,
.tokenTable td {
  padding: 12px 10px;
  border-bottom: 1px solid var(--glass-border);
  text-align: left;
  vertical-align: middle;
}

.tokenTable th {
  color: var(--text-faint);
  font-weight: 600;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.tokenTable tbody tr {
  transition: background var(--t);
}

.tokenTable tbody tr:hover {
  background: rgba(255, 255, 255, 0.04);
}

.tokenId {
  color: var(--text-faint);
  font-size: 12px;
}

.tokenStatus {
  display: inline-flex;
  align-items: center;
  min-height: 22px;
  border-radius: var(--r-pill);
  padding: 0 9px;
  border: 1px solid transparent;
  line-height: 1;
}

.tokenStatus-available {
  border-color: rgba(52, 211, 153, 0.45);
  background: rgba(52, 211, 153, 0.14);
  color: var(--ok);
}

.tokenStatus-used {
  border-color: rgba(96, 165, 250, 0.45);
  background: rgba(96, 165, 250, 0.16);
  color: var(--info);
}

.tokenStatus-expired {
  border-color: rgba(148, 163, 184, 0.45);
  background: rgba(148, 163, 184, 0.16);
  color: #cbd5e1;
}

.tokenStatus-revoked {
  border-color: rgba(251, 113, 133, 0.5);
  background: rgba(251, 113, 133, 0.16);
  color: #ffb3bf;
}
```

> 注意：原第 568–573 行 `.tokenStatus-revoked, .dangerButton` 是合并规则；本任务把 `.tokenStatus-revoked` 单列，`.dangerButton` 已在 Task 2 定义，删除原合并规则避免重复。

- [ ] **Step 4: 构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 5: 测试回归**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS（token 创建/吊销/删除、`available` 文案与状态类仍命中）。

- [ ] **Step 6: Commit**

```bash
git add web/src/styles.css
git commit -m "feat(web): glassmorphism agent token panels and table"
```

---

### Task 8: 响应式 + 收尾验收

**Files:**
- Modify: `web/src/styles.css`（响应式 `@media` 段，原第 575–593、699–709 行）

**Interfaces:**
- Consumes: 全部前序任务。

- [ ] **Step 1: 更新响应式断点以适配悬浮卡片布局（替换原第 575–593、699–709 行的两段 `@media`）**

```css
@media (max-width: 820px) {
  .tokenForm,
  .newTokenGrid {
    grid-template-columns: 1fr;
  }

  .tokenSubmitButton {
    width: 100%;
  }
}

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

- [ ] **Step 2: 全量构建验证**

Run: `cd web && npm run build`
Expected: 构建成功。

- [ ] **Step 3: 全量测试**

Run: `cd web && npm test -- --run`
Expected: 全部 PASS。

- [ ] **Step 4: 人工视觉验收（dev 服务器）**

Run: `cd web && npm run dev`
检查：登录页玻璃卡片 + 渐变标题；侧边栏悬浮卡片 + 导航选中态 + 在线脉冲点；终端标签玻璃 + 选中高亮 + 状态徽章；终端画面深色等宽 + resize 正常；Token 页玻璃面板 + 表格 hover；窄屏单列正常；系统开启「减少动态效果」后动画停止；键盘 Tab 有清晰焦点环。

- [ ] **Step 5: Commit**

```bash
git add web/src/styles.css
git commit -m "feat(web): responsive tweaks for floating glass layout"
```

---

## Self-Review

**Spec coverage**：spec 第 4 节各界面 → Task 3（登录）/Task 4（侧边栏+布局）/Task 5（标签+头部）/Task 6（终端画面+xterm）/Task 7（Token 页）；设计语言 §2 → Task 1；按钮系统 §4.7 → Task 2；动效 §5 → Task 1（背景/reduced-motion）+ Task 4（脉冲）+ Task 3/5（入场/徽章）；字体 → Task 1 + Task 6；兼容兜底 → 见下方补充说明。覆盖完整。

**Placeholder scan**：无 TODO/TBD，所有步骤含真实 CSS/TSX 与确切命令值。

**Type/类名一致性**：保留 `statusRunning/statusStarting/statusLost/statusExited/statusClosed/statusUnknown/tabDeviceBadge/terminalDeviceBadge/tokenStatus-*/terminalStatus` 全部类名；`dotPulse`/`cardIn`/`bgDrift` 关键帧名在引用前已定义（Task 1 定义 `bgDrift`，Task 3 定义 `cardIn`，Task 4 定义 `dotPulse` 供 Task 5 引用，顺序正确）。

**补充：`@supports` 兜底**（spec §6 要求）— 在 Task 1 Step 2 的 CSS 末尾追加以下兜底，供不支持 `backdrop-filter` 的浏览器回退实色：

```css
@supports not ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) {
  .loginForm,
  .devices,
  .terminalArea,
  .tokenPanel,
  .empty {
    background: rgba(20, 22, 33, 0.92);
  }
}
```
