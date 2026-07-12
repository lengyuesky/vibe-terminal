# vibe-terminal 前端毛玻璃通透风 UI 美化设计

- 日期：2026-06-29
- 范围：`web/` 前端全部界面的视觉美化（不改业务逻辑与数据流）
- 风格：Glassmorphism（毛玻璃通透风）+ 悬浮卡片布局 + 适度动效

## 1. 目标与约束

**目标**：把当前扁平、朴素的深色开发者风格界面，统一升级为一套精致、通透、有层次和设计感的毛玻璃设计语言，让界面「好看」。

**硬约束**：
- 不改动任何业务逻辑、状态管理、API 调用与 WebSocket 行为。
- 保留所有现有 JSX 类名与 DOM 结构（美化集中在 CSS），组件改动仅限纯外观、且为增量式。
- 终端画面区域保持纯深色，保证终端文字可读性。
- 不破坏 xterm 的 `FitAddon` resize/fit 行为。
- 现有测试（`web/src/test/*`）与 `npm run build` 必须继续通过。
- 可访问性：维持文字对比度、保留 `focus-visible` 焦点环、用 `prefers-reduced-motion` 兜底关闭动效。

## 2. 设计语言（Design Tokens）

统一在 `:root` 定义 CSS 变量，全站复用，便于一致性与后续调整。

| 维度 | 方案 |
|------|------|
| 背景底色 | 深空 `#0a0b12` |
| 背景光斑 | 多层 `radial-gradient` 柔光：紫 `#7c3aed` / 蓝 `#2563eb` / 青 `#06b6d4`，低透明度、大尺度，经 `body::before` 固定铺满视口 |
| 玻璃面板 | `background: rgba(255,255,255,0.05)`；`backdrop-filter: blur(20px) saturate(140%)`（含 `-webkit-` 前缀）；`border: 1px solid rgba(255,255,255,0.10)`；内高光 `inset 0 1px 0 rgba(255,255,255,.08)`；外投影 `0 8px 32px rgba(0,0,0,.35)` |
| 主强调 | 紫蓝渐变 `linear-gradient(135deg,#818cf8,#a78bfa)` |
| 辅强调 | 蓝 `#60a5fa` |
| 文字 | 主 `#e8eaf2` / 次 `#a5acbd` / 弱 `#8b93a7` |
| 圆角 | 卡片 16px / 控件 10px / 胶囊 999px |
| 状态色 | 绿(在线/运行) `#34d399` · 蓝(启动) `#60a5fa` · 琥珀(警告) · 珊瑚红(危险/离线) `#fb7185`；统一为「半透明填充 + 微发光描边」 |
| 字体 | 经 Google Fonts 引入 Inter（正文）+ JetBrains Mono（终端/代码/命令）；离线环境回退到系统字体栈 |

## 3. 布局策略

采用**悬浮卡片**布局：在 `.shell` 外层加内边距与间距，侧边栏（`.devices`）与主区域（`.terminalArea` / `.tokenPage`）成为四角圆润、浮起的玻璃卡片。保留 `280px minmax(0,1fr)` 网格结构与现有响应式断点（820px / 760px），并为新的留白做相应调整。

## 4. 各界面改造清单

### 4.1 登录页（`.login` / `.loginForm`）
- 居中浮起玻璃卡片；标题 `vibe-terminal` 用渐变文字 + 终端图标（侧边栏共用同一 `.brand` 元素）。
- 输入框玻璃内凹质感 + 聚焦发光环。
- 登录按钮渐变主色 + hover 微抬升。
- 卡片入场淡入上浮动效。

### 4.2 侧边栏（`.devices` / `.sideNav` / `.deviceRow`）
- 整体玻璃面板。
- `.sideNav` 按钮为玻璃胶囊；`.active` = 渐变填充 + 左侧高亮指示条 + 发光。
- 设备行用半透明分隔线；hover 高亮。
- 在线/离线状态改为**发光状态圆点**（`.online` 在线缓慢脉冲）。
- "New terminal" 按钮用主色。

