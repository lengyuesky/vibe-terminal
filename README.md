# vibe-terminal

一个基于浏览器的远程终端系统。`vibe-terminal` 由 Go 服务端、Rust agent 和 React/xterm.js Web UI 组成：agent 主动连接服务端并在被控机器上创建 PTY，浏览器通过服务端进行认证、会话管理和终端交互。

> 当前项目仍处于 MVP/实验阶段。请不要在未配置 HTTPS、强密码、网络访问控制和运行隔离的情况下暴露到公网。

## 特性

- 单管理员登录，基于 Cookie 维护 Web 会话。
- 可选的 TOTP Web 两步验证，并提供一次性恢复码。
- Rust agent 主动连回服务端，被控机器无需开放入站端口。
- 在线设备列表和多终端标签页。
- 前端管理 agent 注册 token，创建后显示一次性 token 和被控端运行命令。
- 支持撤销 agent 注册 token，并对已撤销 token 执行永久删除。
- 浏览器刷新后恢复会话列表和已持久化输出。
- 服务端重启后，agent 重连并同步本地记录的会话状态。
- 终端输入、输出、窗口尺寸 resize 同步。
- 会话删除确认、会话重命名、启动目录显示和状态颜色提示。
- 设备文件管理器：浏览目录、下载文件、上传文件（带进度与覆盖确认）。
- 终端输出搜索：Ctrl+F / Cmd+F 在回滚缓冲中查找并高亮。
- 快捷命令片段：保存常用命令，点击注入当前会话（不自动回车）。
- SQLite 保存设备、会话和审计元数据，终端输出落盘保存。
- Docker Compose、systemd、launchd、WSL 示例配置。

## 架构

```text
Browser
  └─ React + xterm.js
       ├─ REST API: 登录、设备、会话、历史输出
       └─ WebSocket /ws/web: stdin、resize、stdout、会话订阅

Go Server
  ├─ 认证和管理员会话
  ├─ SQLite 元数据存储
  ├─ 终端输出持久化
  ├─ WebSocket Hub
  └─ 静态 Web 资源服务

Rust Agent
  ├─ 注册和设备凭据
  ├─ WebSocket /ws/agent
  ├─ 本地 PTY 管理
  └─ 会话 registry 与输出缓冲
```

核心原则：命令只在 agent 所在机器执行，服务端负责认证、路由、状态和持久化。

## 项目结构

```text
server/   Go 服务端
agent/    Rust agent
web/      React Web UI
deploy/   Caddy、systemd、launchd、WSL 示例
```

## 环境要求

- Go
- Node.js 和 npm
- Rust/Cargo
- Docker Compose

仓库的 `Makefile` 默认把 Go/Rust 缓存放到项目内 `.tools/`。如果需要把 Rust 安装到项目目录：

```bash
mkdir -p .tools
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs -o .tools/rustup-init.sh
RUSTUP_HOME="$PWD/.tools/rustup" \
CARGO_HOME="$PWD/.tools/cargo" \
sh .tools/rustup-init.sh -y --no-modify-path --profile minimal --default-toolchain stable
```

## 快速开始

安装前端依赖并构建 Web UI：

```bash
cd web
npm install
npm run build
```

启动服务端：

```bash
cd server
VIBE_ADDR=127.0.0.1:8080 \
VIBE_DB=data/vibe-terminal.db \
VIBE_OUTPUT_ROOT=../workspace-data \
VIBE_WEB_DIR=../web/dist \
VIBE_SESSION_SECRET=dev-demo-session-secret-32-bytes-long \
VIBE_ADMIN_USER=admin \
VIBE_ADMIN_PASSWORD=admin123456 \
go run ./cmd/server
```

打开 Web UI：

```text
http://127.0.0.1:8080
```

默认账号来自上面的环境变量：

```text
admin / admin123456
```

在 Web UI 左侧进入 `Agent Tokens`，创建注册 token。创建成功后页面会显示一次性 token 和被控端运行命令。按页面中的命令在被控端注册并运行 agent：

```bash
vibe-agent register --server http://127.0.0.1:8080 --token <token>
vibe-agent run
```

agent 在线后，Web UI 左侧设备列表会出现该设备。点击 `New terminal` 创建终端会话。

