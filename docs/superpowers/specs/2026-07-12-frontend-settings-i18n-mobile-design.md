# 前端设置页、中英文切换与移动端布局设计

- 日期:2026-07-12
- 状态:已批准
- 范围:仅前端(`web/`),无后端改动

## 背景与目标

当前 `web/` 前端(React 18 + TypeScript + Vite)左侧导航为 Terminals / Agent Tokens / Security 三项并列,所有 UI 文本硬编码英文,响应式仅有零散断点(760/820px),移动端体验差(侧边栏堆叠在顶部,挤压终端区域)。

本设计新增"设置"页,实现四个目标:

1. 新增"设置"导航入口,作为容器页,内含子分区
2. 把安全(2FA)从独立导航项迁入设置页
3. 支持中英文切换,覆盖全部 UI 文本
4. 移动端(≤760px)布局优化:底部标签栏导航

## 需求

- 左侧导航的 Security 项替换为 Settings 项(齿轮图标);Agent Tokens 保持独立导航项
- 设置页内含两个子分区 tab:**通用**(语言切换)和 **安全**(现有 SecurityView 完整迁入)
- 语言切换:中文 / English 两个选项,即时生效,无需刷新
- 语言偏好持久化到 localStorage,首次访问按浏览器语言自动选择
- 翻译覆盖全部 UI 文本:登录页、导航、设置/安全(2FA)、Agent Tokens、设备列表、文件管理器、终端标签栏、搜索栏、代码片段栏;终端会话内容本身除外
- 登录页(未登录状态)右上角提供小型语言切换按钮
- 移动端(≤760px):侧边栏隐藏,底部固定 tab bar:设备 / 终端 / 令牌 / 设置
- 桌面端(>760px)布局完全保持现状

## 非目标(YAGNI)

- 不做语言偏好的服务端存储(不改后端)
- 不支持中英以外的语言(但字典结构天然可扩展)
- 不改动桌面端布局与视觉风格(玻璃拟态风格不变)
- 不引入第三方 i18n 库(react-i18next 等)
- 设置页暂不加入外观/主题等其他偏好(结构上预留扩展空间)

## 总体架构

```
web/src/
├── i18n/
│   ├── index.tsx      # LanguageProvider + useT() + useLang()
│   ├── en.ts          # 英文字典(作为类型基准)
│   └── zh.ts          # 中文字典(类型强制与 en 对齐)
├── components/
│   ├── SettingsView.tsx   # 新增:设置容器页(通用 + 安全 两个子分区 tab)
│   ├── SecurityView.tsx   # 保留,挂到设置页内,继续懒加载
│   └── ...                # 所有组件文本改用 t()
└── App.tsx            # ViewMode 调整;移动端底部 tab bar
```

- `main.tsx` 中用 `LanguageProvider` 包裹 `<App />`
- `App.tsx` 的 `ViewMode` 由 `'terminals' | 'agentTokens' | 'security'` 改为 `'terminals' | 'agentTokens' | 'settings' | 'devices'`,其中 `'devices'` 仅移动端可达

## i18n 模块设计

**Provider 与 hook:**

- `LanguageProvider`:持有 `lang: 'en' | 'zh'` 状态,通过 Context 下发
- `useT()`:返回翻译函数 `t(key, params?)`
- `useLang()`:返回 `{ lang, setLang }`,供语言切换 UI 使用

**初始语言判定(优先级从高到低):**

1. `localStorage['vibe.lang']` 中已保存的有效值(`'en'` 或 `'zh'`)
2. `navigator.language` 以 `zh` 开头 → `'zh'`
3. 否则 `'en'`

**切换行为:**

- `setLang` 即时更新 Context(全 UI 重渲染),写入 `localStorage['vibe.lang']`,并同步更新 `document.documentElement.lang`(`'en'` / `'zh-CN'`)

**字典与类型安全:**

- `en.ts` 导出扁平结构的字典对象,作为类型基准:`export type Translation = typeof en`
- `zh.ts` 声明为 `const zh: Translation`,漏译/多译在编译期报错
- 运行时若 key 缺失(理论上被类型排除),回退英文字典对应值
- 插值:简单 `{name}` 占位符替换,如 `t('device.renamed', { name })`

