# vibe-terminal 设计规格

## 目标

`vibe-terminal` 第一版提供一个公网可用的远程终端控制系统，让用户可以通过网页随时查看并继续其他电脑上的 vibe coding 终端会话。被控端主动连接服务端，不需要开放入站端口。命令始终在被控端真实机器执行，服务端只负责认证、路由、状态持久化、审计和 Docker workspace 管理。

## 第一版范围

第一版包含以下能力：

- 单用户管理员登录。
- 被控端 agent 注册和授权。
- Rust agent 支持 Linux、macOS、WSL。
- Agent 前台 CLI 和常驻服务两种运行方式。
- 服务端部署在公网 VPS。
- Docker Compose 单机部署。
- Go 服务端、Rust agent、React + xterm.js 网页前端。
- WebSocket 双向控制通道。
- 网页设备列表和多终端标签。
- 网页刷新或断线后重新连接已有终端。
- 服务端重启后，只要 agent 进程仍在，终端会话可重新同步。
- 服务端为用户维护一个长期 Docker workspace 容器，用于控制层状态隔离和审计落盘，不执行被控端命令。
- SQLite 保存用户、设备、令牌、会话元数据、审计索引和输出分块索引。
- 保存最近终端输出和完整审计日志。
- 公网可用的基础安全措施。

第一版不包含以下能力：

- 手机 App。
- 多用户权限系统。
- Kubernetes 或多节点部署。
- mTLS、端到端加密、命令级审批、IP 白名单。
- 原生 Windows PowerShell/CMD agent。
- 被控端重启或 agent 崩溃后的真实 PTY 进程恢复。
- 服务端 workspace 内执行被控端命令。
- 文件浏览、远程编辑器或完整 IDE 功能。

## 架构

系统由四个主要运行边界组成：

- `server`：Go 服务端，提供 API 和 WebSocket 服务，负责管理员登录、agent 注册、设备在线状态、终端会话路由、SQLite 持久化、审计日志索引，以及 Docker workspace 生命周期管理。
- `agent`：Rust 被控端程序，主动连接服务端，不开放入站端口；负责本机 PTY 会话创建、输入输出转发、resize、进程生命周期、断线重连、服务端重启后的会话恢复。
- `web`：React + xterm.js 前端，展示设备列表、多终端标签、在线或断线状态、最近输出历史，并通过 WebSocket 控制终端。
- `workspace`：服务端每个用户一个长期 Docker 容器，第一版只作为控制与会话状态隔离层，不执行被控端命令；它保存或承载会话层辅助进程、日志卷、状态文件和后续移动端扩展入口。

核心约束：

- 命令始终在被控端真实机器执行。
- 服务端和 workspace 不直接替代被控端 shell。
- 被控端主动连接服务端，避免在被控端开放公网端口。
- 服务端是设备、用户、会话元数据和审计索引的权威控制面。
- Agent 是 PTY 进程和真实终端状态的权威执行面。

## 组件边界

### Server

`server` 保存并管理：

- 用户账号和密码哈希。
- Agent 注册令牌。
- 已授权设备。
- 设备在线状态。
- 会话元数据。
- WebSocket 连接映射。
- 审计事件。
- 终端输出分块索引。
- 用户级 workspace 容器生命周期。

`server` 不负责：

- 执行用户 shell 命令。
- 保存真实 PTY 进程。
- 直接访问被控端文件系统。

### Agent

`agent` 保存并管理：

- 本机设备凭据。
- 本机设备指纹。
- 服务端 WebSocket 长连接。
- 本机 PTY 进程。
- 本机会话注册表。
- 最近输出环形缓冲。
- 输出序列号。
- 进程退出状态。

`agent` 不负责：

- 管理用户账号。
- 对外提供入站网络服务。
- 替代服务端审计索引。

### Web

`web` 负责：

- 登录页面。
- 设备列表。
- 多终端标签。
- 终端输入输出显示。
- resize 事件上报。
- 离线、只读、退出状态展示。
- 最近输出恢复展示。

`web` 不持久化关键状态。刷新后通过 REST 和 WebSocket 从服务端恢复状态。

### Workspace

`workspace` 负责：

- 用户级控制层状态隔离。
- 审计日志落盘卷。
- 会话辅助状态文件。
- 后续移动端扩展入口预留。

`workspace` 不负责：

- 执行被控端 shell 命令。
- 存放被控端真实项目代码。
- 作为云端 IDE 工作区。

## 数据模型

### `users`

- `id`：用户 ID。
- `username`：管理员用户名。
- `password_hash`：Argon2id 或 bcrypt 密码哈希。
- `created_at`：创建时间。
- `updated_at`：更新时间。

### `agent_tokens`