### 4.3 终端标签页（`.tabs` / `.tabItem` / `.tabButton`）
- 每个标签为玻璃 chip。
- 选中标签（`[aria-selected="true"]`）= 强调色玻璃 + 顶部高亮条 + 发光。
- 状态徽章（`.statusRunning` 等）改为半透发光胶囊；`running` / `starting` 带轻脉冲。
- `.iconButton` 玻璃 hover；danger 态珊瑚红。
- 设备徽章 `.tabDeviceBadge` 渐变质感。

### 4.4 终端头部（`.terminalHeader`）
- 玻璃条 + 渐变设备徽章（`.terminalDeviceBadge`）+ 精修字号层级。

### 4.5 终端画面（`.terminalPaneShell` / `.terminalPane` / xterm）
- 保持纯深色「屏幕」质感：深底 `rgba(8,9,16,.6)` + 内描边。
- 给 `TerminalPane.tsx` 的 `new Terminal({...})` 增加 `theme`（柔白前景、强调色光标/选区、协调 ANSI 调色板）、`fontFamily`（JetBrains Mono 栈）、`fontSize`。
- 连接状态提示（`.terminalStatus`）改为玻璃危险浮层。

### 4.6 Agent Token 页（`.tokenPage` / `.tokenPanel` / `.tokenTable`）
- 面板全部玻璃化。
- 表单输入玻璃内凹。
- 新建成功卡片（`.newToken`）保留绿色强调但玻璃质感；`.tokenValue` / `.agentCommand` 用等宽字体代码块。
- Token 表格半透行 + hover 高亮 + 玻璃表头 + 精修状态胶囊（`.tokenStatus-*`）。

### 4.7 全局控件
- 统一按钮系统：默认（玻璃）/ 主色（渐变）/ 次级（玻璃）/ 危险（珊瑚半透），含 hover 抬升发光、按下回弹、`focus-visible` 焦点环、disabled 降透明。
- 自定义细半透明滚动条（WebKit + Firefox）。

## 5. 动效（适度点缀）

- 背景光斑缓慢漂移（20–30s）。
- 卡片入场淡入上浮（登录卡片、token 卡片）。
- 按钮 hover 过渡（transform + shadow）。
- 状态圆点/徽章脉冲（在线、running、starting）。
- 焦点环过渡。
- 全部置于 `@media (prefers-reduced-motion: reduce)` 兜底关闭。

## 6. 实现方式与改动面

- **~90% 改动集中在 `web/src/styles.css`**：重写为 token 驱动的玻璃设计系统，保留全部现有类名。
- **`web/index.html`**：`<head>` 加 Google Fonts（Inter + JetBrains Mono）`<link>`，含 `preconnect`。
- **`web/src/components/TerminalPane.tsx`**：为 `new Terminal({...})` 增加 `theme` / `fontFamily` / `fontSize` 选项（纯外观）。
- **可选增量 JSX**：登录页（`LoginView.tsx`）与侧边栏（`App.tsx`）新增一个 `.brand` logo 块（纯展示，不改逻辑）。
- **背景**：`body::before` 固定光斑层，零 JSX 改动。
- **兼容兜底**：`@supports not (backdrop-filter: blur(1px))` 提供实色背景回退。

## 7. 测试与验收

- `cd web && npm test` 现有测试全部通过。
- `cd web && npm run build` 构建成功。
- 手动验收：登录页、侧边栏、终端标签与画面、Token 页在毛玻璃风下视觉统一；终端 resize 正常；`prefers-reduced-motion` 下动效关闭；键盘聚焦有清晰焦点环。

## 8. 风险与对策

| 风险 | 对策 |
|------|------|
| `backdrop-filter` 兼容性 | 加 `-webkit-` 前缀 + `@supports not` 实色兜底 |
| 玻璃上文字对比度下降 | 文字用实色且足够明亮，关键文字不放在低透明区 |
| Google Fonts 离线不可用 | 字体栈回退到系统字体，不影响可用性 |
| 终端 resize/fit 受影响 | 仅改外观，不动 DOM 尺寸逻辑；构建后手动验证 |
| docs 目录被仓库忽略 | 本 spec 写入既定位置 `docs/superpowers/specs/`，遵循仓库 `.gitignore` 策略不强行提交 |
