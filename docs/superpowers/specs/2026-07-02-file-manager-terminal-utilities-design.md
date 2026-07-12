# 实用功能第一批设计：文件管理器 + 终端搜索 + 快捷命令

日期：2026-07-02
状态：待用户审阅

## 背景与目标

vibe-terminal 目前只能通过终端敲命令操作被控机。本批次为日常使用补上三个最实用的能力：

1. **文件管理器**：在 Web 上浏览被控机目录、下载和上传文件（核心功能）。
2. **终端输出搜索**：在终端回滚缓冲中按 Ctrl+F 搜索（纯前端）。
3. **快捷命令**：保存常用命令片段，一键插入当前会话（小型全栈功能）。

三个功能各自独立交付，互不依赖。

## 范围决策与假设

用户请求为「添加更多实用功能」，方向未指定（提问未获回复，按最佳判断推进）。假设与理由：

- **假设 1**：文件传输是同类工具（JumpServer、Termius 等）的标配，也是本项目最大的能力空白，优先级最高。
- **假设 2**：输出搜索与快捷命令成本低、使用频率高，适合与文件管理器同批交付。
- **假设 3**：设备监控、分组、2FA、审计界面、会话回放、Windows agent 等价值也高，但留作后续批次（见文末路线图）。

若用户希望调整方向，本规格可整体替换。

## 功能 1：文件管理器

### 方案对比

| 方案 | 描述 | 优点 | 缺点 |
| --- | --- | --- | --- |
| A. 全 WebSocket | 浏览器↔服务端↔agent 全部走现有 WS 通道分块传输 | 复用现有通道 | 浏览器端无原生下载/上传体验，前端需自行拼装 Blob，大文件占内存 |
| **B. REST + WS 桥接（推荐）** | 浏览器用 REST 上传/下载，服务端把请求经现有 agent WS 通道转成分块请求-响应 | 浏览器原生下载条/XHR 上传进度；`Envelope.request_id` 字段本来就预留；复杂度集中在服务端桥接层 | 服务端需实现请求关联与流控；agent 通道内 base64 有 ~33% 开销 |
| C. agent 第二条二进制 WS 数据通道 | 文件流量走独立连接、二进制帧 | 无 base64 开销、与控制流量隔离 | 新增连接管理与认证，复杂度最高 |

**选 B**。MVP 阶段吞吐足够；未来若需要高吞吐，可只替换服务端↔agent 的传输层升级为 C，REST 接口与 Web UI 不变。

### 协议扩展（agent 通道，向后兼容）

所有 fs 消息复用现有 JSON `Envelope`，新增 `request_id` 关联请求与响应。协议版本保持 `v1`（可选扩展）。

**能力声明**：`agent_hello` 新增 `capabilities: ["fs"]` 字段。旧 agent 不发送该字段，服务端视为不支持，fs 请求直接返回明确错误，不必等待超时。双方 JSON 解析都忽略未知字段，新旧混搭不炸。

**消息列表**（`fs_error` 为所有请求的统一失败响应，含 `code`/`message`）：

| 请求（服务端→agent） | 响应（agent→服务端） | 说明 |
| --- | --- | --- |
| `fs_list` {path} | `fs_list_result` {path, entries[]} | entry: name, is_dir, size, mode, modified_at |
| `fs_read` {path, offset, length} | `fs_read_result` {data(base64), eof, file_size} | 无状态分块读（pread），块大小 256 KiB，服务端拉模式逐块请求，天然流控 |
| `fs_write_open` {path, size, overwrite} | `fs_write_opened` {} | agent 在目标目录创建临时文件 `.vibe-upload-<request_id>.tmp` |
| `fs_write_chunk` {offset, data(base64)} | `fs_write_ack` {offset} | 逐块确认后服务端再发下一块 |
| `fs_write_close` {total_size} | `fs_write_result` {} | 校验字节数后原子 rename 到目标路径 |

agent 侧维护 upload map（request_id → 临时文件句柄），60 秒无后续块自动放弃并删除临时文件。下载为无状态读，无需清理。

### REST API（管理员 Cookie 认证）

| 端点 | 说明 |
| --- | --- |
| `GET /api/devices/{id}/fs?path=/abs/path` | 目录列表 JSON |
| `GET /api/devices/{id}/fs/file?path=` | 流式下载，`Content-Disposition: attachment`，已知 `Content-Length` |
| `POST /api/devices/{id}/fs/file?path=&overwrite=false` | 请求体为原始文件流；目标存在且未 overwrite 时返回 409 |