- `id`：令牌 ID。
- `name`：令牌名称。
- `token_hash`：注册令牌哈希。
- `expires_at`：过期时间。
- `used_at`：使用时间。
- `revoked_at`：吊销时间。
- `created_at`：创建时间。

### `devices`

- `id`：设备 ID。
- `name`：设备名称。
- `platform`：平台，例如 `linux`、`macos`、`wsl`。
- `agent_version`：agent 版本。
- `fingerprint`：设备指纹。
- `credential_hash`：设备凭据哈希。
- `authorized`：是否授权。
- `last_seen_at`：最后在线时间。
- `created_at`：创建时间。
- `updated_at`：更新时间。

### `terminal_sessions`

- `id`：会话 ID。
- `device_id`：设备 ID。
- `title`：终端标题。
- `shell_path`：shell 路径。
- `working_directory`：工作目录。
- `status`：`starting`、`running`、`exited`、`lost`、`closed`。
- `agent_pid`：agent 上报的本地进程 ID。
- `last_output_seq`：最后输出序列号。
- `created_at`：创建时间。
- `updated_at`：更新时间。

### `audit_events`

- `id`：事件 ID。
- `user_id`：用户 ID。
- `device_id`：设备 ID。
- `session_id`：会话 ID。
- `event_type`：事件类型。
- `summary`：事件摘要。
- `metadata_json`：结构化元数据。
- `created_at`：创建时间。

### `terminal_output_chunks`

- `id`：输出块 ID。
- `session_id`：会话 ID。
- `start_seq`：起始输出序列号。
- `end_seq`：结束输出序列号。
- `storage_path`：workspace 日志卷中的路径。
- `byte_size`：字节数。
- `created_at`：创建时间。

## API 和协议边界

### REST API

- `POST /api/login`：管理员登录。
- `POST /api/logout`：退出登录。
- `GET /api/me`：获取当前用户。
- `GET /api/devices`：获取设备列表。
- `POST /api/agent-tokens`：创建 agent 注册令牌。
- `GET /api/agent-tokens`：获取注册令牌列表。
- `POST /api/devices/{device_id}/sessions`：创建终端会话。
- `GET /api/devices/{device_id}/sessions`：获取设备会话列表。
- `POST /api/sessions/{session_id}/close`：关闭终端会话。
- `GET /api/sessions/{session_id}/output`：获取最近输出。

### Agent WebSocket

入口：`/ws/agent`

消息类型：

- `agent_hello`：agent 上报设备凭据、平台、版本和协议版本。
- `heartbeat`：心跳。
- `sync_sessions`：agent 上报本地会话注册表。
- `start_session`：服务端要求启动 PTY。
- `session_started`：agent 上报 PTY 启动成功。
- `stdin`：服务端转发用户输入。
- `resize`：服务端转发终端尺寸变化。
- `stdout`：agent 上报终端输出。
- `session_exit`：agent 上报进程退出。
- `close_session`：服务端要求关闭会话。
- `error`：错误事件。

### Web WebSocket

入口：`/ws/web`

消息类型：

- `subscribe_session`：网页订阅会话输出。
- `unsubscribe_session`：网页取消订阅。
- `stdin`：网页发送用户输入。
- `resize`：网页发送终端尺寸变化。
- `session_state`：服务端推送会话状态。
- `stdout`：服务端推送终端输出。
- `error`：错误事件。

## 主要流程

### 管理员初始化

第一次启动 `server` 时通过 CLI 参数或环境变量创建管理员账号。之后用户通过网页登录，服务端设置 HttpOnly session cookie。REST 和 WebSocket 复用同一登录态鉴权。

### Agent 配对

管理员在网页创建 agent 注册令牌。被控端执行：

```bash
vibe-agent register --server https://example.com --token <registration-token>
```

Agent 生成本机设备密钥和设备指纹，服务端校验令牌后签发设备凭据。后续 agent 使用设备凭据连接 `/ws/agent`。

### Agent 常驻连接

Agent 通过 WebSocket 建立控制通道，周期发送心跳、平台信息、版本号和本地会话摘要。服务端更新设备在线状态。连接断开时设备变为 `offline`，但会话不会立即删除。

### 网页打开终端

用户在网页设备页点击新建终端。Server 创建 `terminal_sessions` 记录，向对应 agent 下发 `start_session`。Agent 启动 PTY shell，返回本地进程信息，随后输出流经 agent、server、web。每个会话在网页中是一个标签。

### 网页断线或刷新

WebSocket 断开不会关闭 PTY。网页重新连接后，先通过 REST 拉取会话列表和最近输出，再通过 WebSocket 重新订阅会话。Agent 继续保留进程和缓冲。

### 服务端重启恢复

Server 启动后从 SQLite 恢复设备和会话元数据。Agent 自动重连并发送本地会话注册表、PTY 状态、最近输出序列号。Server 对比并修正 `terminal_sessions` 状态。网页看到可恢复会话。真实 shell 进程只要 agent 进程仍在，就继续存在。