源码开发时也可以直接用 Cargo 运行 agent：

```bash
cd agent
cargo run -- register --server http://127.0.0.1:8080 --token <token>
cargo run -- run
```

token 管理规则：

- `Revoke` 会撤销 token，使其不能再注册新 agent。
- 已撤销 token 会显示 `Delete`，二次确认后从数据库中永久删除。

## Web 两步验证

管理员登录后可在左侧进入 `Security`，按页面提示输入当前密码、扫描二维码并提交验证器中的动态验证码来启用两步验证。该功能使用标准 TOTP，兼容支持 RFC 6238 的验证器应用。

启用或重新生成恢复码时会创建 10 个一次性恢复码；这些恢复码只显示一次，每个码使用后立即失效。请在离线、受保护的位置保存它们，不要只保存在运行验证器的同一台设备上。

验证器设备不可用时，根据是否还有其他可用验证器选择恢复流程：

- 若仍有其他可用验证器，正常登录后进入 `Security`，使用当前密码和该验证器生成的 TOTP 重新生成恢复码，并离线保存新生成的 10 个码。
- 若所有验证器均已丢失，先在登录页面使用一个未消费的恢复码登录，再进入 `Security`，使用当前密码执行 `Disable two-factor authentication`；该操作会删除旧恢复码。随后重新执行 `Enable two-factor authentication` 绑定新验证器，并离线保存新生成的 10 个恢复码。

TOTP 依赖服务端与验证器设备的时间基本同步。生产环境应为服务端和验证器设备启用可靠的 NTP 时间同步；时间偏差过大时，即使验证码本身正确也会验证失败。

TOTP 密钥使用由 `session_secret` 派生的密钥加密后保存。启用两步验证后必须稳定、持久地保存同一个 `session_secret`；更换或丢失它会导致已保存的 TOTP 密钥无法解密，现有两步验证配置也将无法正常使用。备份和迁移数据库时，应同时以安全方式备份对应的 `session_secret`。

两步验证只是在密码之外增加一道登录校验，不能替代 HTTPS、强管理员密码、网络访问控制和运行隔离。不要因为启用了两步验证就把未加密或缺少访问限制的服务直接暴露到公网。

## 配置

Docker Compose 部署通过 `config.yaml` 配置服务端。先复制示例文件：

```bash
cp config.example.yaml config.yaml
```

`config.yaml` 字段：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `addr` | `:8080` | HTTP/WebSocket 监听地址 |
| `database_path` | `data/vibe-terminal.db` | SQLite 数据库路径 |
| `output_root` | `workspace-data` | 终端输出持久化目录 |
| `web_dir` | `web/dist` | 前端静态资源目录 |
| `session_secret` | 开发默认值 | Cookie 签名密钥，生产环境必须替换 |
| `admin_username` | 空 | 启动时创建的管理员用户名 |
| `admin_password` | 空 | 启动时创建的管理员密码 |
| `fs_max_upload_size` | `536870912` | 上传单文件大小上限（字节） |

服务端启动时会读取 `VIBE_CONFIG` 指向的 YAML 文件；未设置 `VIBE_CONFIG` 时仍使用默认值和环境变量。以下环境变量可覆盖 YAML 中的同名配置：`VIBE_ADDR`、`VIBE_DB`、`VIBE_OUTPUT_ROOT`、`VIBE_WEB_DIR`、`VIBE_SESSION_SECRET`、`VIBE_ADMIN_USER`、`VIBE_ADMIN_PASSWORD`、`VIBE_FS_MAX_UPLOAD_SIZE`。

agent 注册后会把设备 ID、服务端地址和凭据保存到用户配置目录。运行 `cargo run -- run` 时会读取该配置并连接服务端。

## 会话状态

会话标签包含标题、启动目录、状态和短会话 ID。状态颜色用于快速判断可用性：

| 状态 | 含义 | UI 颜色 |
| --- | --- | --- |
| `running` | 会话正常运行 | 绿色 |
| `starting` | 服务端已创建会话，等待 agent 启动 PTY | 蓝色 |
| `lost` | 服务端知道会话，但当前不可写或 agent 丢失 | 橙红色 |
| `exited` | PTY 进程已退出，只能查看输出 | 灰色 |
| `closed` | 会话已关闭，不显示在标签列表 | 不显示 |