**容错:**

- localStorage 读写包 try/catch(隐私模式等场景),失败时静默降级为仅内存状态

## 设置页设计(SettingsView)

- 左侧导航:移除 Security 项,新增 Settings 项(lucide `Settings` 齿轮图标)
- 设置页顶部标题 + 两个子分区 tab:
  - **通用**:语言设置(中文 / English 两个选项按钮,当前语言高亮)
  - **安全**:现有 `SecurityPanel`(懒加载 SecurityView + ErrorBoundary + 重试)原样移入
- 默认打开"通用" tab
- `securityDeliveryLocked`(查看恢复码期间锁定导航,防止误离开)逻辑保留,锁定范围为:主导航全部按钮 + 设置页内子 tab 切换
- 登录页 `LoginView` 右上角添加小型语言切换按钮(未登录也能切换)

## 移动端布局设计(≤760px)

- 断点沿用现有 `760px`
- 侧边栏(`aside.devices`)整体隐藏
- 底部固定 tab bar,四项:**设备 / 终端 / 令牌 / 设置**(图标 + 文字标签)
  - `padding-bottom: env(safe-area-inset-bottom)` 适配全面屏
  - 触控目标最小 44px
  - `securityDeliveryLocked` 时同样禁用(与桌面导航一致)
- "设备" tab 内容 = 品牌(brand)+ 现有 DeviceList;点"新建会话"成功后自动切到"终端" tab
- 窗口从窄变宽(离开移动断点)时,若当前 `viewMode === 'devices'`,自动回退为 `'terminals'`(桌面端无设备 tab,设备列表回到侧边栏)
- 视口高度继续用 `100dvh`;主内容区为 `auto` 行 + tab bar 行的 grid
- 文件管理器抽屉在移动端改为全宽(去掉右侧留白)
- 断点检测:CSS media query 负责布局;JS 侧用 `window.matchMedia('(max-width: 760px)')` 监听,驱动 `'devices'` 回退逻辑与 tab bar 渲染

## 错误处理

- 字典 key 缺失:编译期类型检查兜底;运行时回退英文
- localStorage 不可用:try/catch 静默降级,语言切换仅当次会话生效
- SecurityView 懒加载失败:沿用现有 ErrorBoundary + 重试按钮,文案本地化
- 语言切换不影响任何进行中的请求或终端连接(纯展示层变更)

## 测试策略

**新增:**

- i18n 单测:初始语言判定(localStorage 优先、浏览器语言检测、兜底英文)、切换即时生效、持久化写入、`<html lang>` 同步、插值、localStorage 异常降级
- SettingsView 测试:子 tab 切换、语言切换立即改变界面文本、安全 tab 内 SecurityView 懒加载与错误重试正常、`securityDeliveryLocked` 时子 tab 禁用
- 移动端测试:mock `matchMedia`,断言底部 tab bar 渲染、设备 tab 可达、新建会话后自动切换到终端 tab、变宽时 `'devices'` 回退

**更新:**

- `App.test.tsx`:导航断言从 Security 改为 Settings;安全页入口路径变为 设置 → 安全 tab
- `SecurityView.test.tsx`:文本断言保持英文(测试环境 `navigator.language` 默认 en-US,初始语言为英文,大部分断言不变)
- 其余组件测试:涉及文本断言处随组件改造同步微调

## 涉及文件清单

| 文件 | 变更 |
|------|------|
| `web/src/i18n/index.tsx` | 新增:Provider + hooks |
| `web/src/i18n/en.ts` / `zh.ts` | 新增:中英字典 |
| `web/src/components/SettingsView.tsx` | 新增:设置容器页 |
| `web/src/main.tsx` | LanguageProvider 包裹 |
| `web/src/App.tsx` | ViewMode 调整、导航替换、移动端 tab bar、设备 tab |
| `web/src/components/*.tsx`(全部) | 文本改用 t() |
| `web/src/styles.css` | 底部 tab bar 样式、移动端断点整理、设置页子 tab 样式 |
| `web/index.html` | 无需改动(`<html lang>` 由 JS 动态维护) |
| `web/src/test/*` | 新增 i18n / SettingsView / 移动端测试,更新现有断言 |