### 服务端桥接层

新包 `server/internal/files`：

- `Service` 持有 pending map（request_id → 响应 channel），单请求超时 30 秒。
- router 的 agent 读循环把 `fs_*` 响应按 request_id 分发到 pending。
- `ws.Outbound` 增加 `RequestID` 字段，`Hub` 增加 `ToDevice(deviceID, Outbound)` 方法（agents map 已存在，只缺方法）。
- 每设备并发文件操作上限 4，超出返回 429。

### 限制与安全

- 路径必须为绝对路径；agent 按原样访问（管理员本来就拥有该机 shell 全权，文件 API 不引入新的权限面）。
- 上传单文件默认上限 512 MiB，配置项 `VIBE_FS_MAX_UPLOAD_SIZE` / YAML `fs_max_upload_size` 可调；下载不限制大小。
- 下载/上传完成写入 `audit_events`（event_type: `file_download` / `file_upload`，metadata 含 path 与字节数）；目录列表不记审计。

### 错误映射

| 错误情形 | HTTP |
| --- | --- |
| agent 离线 / 无 fs 能力 | 503（响应体说明原因） |
| not_found | 404 |
| permission_denied | 403 |
| not_a_file / not_a_directory | 400 |
| already_exists（上传未指定 overwrite） | 409 |
| 传输超时 | 504 |
| 并发超限（服务端判定） | 429 |

### Web UI

- 设备列表中在线设备增加「文件」按钮，打开右侧滑出的玻璃拟态抽屉面板（约 70% 宽），不改动现有终端标签结构。
- 面板：路径面包屑（可点击逐级跳转）＋路径输入框、条目列表（类型图标、名称、大小、修改时间，目录优先按名称排序）、目录双击进入、文件行内下载按钮。
- 顶部操作：上传（XHR 以获得 `upload.onprogress` 进度条）、刷新。
- 下载用隐藏 `<a download>` 触发浏览器原生下载（同源 Cookie 认证直接生效）。

## 功能 2：终端输出搜索

- 引入与现有 xterm 包配套的 search addon（计划阶段核对 `web/package.json` 中 xterm 的发行系与版本）。
- TerminalPane 内 Ctrl+F 唤出搜索条：输入框、上一个/下一个、大小写敏感开关、Esc 关闭，命中高亮。
- 纯前端改动，无服务端/agent 变更。
- 搜索范围为 xterm 回滚缓冲区（scrollback），不含仅存于磁盘的历史输出（见已知限制）。

## 功能 3：快捷命令

- 新表 `command_snippets`（id text 主键, name text not null, command text not null, created_at, updated_at），排序按 created_at。
- REST：`GET/POST /api/snippets`、`PUT/DELETE /api/snippets/{id}`，管理员 Cookie 认证。
- UI：终端区域顶栏「快捷命令」入口，展开列表；点击条目把命令文本经现有 `encodeStdin` 注入当前活动会话，**不自动追加回车**（用户确认后自行执行，防误触发）；管理界面（增删改）复用 AgentTokenManager 的面板交互模式。
- 单管理员系统，快捷命令全局共享，无按用户隔离。

## 测试策略

- **store**：`command_snippets` CRUD 单测。
- **protocol**：fs 消息 Go/Rust 两侧编解码往返测试。
- **files service**：用 `MemoryPeer` 模拟 agent，覆盖正常流、超时、并发上限、错误映射、旧 agent 无能力快速失败。
- **httpapi**：新端点认证、参数校验、错误码。
- **agent**：tempdir 真实文件的 fs_list / fs_read / fs_write 单测；上传中断后临时文件清理测试。
- **web**：文件面板渲染与目录导航、搜索条快捷键与命中、快捷命令点击调用 encodeStdin 的断言。
- 全量 `make test` 通过为完成标准。

## 明确不做（本批次）

- 文件删除、重命名、移动、新建目录（用户可在终端完成；降低误操作面）。
- 目录递归下载/打包下载。
- 断点续传；传输中文件被修改的一致性保证（无状态分块读可能读到撕裂内容，记为已知限制）。
- 搜索磁盘上的历史输出。
- 快捷命令变量占位符/参数化。

## 后续路线图（未纳入本批次）

1. 设备监控（CPU/内存/磁盘心跳上报）与设备分组。
2. 安全强化：2FA、IP 白名单、审计日志查看界面、会话只读分享。
3. 会话录像回放（输出已按 seq 落盘，具备基础）。
4. Windows 原生 agent。