标签中的目录来自创建会话时的 `working_directory`。它不是 shell 内执行 `cd` 后的实时工作目录。

## 会话恢复

浏览器刷新后，前端会重新加载设备、会话和历史输出。只要数据库和输出目录保留，已有会话会重新出现在标签栏中。

服务端重启后，agent 重连时会同步本地 registry 中记录的会话。被控机器重启或 agent 进程崩溃后，真实 PTY 进程不能恢复，相关会话会进入 `lost` 或不可写状态。

## 测试

运行完整检查：

```bash
make test
```

该命令包括：

- `cd server && go test ./...`
- `cd agent && cargo test`
- `cd web && npm test -- --run && npm run build`
- `docker compose config >/dev/null`

也可以按模块运行：

```bash
make test-server
make test-agent
make test-web
make docker-config
```

## 部署

预构建镜像会推送到 GitHub Container Registry：

```bash
docker pull ghcr.io/lengyuesky/vibe-terminal:v1.0
docker pull ghcr.io/lengyuesky/vibe-terminal:latest
```

Docker Compose：

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml，至少替换 session_secret 和 admin_password
docker compose pull
docker compose up -d
```

建议把服务部署在 Caddy 或 Nginx 后面，并只通过 HTTPS 暴露。示例配置：

- `deploy/Caddyfile.example`

agent 常驻运行模板：

- Linux：`deploy/systemd/vibe-agent.service`
- macOS：`deploy/launchd/com.vibe-terminal.agent.plist`
- WSL：`deploy/scripts/vibe-agent-wsl.sh`

## Release

`v1.0` 已发布 GitHub Release：

```text
https://github.com/lengyuesky/vibe-terminal/releases/tag/v1.0
```

Release 附带以下 agent 可执行文件包：

- `vibe-agent-linux-x86_64.tar.gz`
- `vibe-agent-macos-aarch64.tar.gz`
- `vibe-agent-macos-x86_64.tar.gz`

## 协议

入口：

- `/ws/agent`：agent 控制通道，使用设备凭据认证。
- `/ws/web`：Web UI 控制通道，使用管理员 Cookie 认证。

主要消息类型：

- `agent_hello`
- `sync_sessions`
- `start_session`
- `session_started`
- `stdin`
- `resize`
- `stdout`
- `session_exit`
- `close_session`
- `subscribe_session`
- `session_state`
- `error`
- `fs_list`
- `fs_list_result`
- `fs_read`
- `fs_read_result`
- `fs_write_open`
- `fs_write_opened`
- `fs_write_chunk`
- `fs_write_ack`
- `fs_write_close`
- `fs_write_result`
- `fs_error`

`agent_hello` 可携带 `capabilities` 字段声明 agent 能力，声明 `fs` 后服务端才会向该设备转发文件消息。

## 安全注意

- 当前是单管理员模型，不包含 RBAC。
- 生产环境必须设置强随机 `VIBE_SESSION_SECRET` 和强管理员密码。
- 两步验证不能替代 HTTPS、强密码、网络访问控制或运行隔离。
- 建议只通过 HTTPS 使用 Web 和 WebSocket。
- 当前没有端到端加密、命令审批、IP 白名单或细粒度审计策略。
- agent 拥有在被控机器上执行 shell 的能力，请只在可信机器和可信网络中运行。

## 已知限制

- 不支持原生 Windows PowerShell/CMD agent。
- 被控机器重启或 agent 崩溃后，真实 PTY 进程无法恢复。
- 会话目录显示的是启动目录，不是 shell 实时工作目录。
- 当前不包含多节点部署、Kubernetes 示例或高可用方案。
- 文件传输期间被控端文件被并发修改时，下载内容可能撕裂（无快照一致性）。
- 终端搜索只作用于浏览器内的回滚缓冲，不搜索仅存于磁盘的历史输出。
- 文件管理器不提供删除/重命名/新建目录，请在终端内完成。

## 许可证

当前仓库尚未声明开源许可证。对外公开前请先添加明确的 `LICENSE` 文件。