### 被控端重启或 agent 崩溃

第一版不承诺恢复真实 PTY 进程。Agent 重启后读取本地会话注册表，将旧会话标记为 `lost`，并把最后缓冲上报。用户可以查看历史并新建终端。

### Docker workspace 生命周期

用户首次登录或 server 启动时确保用户级 workspace 容器存在。容器挂载专用数据卷，用于保存控制层辅助状态和审计日志文件。容器异常退出时 server 重新拉起。容器不可用时不影响 agent 执行命令，但会降低审计和缓冲持久性，并在 UI 显示告警。

## 错误处理

- 输入发送到离线设备：REST 或 WebSocket 返回明确错误，UI 禁用输入。
- 会话不存在或已退出：WebSocket 返回状态事件，终端标签变为只读。
- 输出分块落盘失败：继续实时转发，同时写入 `audit_events` 警告。
- Agent 协议版本不兼容：拒绝连接并记录审计事件。
- Workspace 创建失败：服务端仍可启动，但管理员首页显示部署错误。
- Agent 连接断开：设备显示为离线，会话显示为可恢复或等待重连。
- Agent 上报未知会话：服务端创建或修正对应会话元数据，并记录同步事件。

## 安全设计

- 服务端默认放在反向代理后，只暴露 HTTPS 入口。
- 不暴露 agent 裸端口、Docker 裸端口或 SQLite 文件。
- 管理员密码使用 Argon2id 或 bcrypt 哈希。
- Session cookie 使用 HttpOnly、Secure、SameSite。
- Agent 使用注册令牌配对，配对后改用设备凭据。
- 注册令牌支持过期、吊销和一次性使用。
- WebSocket 分为 `web` 和 `agent` 两类入口，都必须鉴权。
- 所有控制消息校验设备、会话和用户权限。
- 审计日志记录登录、注册、连接、会话创建、关闭、resize、错误和输入摘要。
- 默认不明文记录完整输入内容。

## 部署设计

第一版使用 Docker Compose 单机部署：

- `server` 容器运行 Go API，并内置或挂载 React 构建产物。
- SQLite 使用挂载数据卷，不单独起数据库容器。
- `workspace` 是由 server 管理的用户级长期容器。
- Server 需要受控访问 Docker socket，或通过受限 Docker API 代理管理 workspace。
- 反向代理建议使用 Caddy 或 Nginx。
- 部署文档提供 HTTPS 配置示例。

Agent 部署方式：

- Linux：支持 systemd 服务。
- macOS：支持 launchd 服务。
- WSL：支持手动启动脚本和前台 CLI。
- 所有平台支持前台 CLI 便于调试。

## 测试策略

### Go server

- 单元测试覆盖认证、令牌、设备状态、会话状态机、协议消息校验。
- 集成测试覆盖 SQLite 迁移、REST API、WebSocket 路由。
- Docker workspace 管理使用接口抽象，并用 fake Docker client 测试生命周期逻辑。

### Rust agent

- 单元测试覆盖协议序列化、会话注册表、缓冲序列号。
- 集成测试用本地 shell 和 PTY 验证启动、输入、resize、退出、重连摘要。
- 连接层测试覆盖断线重连和服务端重启后的 `sync_sessions`。

### Web

- 组件测试覆盖设备列表、多终端标签、断线状态、只读退出状态。
- 端到端测试使用 mock server 验证终端输入输出。
- xterm.js 集成测试聚焦终端挂载、输入回显、输出追加、resize 上报和只读状态，不测试第三方库内部行为。

### 部署

- `docker compose up` smoke test 验证 server 启动。
- 初始化管理员账号。
- 创建注册令牌。
- 模拟 agent 连接。
- 创建终端会话。
- 刷新网页后恢复会话。

## 验收标准

- VPS 上部署后，浏览器能登录并看到在线 agent。
- 被控端无需开放入站端口。
- 同一设备可打开多个终端标签。
- 网页刷新后终端仍可继续。
- 重启 server 后，只要 agent 没重启，终端会话可重新同步。
- Agent 崩溃或被控端重启后，旧会话显示为 `lost`，用户可查看最后历史并新建终端。
- 审计日志和最近输出能在页面恢复显示。
- Workspace 容器异常时页面显示告警，基础实时终端不被直接阻断。
- 所有 WebSocket 控制消息都经过鉴权和会话归属校验。

## 后续演进

- 手机 App 和移动端专用交互。
- 多用户和 RBAC。
- 原生 Windows 支持。
- mTLS、端到端加密、命令级审批和 IP 白名单。
- 文件浏览和远程编辑。
- 服务端 workspace 可选命令执行模式。
- PostgreSQL 和多节点部署。
