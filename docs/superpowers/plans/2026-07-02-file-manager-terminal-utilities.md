# 实用功能第一批实现计划：文件管理器 + 终端搜索 + 快捷命令

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 vibe-terminal 增加三个独立交付的实用功能——设备文件管理器（浏览/下载/上传）、终端输出搜索（Ctrl+F）、快捷命令片段（保存并一键注入会话）。

**Architecture:** 文件传输采用「REST + WS 桥接」：浏览器走 REST（原生下载、XHR 上传进度），服务端新包 `internal/files` 把请求经现有 agent WebSocket 通道转成带 `request_id` 的分块请求-响应（下载为服务端拉模式逐块请求，上传为逐块确认 + 临时文件原子 rename）。终端搜索纯前端（xterm search addon）。快捷命令为 SQLite 新表 + REST CRUD + 前端注入（不自动回车）。

**Tech Stack:** Go 服务端（coder/websocket、modernc.org/sqlite）、Rust agent（tokio、tokio-tungstenite、serde、base64、dirs）、React + TypeScript + xterm 5.3（旧发行系，非 @xterm scope）+ vitest。

**规格文档:** `docs/superpowers/specs/2026-07-02-file-manager-terminal-utilities-design.md`

## Global Constraints

- Go module 路径：`github.com/djy/vibe-terminal/server`；Rust crate 名：`vibe_agent`。
- 协议版本保持 `"v1"` 不变；fs 消息是可选扩展；capability 常量为 `"fs"`。
- 分块大小：256 KiB（262144 字节）；上传单文件默认上限 512 MiB（536870912 字节），配置 YAML `fs_max_upload_size` / 环境变量 `VIBE_FS_MAX_UPLOAD_SIZE`。
- 服务端等待 agent 单次响应超时：30 秒；每设备并发文件操作上限：4；agent 侧上传 60 秒无活动自动清理。
- base64：Go 用 `encoding/base64.StdEncoding`，Rust 用 `base64::engine::general_purpose::STANDARD`。
- 前端 xterm 是旧发行系：搜索插件必须用 `xterm-addon-search@^0.13.0`（不是 `@xterm/addon-search`）。
- 所有新 REST 端点用现有 `r.requireUser` 管理员 Cookie 认证；错误响应用现有 `writeError(w, status, code, message)` 格式。
- 提交信息遵循现有 conventional 风格（`feat:`、`test:`、`docs:`，web 相关用 `feat(web):`）。
- 代码注释极少、必要时用中文（现有惯例）；Web UI 文案用英文（匹配现有 "New terminal" 等）。
- 每个任务完成时该模块测试必须全绿：server `cd server && go test ./...`；agent `cd agent && cargo test`；web `cd web && npm test -- --run`。
- 仓库根目录为工作目录基准，所有路径相对仓库根。

## 文件结构总览

| 文件 | 动作 | 职责 |
| --- | --- | --- |
| `server/internal/protocol/messages.go` | 修改 | fs 消息类型常量与结构体、AgentHello.Capabilities、EncodeEnvelopeWithRequest |
| `server/internal/ws/hub.go` | 修改 | Outbound 增加 RequestID、Hub.ToDevice |
| `server/internal/devices/service.go` | 修改 | Presence 记录设备 capabilities |
| `server/internal/files/service.go` | 新建 | 请求-响应桥接：List/Download/Upload/HandleAgentResponse、超时、并发限制、错误码 |
| `server/internal/httpapi/router.go` | 修改 | fs REST 端点、agent fs 响应路由、hello capabilities、socketPeer request_id 编码、snippets REST |
| `server/internal/config/config.go` | 修改 | FsMaxUploadSize 配置 |
| `server/internal/store/store.go` | 修改 | command_snippets 表 + CRUD |
| `server/cmd/server/main.go` | 修改 | 传入 FsMaxUploadSize |
| `agent/src/protocol.rs` | 修改 | Envelope.request_id、fs 消息结构体 |
| `agent/src/fs.rs` | 新建 | list_dir/read_chunk/UploadManager + fs 消息 handler（纯函数，可单测） |
| `agent/src/client.rs` | 修改 | fs 消息分发、hello capabilities、上传清理、send_payload 增加 request_id |
| `agent/src/lib.rs` | 修改 | 声明 `pub mod fs` |
| `web/src/api.ts` | 修改 | fs 与 snippets API 客户端、XHR 上传 |
| `web/src/components/FileManagerPanel.tsx` | 新建 | 文件面板（面包屑、列表、下载、上传进度） |
| `web/src/components/TerminalSearchBar.tsx` | 新建 | 搜索条 UI |
| `web/src/components/SnippetsBar.tsx` | 新建 | 快捷命令下拉与管理 |
| `web/src/components/TerminalPane.tsx` | 修改 | search addon、Ctrl+F、forwardRef 暴露 sendText |
| `web/src/components/TerminalTabs.tsx` | 修改 | 接入 SnippetsBar 与 paneRef |
| `web/src/components/DeviceList.tsx` | 修改 | 设备行「文件」按钮 |
| `web/src/App.tsx` | 修改 | 文件面板开关状态 |
| `web/src/styles.css` | 修改 | 新组件样式（用现有玻璃令牌 `--glass-bg` 等） |
| `README.md`、`docs/protocol/v1.md`、`config.example.yaml` | 修改 | 文档与示例配置 |

任务依赖：1→2→3→4（服务端链）；1→5→6→7（agent 链，5 依赖 1 仅为字段命名对齐，可并行）；4+7→8→9（Web 文件面板）；10 独立（搜索）；11→12→13（快捷命令链）；14 收尾。

---

### Task 1: Go 协议层——fs 消息类型、capabilities 与带 request_id 的编码

**Files:**
- Modify: `server/internal/protocol/messages.go`
- Test: `server/internal/protocol/messages_test.go`

**Interfaces:**
- Consumes: 现有 `Envelope`（已含 `RequestID string \`json:"request_id,omitempty"\``）。
- Produces（后续任务依赖的确切名字）：
  - 类型常量：`TypeFsList = "fs_list"`、`TypeFsListResult = "fs_list_result"`、`TypeFsRead = "fs_read"`、`TypeFsReadResult = "fs_read_result"`、`TypeFsWriteOpen = "fs_write_open"`、`TypeFsWriteOpened = "fs_write_opened"`、`TypeFsWriteChunk = "fs_write_chunk"`、`TypeFsWriteAck = "fs_write_ack"`、`TypeFsWriteClose = "fs_write_close"`、`TypeFsWriteResult = "fs_write_result"`、`TypeFsError = "fs_error"`；`CapabilityFs = "fs"`。
  - 结构体：`FsList{Path string}`、`FsEntry{Name string; IsDir bool; Size int64; Mode uint32; ModifiedAt int64}`、`FsListResult{Path string; Entries []FsEntry}`、`FsRead{Path string; Offset int64; Length int}`、`FsReadResult{Data string; EOF bool; FileSize int64}`、`FsWriteOpen{UploadID, Path string; Size int64; Overwrite bool}`、`FsWriteOpened{UploadID string}`、`FsWriteChunk{UploadID string; Offset int64; Data string}`、`FsWriteAck{UploadID string; Offset int64}`、`FsWriteClose{UploadID string; TotalSize int64}`、`FsWriteResult{UploadID string}`、`FsError{Code, Message string}`。
  - `AgentHello` 增加 `Capabilities []string \`json:"capabilities,omitempty"\``。
  - `func EncodeEnvelopeWithRequest(messageType string, requestID string, payload any) ([]byte, error)`。

- [ ] **Step 1: 写失败测试**

在 `server/internal/protocol/messages_test.go` 末尾追加：

```go
func TestEncodeEnvelopeWithRequestRoundTrip(t *testing.T) {
	payload := FsRead{Path: "/tmp/demo.txt", Offset: 262144, Length: 262144}
	data, err := EncodeEnvelopeWithRequest(TypeFsRead, "req-1", payload)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != TypeFsRead || env.RequestID != "req-1" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	var got FsRead
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Path != "/tmp/demo.txt" || got.Offset != 262144 || got.Length != 262144 {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestAgentHelloCarriesCapabilities(t *testing.T) {
	raw := []byte(`{"device_id":"dev-1","credential":"c","platform":"linux","agent_version":"0.1.0","protocol_version":"v1","capabilities":["fs"],"sessions":[]}`)
	var hello AgentHello
	if err := json.Unmarshal(raw, &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if len(hello.Capabilities) != 1 || hello.Capabilities[0] != CapabilityFs {
		t.Fatalf("capabilities = %#v", hello.Capabilities)
	}
}

func TestFsErrorRoundTrip(t *testing.T) {
	data, err := EncodeEnvelopeWithRequest(TypeFsError, "req-2", FsError{Code: "not_found", Message: "missing"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	env, err := DecodeEnvelope(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got FsError
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.Code != "not_found" {
		t.Fatalf("code = %q", got.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/protocol/`
Expected: FAIL，报 `undefined: FsRead`、`undefined: EncodeEnvelopeWithRequest` 等编译错误。

- [ ] **Step 3: 实现**

在 `server/internal/protocol/messages.go` 中：

3a. 在现有 const 块（`TypeError` 之后）追加：

```go
	TypeFsList        = "fs_list"
	TypeFsListResult  = "fs_list_result"
	TypeFsRead        = "fs_read"
	TypeFsReadResult  = "fs_read_result"
	TypeFsWriteOpen   = "fs_write_open"
	TypeFsWriteOpened = "fs_write_opened"
	TypeFsWriteChunk  = "fs_write_chunk"
	TypeFsWriteAck    = "fs_write_ack"
	TypeFsWriteClose  = "fs_write_close"
	TypeFsWriteResult = "fs_write_result"
	TypeFsError       = "fs_error"
```

3b. const 块后新增：

```go
const CapabilityFs = "fs"
```

3c. `AgentHello` 结构体在 `Sessions` 字段前加：

```go
	Capabilities []string `json:"capabilities,omitempty"`
```

3d. 在 `EncodeEnvelope` 之后新增：

```go
func EncodeEnvelopeWithRequest(messageType string, requestID string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	env := Envelope{Type: messageType, RequestID: requestID, Payload: raw}
	if sessionPayload, ok := payload.(interface{ SessionIdentifier() string }); ok {
		env.SessionID = sessionPayload.SessionIdentifier()
	}
	return json.Marshal(env)
}
```

3e. 文件末尾追加 fs 消息结构体：

```go
type FsList struct {
	Path string `json:"path"`
}

type FsEntry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModifiedAt int64  `json:"modified_at"`
}

type FsListResult struct {
	Path    string    `json:"path"`
	Entries []FsEntry `json:"entries"`
}

type FsRead struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
}

type FsReadResult struct {
	Data     string `json:"data"`
	EOF      bool   `json:"eof"`
	FileSize int64  `json:"file_size"`
}

type FsWriteOpen struct {
	UploadID  string `json:"upload_id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Overwrite bool   `json:"overwrite"`
}

type FsWriteOpened struct {
	UploadID string `json:"upload_id"`
}

type FsWriteChunk struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
	Data     string `json:"data"`
}

type FsWriteAck struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
}

type FsWriteClose struct {
	UploadID  string `json:"upload_id"`
	TotalSize int64  `json:"total_size"`
}

type FsWriteResult struct {
	UploadID string `json:"upload_id"`
}

type FsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd server && go test ./internal/protocol/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add server/internal/protocol/messages.go server/internal/protocol/messages_test.go
git commit -m "feat(server): add fs protocol messages and agent capabilities"
```

---

### Task 2: ws 包——Outbound.RequestID 与 Hub.ToDevice

**Files:**
- Modify: `server/internal/ws/hub.go`
- Modify: `server/internal/httpapi/router.go`（仅 `socketPeer.Send`）
- Test: `server/internal/ws/hub_test.go`

**Interfaces:**
- Consumes: Task 1 的 `protocol.EncodeEnvelopeWithRequest`。
- Produces:
  - `ws.Outbound` 增加字段 `RequestID string`。
  - `func (h *Hub) ToDevice(deviceID string, msg Outbound) error`——设备未连接返回 `ErrNoAgent`。
  - `socketPeer.Send` 在 `msg.RequestID != ""` 时用 `EncodeEnvelopeWithRequest` 编码（agent 收到的 envelope 顶层带 `request_id`）。

- [ ] **Step 1: 写失败测试**

在 `server/internal/ws/hub_test.go` 末尾追加：

```go
func TestHubToDeviceDeliversWithRequestID(t *testing.T) {
	hub := NewHub()
	agent := NewMemoryPeer("agent-dev-1")
	hub.AttachAgent("dev-1", agent)

	err := hub.ToDevice("dev-1", Outbound{Type: protocol.TypeFsList, RequestID: "req-1", Payload: protocol.FsList{Path: "/tmp"}})
	if err != nil {
		t.Fatalf("to device: %v", err)
	}
	got := agent.Pop()
	if got.Type != protocol.TypeFsList || got.RequestID != "req-1" {
		t.Fatalf("unexpected message: %#v", got)
	}
}

func TestHubToDeviceErrorsWhenAgentMissing(t *testing.T) {
	hub := NewHub()
	if err := hub.ToDevice("dev-x", Outbound{Type: protocol.TypeFsList}); err != ErrNoAgent {
		t.Fatalf("err = %v, want ErrNoAgent", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/ws/`
Expected: FAIL，`unknown field RequestID`、`hub.ToDevice undefined`。

- [ ] **Step 3: 实现**

3a. `hub.go` 中 `Outbound` 改为：

```go
type Outbound struct {
	Type      string
	SessionID string
	RequestID string
	Payload   any
}
```

3b. 在 `DetachAgent` 之后新增：

```go
func (h *Hub) ToDevice(deviceID string, msg Outbound) error {
	h.mu.RLock()
	agent, ok := h.agents[deviceID]
	h.mu.RUnlock()
	if !ok {
		return ErrNoAgent
	}
	return agent.Send(msg)
}
```

3c. `server/internal/httpapi/router.go` 中 `socketPeer.Send` 整体替换为：

```go
func (p *socketPeer) Send(msg wshub.Outbound) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	var err error
	if msg.RequestID != "" {
		data, err = protocol.EncodeEnvelopeWithRequest(msg.Type, msg.RequestID, msg.Payload)
	} else {
		data, err = protocol.EncodeEnvelope(msg.Type, msg.Payload)
	}
	if err != nil {
		return err
	}
	return p.conn.Write(ctx, websocket.MessageText, data)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd server && go test ./...`
Expected: PASS（全包，确认无处依赖 Outbound 位置初始化）

- [ ] **Step 5: 提交**

```bash
git add server/internal/ws/hub.go server/internal/ws/hub_test.go server/internal/httpapi/router.go
git commit -m "feat(server): route request-scoped messages to a device peer"
```

---

### Task 3: Presence capabilities 与 files 桥接服务

**Files:**
- Modify: `server/internal/devices/service.go`
- Create: `server/internal/files/service.go`
- Test: `server/internal/devices/service_test.go`（新建）、`server/internal/files/service_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 的 protocol fs 类型；Task 2 的 `ws.Outbound.RequestID`、`Hub.ToDevice`、`ws.MemoryPeer`。
- Produces:
  - `devices.Presence` 新方法：`SetCapabilities(deviceID string, capabilities []string)`、`HasCapability(deviceID string, capability string) bool`；`Set(deviceID, false)` 时清空该设备 capabilities。
  - `files.OpError{Code, Message string}`，实现 `error`；错误码常量：`CodeAgentUnsupported = "agent_unsupported"`、`CodeAgentOffline = "agent_offline"`、`CodeTimeout = "timeout"`、`CodeBusy = "busy"`、`CodeAgentError = "agent_error"`（agent 侧回传的 `not_found` 等码原样透传进 OpError.Code）。
  - `files.AgentSender` 接口 `{ ToDevice(deviceID string, msg ws.Outbound) error }`；`files.CapabilityChecker` 接口 `{ HasCapability(deviceID string, capability string) bool }`。
  - `files.NewService(sender AgentSender, caps CapabilityChecker) *Service`（默认 timeout 30s、chunkSize 262144、maxPerDevice 4；均为非导出字段，测试同包可改）。
  - 方法（Task 4 依赖的确切签名）：
    - `func (s *Service) List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error)`
    - `func (s *Service) Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error`
    - `func (s *Service) Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error`
    - `func (s *Service) HandleAgentResponse(env protocol.Envelope) bool`

- [ ] **Step 1: 写 Presence 失败测试**

新建 `server/internal/devices/service_test.go`：

```go
package devices

import "testing"

func TestPresenceCapabilities(t *testing.T) {
	p := NewPresence()
	p.Set("dev-1", true)
	p.SetCapabilities("dev-1", []string{"fs"})
	if !p.HasCapability("dev-1", "fs") {
		t.Fatal("expected fs capability")
	}
	if p.HasCapability("dev-1", "gpu") {
		t.Fatal("unexpected gpu capability")
	}
	if p.HasCapability("dev-2", "fs") {
		t.Fatal("unknown device should have no capability")
	}
	p.Set("dev-1", false)
	if p.HasCapability("dev-1", "fs") {
		t.Fatal("capabilities must clear on disconnect")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/devices/`
Expected: FAIL，`p.SetCapabilities undefined`。

- [ ] **Step 3: 实现 Presence 扩展**

`server/internal/devices/service.go` 整体替换为：

```go
package devices

import "sync"

type Presence struct {
	mu           sync.RWMutex
	online       map[string]bool
	capabilities map[string]map[string]bool
}

func NewPresence() *Presence {
	return &Presence{online: map[string]bool{}, capabilities: map[string]map[string]bool{}}
}

func (p *Presence) Set(deviceID string, online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online[deviceID] = online
	if !online {
		delete(p.capabilities, deviceID)
	}
}

func (p *Presence) Online(deviceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.online[deviceID]
}

func (p *Presence) SetCapabilities(deviceID string, capabilities []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := map[string]bool{}
	for _, capability := range capabilities {
		set[capability] = true
	}
	p.capabilities[deviceID] = set
}

func (p *Presence) HasCapability(deviceID string, capability string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities[deviceID][capability]
}
```

Run: `cd server && go test ./internal/devices/` → PASS

- [ ] **Step 4: 写 files.Service 失败测试**

新建 `server/internal/files/service_test.go`。核心手法：真实 `ws.Hub` + `ws.MemoryPeer` 当 agent，后台 goroutine 轮询 `peer.Pop()` 并按消息类型构造响应回灌 `HandleAgentResponse`：

```go
package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/protocol"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

type stubCaps bool

func (s stubCaps) HasCapability(string, string) bool { return bool(s) }

func newTestService(t *testing.T, respond func(out wshub.Outbound) (string, any)) (*Service, func()) {
	t.Helper()
	hub := wshub.NewHub()
	peer := wshub.NewMemoryPeer("agent-dev-1")
	hub.AttachAgent("dev-1", peer)
	svc := NewService(hub, stubCaps(true))
	svc.timeout = 2 * time.Second
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			out := peer.Pop()
			if out.Type == "" {
				time.Sleep(time.Millisecond)
				continue
			}
			replyType, replyPayload := respond(out)
			if replyType == "" {
				continue
			}
			raw, err := json.Marshal(replyPayload)
			if err != nil {
				panic(err)
			}
			svc.HandleAgentResponse(protocol.Envelope{Type: replyType, RequestID: out.RequestID, Payload: raw})
		}
	}()
	return svc, func() { close(stop); <-done }
}

func TestListRoundTrip(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		if out.Type != protocol.TypeFsList {
			t.Errorf("unexpected type %q", out.Type)
		}
		return protocol.TypeFsListResult, protocol.FsListResult{
			Path:    "/home/dev",
			Entries: []protocol.FsEntry{{Name: "notes.txt", Size: 12}},
		}
	})
	defer stop()
	result, err := svc.List(context.Background(), "dev-1", "~")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Path != "/home/dev" || len(result.Entries) != 1 || result.Entries[0].Name != "notes.txt" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestListPropagatesAgentError(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		return protocol.TypeFsError, protocol.FsError{Code: "not_found", Message: "no such dir"}
	})
	defer stop()
	_, err := svc.List(context.Background(), "dev-1", "/missing")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != "not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadStreamsUntilEOF(t *testing.T) {
	content := []byte("hello world!")
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		req := out.Payload.(protocol.FsRead)
		end := req.Offset + int64(req.Length)
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		chunk := content[req.Offset:end]
		return protocol.TypeFsReadResult, protocol.FsReadResult{
			Data:     base64.StdEncoding.EncodeToString(chunk),
			EOF:      len(chunk) < req.Length,
			FileSize: int64(len(content)),
		}
	})
	defer stop()
	svc.chunkSize = 5
	var buf bytes.Buffer
	var gotSize int64
	err := svc.Download(context.Background(), "dev-1", "/tmp/hello.txt", func(size int64) { gotSize = size }, &buf)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != "hello world!" || gotSize != 12 {
		t.Fatalf("data=%q size=%d", buf.String(), gotSize)
	}
}

func TestUploadSendsOpenChunksClose(t *testing.T) {
	var received bytes.Buffer
	var sawOpen, sawClose bool
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		switch out.Type {
		case protocol.TypeFsWriteOpen:
			req := out.Payload.(protocol.FsWriteOpen)
			sawOpen = true
			if req.Path != "/tmp/up.bin" || req.Overwrite {
				t.Errorf("unexpected open: %#v", req)
			}
			return protocol.TypeFsWriteOpened, protocol.FsWriteOpened{UploadID: req.UploadID}
		case protocol.TypeFsWriteChunk:
			req := out.Payload.(protocol.FsWriteChunk)
			data, err := base64.StdEncoding.DecodeString(req.Data)
			if err != nil {
				t.Errorf("chunk decode: %v", err)
			}
			received.Write(data)
			return protocol.TypeFsWriteAck, protocol.FsWriteAck{UploadID: req.UploadID, Offset: req.Offset + int64(len(data))}
		case protocol.TypeFsWriteClose:
			req := out.Payload.(protocol.FsWriteClose)
			sawClose = true
			if req.TotalSize != 12 {
				t.Errorf("total = %d", req.TotalSize)
			}
			return protocol.TypeFsWriteResult, protocol.FsWriteResult{UploadID: req.UploadID}
		}
		return "", nil
	})
	defer stop()
	svc.chunkSize = 5
	err := svc.Upload(context.Background(), "dev-1", "/tmp/up.bin", 12, false, strings.NewReader("hello world!"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !sawOpen || !sawClose || received.String() != "hello world!" {
		t.Fatalf("open=%v close=%v data=%q", sawOpen, sawClose, received.String())
	}
}

func TestTimeoutWhenAgentSilent(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) { return "", nil })
	defer stop()
	svc.timeout = 50 * time.Millisecond
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeTimeout {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsupportedAgentFailsFast(t *testing.T) {
	hub := wshub.NewHub()
	svc := NewService(hub, stubCaps(false))
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeAgentUnsupported {
		t.Fatalf("err = %v", err)
	}
}

func TestOfflineAgent(t *testing.T) {
	hub := wshub.NewHub()
	svc := NewService(hub, stubCaps(true))
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeAgentOffline {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	svc, stop := newTestService(t, func(out wshub.Outbound) (string, any) {
		return protocol.TypeFsListResult, protocol.FsListResult{Path: "/tmp"}
	})
	defer stop()
	svc.maxPerDevice = 1
	if err := svc.acquire("dev-1"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_, err := svc.List(context.Background(), "dev-1", "/tmp")
	opErr, ok := err.(*OpError)
	if !ok || opErr.Code != CodeBusy {
		t.Fatalf("err = %v", err)
	}
	svc.release("dev-1")
	if _, err := svc.List(context.Background(), "dev-1", "/tmp"); err != nil {
		t.Fatalf("after release: %v", err)
	}
}
```

注意：`MemoryPeer` 存的是 `Outbound.Payload any`（未序列化的原结构体），所以测试里直接类型断言 `out.Payload.(protocol.FsRead)`。

- [ ] **Step 5: 运行确认失败**

Run: `cd server && go test ./internal/files/`
Expected: FAIL（包不存在 / 未定义）。

- [ ] **Step 6: 实现 files.Service**

新建 `server/internal/files/service.go`：

```go
package files

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/djy/vibe-terminal/server/internal/protocol"
	wshub "github.com/djy/vibe-terminal/server/internal/ws"
)

const (
	CodeAgentUnsupported = "agent_unsupported"
	CodeAgentOffline     = "agent_offline"
	CodeTimeout          = "timeout"
	CodeBusy             = "busy"
	CodeAgentError       = "agent_error"
)

type OpError struct {
	Code    string
	Message string
}

func (e *OpError) Error() string { return e.Code + ": " + e.Message }

type AgentSender interface {
	ToDevice(deviceID string, msg wshub.Outbound) error
}

type CapabilityChecker interface {
	HasCapability(deviceID string, capability string) bool
}

type Service struct {
	sender       AgentSender
	caps         CapabilityChecker
	timeout      time.Duration
	chunkSize    int
	maxPerDevice int

	mu      sync.Mutex
	pending map[string]chan protocol.Envelope
	active  map[string]int
}

func NewService(sender AgentSender, caps CapabilityChecker) *Service {
	return &Service{
		sender:       sender,
		caps:         caps,
		timeout:      30 * time.Second,
		chunkSize:    256 * 1024,
		maxPerDevice: 4,
		pending:      map[string]chan protocol.Envelope{},
		active:       map[string]int{},
	}
}

// HandleAgentResponse 按 request_id 把 agent 响应交给等待中的调用，返回是否已消费。
func (s *Service) HandleAgentResponse(env protocol.Envelope) bool {
	if env.RequestID == "" {
		return false
	}
	s.mu.Lock()
	ch, ok := s.pending[env.RequestID]
	if ok {
		delete(s.pending, env.RequestID)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	ch <- env
	return true
}

func (s *Service) List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error) {
	if err := s.checkDevice(deviceID); err != nil {
		return protocol.FsListResult{}, err
	}
	if err := s.acquire(deviceID); err != nil {
		return protocol.FsListResult{}, err
	}
	defer s.release(deviceID)
	env, err := s.roundTrip(ctx, deviceID, protocol.TypeFsList, protocol.FsList{Path: path})
	if err != nil {
		return protocol.FsListResult{}, err
	}
	return decodeResult[protocol.FsListResult](env, protocol.TypeFsListResult)
}

func (s *Service) Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error {
	if err := s.checkDevice(deviceID); err != nil {
		return err
	}
	if err := s.acquire(deviceID); err != nil {
		return err
	}
	defer s.release(deviceID)
	offset := int64(0)
	for {
		env, err := s.roundTrip(ctx, deviceID, protocol.TypeFsRead, protocol.FsRead{Path: path, Offset: offset, Length: s.chunkSize})
		if err != nil {
			return err
		}
		result, err := decodeResult[protocol.FsReadResult](env, protocol.TypeFsReadResult)
		if err != nil {
			return err
		}
		if offset == 0 && onSize != nil {
			onSize(result.FileSize)
		}
		data, err := base64.StdEncoding.DecodeString(result.Data)
		if err != nil {
			return &OpError{Code: CodeAgentError, Message: "invalid chunk encoding"}
		}
		if len(data) > 0 {
			if _, err := w.Write(data); err != nil {
				return err
			}
			offset += int64(len(data))
		}
		if result.EOF {
			return nil
		}
	}
}

func (s *Service) Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error {
	if err := s.checkDevice(deviceID); err != nil {
		return err
	}
	if err := s.acquire(deviceID); err != nil {
		return err
	}
	defer s.release(deviceID)
	uploadID := uuid.NewString()
	openEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteOpen, protocol.FsWriteOpen{UploadID: uploadID, Path: path, Size: size, Overwrite: overwrite})
	if err != nil {
		return err
	}
	if _, err := decodeResult[protocol.FsWriteOpened](openEnv, protocol.TypeFsWriteOpened); err != nil {
		return err
	}
	buf := make([]byte, s.chunkSize)
	offset := int64(0)
	for {
		n, readErr := io.ReadFull(r, buf)
		if n > 0 {
			chunkEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteChunk, protocol.FsWriteChunk{
				UploadID: uploadID,
				Offset:   offset,
				Data:     base64.StdEncoding.EncodeToString(buf[:n]),
			})
			if err != nil {
				return err
			}
			if _, err := decodeResult[protocol.FsWriteAck](chunkEnv, protocol.TypeFsWriteAck); err != nil {
				return err
			}
			offset += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	closeEnv, err := s.roundTrip(ctx, deviceID, protocol.TypeFsWriteClose, protocol.FsWriteClose{UploadID: uploadID, TotalSize: offset})
	if err != nil {
		return err
	}
	_, err = decodeResult[protocol.FsWriteResult](closeEnv, protocol.TypeFsWriteResult)
	return err
}

func (s *Service) checkDevice(deviceID string) error {
	if !s.caps.HasCapability(deviceID, protocol.CapabilityFs) {
		return &OpError{Code: CodeAgentUnsupported, Message: "agent does not support file operations"}
	}
	return nil
}

func (s *Service) acquire(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[deviceID] >= s.maxPerDevice {
		return &OpError{Code: CodeBusy, Message: "too many concurrent file operations"}
	}
	s.active[deviceID]++
	return nil
}

func (s *Service) release(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[deviceID]--
	if s.active[deviceID] <= 0 {
		delete(s.active, deviceID)
	}
}

func (s *Service) roundTrip(ctx context.Context, deviceID string, messageType string, payload any) (protocol.Envelope, error) {
	requestID := uuid.NewString()
	ch := make(chan protocol.Envelope, 1)
	s.mu.Lock()
	s.pending[requestID] = ch
	s.mu.Unlock()
	cleanup := func() {
		s.mu.Lock()
		delete(s.pending, requestID)
		s.mu.Unlock()
	}
	if err := s.sender.ToDevice(deviceID, wshub.Outbound{Type: messageType, RequestID: requestID, Payload: payload}); err != nil {
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeAgentOffline, Message: "agent not connected"}
	}
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case env := <-ch:
		return env, nil
	case <-timer.C:
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeTimeout, Message: "agent did not respond in time"}
	case <-ctx.Done():
		cleanup()
		return protocol.Envelope{}, &OpError{Code: CodeTimeout, Message: "request cancelled"}
	}
}

func decodeResult[T any](env protocol.Envelope, wantType string) (T, error) {
	var result T
	if env.Type == protocol.TypeFsError {
		var fsErr protocol.FsError
		if err := json.Unmarshal(env.Payload, &fsErr); err != nil {
			return result, &OpError{Code: CodeAgentError, Message: "invalid agent error payload"}
		}
		return result, &OpError{Code: fsErr.Code, Message: fsErr.Message}
	}
	if env.Type != wantType {
		return result, &OpError{Code: CodeAgentError, Message: fmt.Sprintf("unexpected reply type %q", env.Type)}
	}
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return result, &OpError{Code: CodeAgentError, Message: "invalid agent payload"}
	}
	return result, nil
}
```

- [ ] **Step 7: 运行确认通过**

Run: `cd server && go test ./internal/files/ ./internal/devices/`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add server/internal/devices/ server/internal/files/
git commit -m "feat(server): file transfer bridge service with capability tracking"
```

---

### Task 4: httpapi——fs REST 端点、agent 消息路由与配置

**Files:**
- Modify: `server/internal/httpapi/router.go`
- Modify: `server/internal/config/config.go`
- Modify: `server/cmd/server/main.go`
- Modify: `config.example.yaml`
- Test: `server/internal/httpapi/router_fs_test.go`（新建）、`server/internal/config/config_test.go`

**Interfaces:**
- Consumes: Task 3 的 `files.Service` 全部方法与错误码；Task 1 的 protocol 类型。
- Produces:
  - REST：`GET /api/devices/{id}/fs?path=`（200 → `protocol.FsListResult` JSON）；`GET /api/devices/{id}/fs/file?path=`（200 二进制流 + `Content-Disposition` + `Content-Length`）；`POST /api/devices/{id}/fs/file?path=&overwrite=true|false`（201 → `{"path":..., "size":...}`）。
  - 错误映射（`writeFsError`）：`agent_offline`/`agent_unsupported`→503、`not_found`→404、`permission_denied`→403、`not_a_file`/`not_a_directory`/`invalid_path`/`invalid_request`→400、`already_exists`→409、`timeout`→504、`busy`→429、其余→500。
  - `httpapi.FsService` 接口（供测试替身）：`List`/`Download`/`Upload`/`HandleAgentResponse`，签名同 Task 3。
  - `Deps` 增加 `Files FsService` 与 `FsMaxUploadSize int64`（零值时默认 512 MiB、用 `files.NewService(hub, presence)`）。
  - `config.Config` 增加 `FsMaxUploadSize int64`；YAML 键 `fs_max_upload_size`；env `VIBE_FS_MAX_UPLOAD_SIZE`；默认 `512 << 20`。
  - 审计事件：`file_download`、`file_upload`（metadata JSON 含 `path`、`bytes`）。

- [ ] **Step 1: 写 config 失败测试**

在 `server/internal/config/config_test.go` 末尾追加（沿用文件内现有测试风格，若有 helper 就复用）：

```go
func TestFsMaxUploadSizeDefaultsAndOverrides(t *testing.T) {
	t.Setenv("VIBE_CONFIG", "")
	t.Setenv("VIBE_FS_MAX_UPLOAD_SIZE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FsMaxUploadSize != 512<<20 {
		t.Fatalf("default = %d", cfg.FsMaxUploadSize)
	}
	t.Setenv("VIBE_FS_MAX_UPLOAD_SIZE", "1048576")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if cfg.FsMaxUploadSize != 1048576 {
		t.Fatalf("env override = %d", cfg.FsMaxUploadSize)
	}
}
```

Run: `cd server && go test ./internal/config/` → FAIL（字段不存在）。

- [ ] **Step 2: 实现 config**

`server/internal/config/config.go`：

- `Config` 结构体加字段 `FsMaxUploadSize int64`。
- `fileConfig` 加 `FsMaxUploadSize int64 \`yaml:"fs_max_upload_size"\``。
- `defaultConfig()` 返回值加 `FsMaxUploadSize: 512 << 20,`。
- `applyFile` 加：

```go
	if file.FsMaxUploadSize > 0 {
		cfg.FsMaxUploadSize = file.FsMaxUploadSize
	}
```

- `applyEnv` 加（import 增加 `strconv`）：

```go
	if value := os.Getenv("VIBE_FS_MAX_UPLOAD_SIZE"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			cfg.FsMaxUploadSize = parsed
		}
	}
```

Run: `cd server && go test ./internal/config/` → PASS

- [ ] **Step 3: 写 httpapi 失败测试**

新建 `server/internal/httpapi/router_fs_test.go`：

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/files"
	"github.com/djy/vibe-terminal/server/internal/protocol"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

type stubFiles struct {
	listResult   protocol.FsListResult
	err          error
	downloadData []byte
	downloadSize int64
	uploaded     bytes.Buffer
	uploadPath   string
	uploadSize   int64
	overwrite    bool
	handled      []protocol.Envelope
}

func (s *stubFiles) List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error) {
	return s.listResult, s.err
}

func (s *stubFiles) Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error {
	if s.err != nil {
		return s.err
	}
	onSize(s.downloadSize)
	_, err := w.Write(s.downloadData)
	return err
}

func (s *stubFiles) Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error {
	if s.err != nil {
		return s.err
	}
	s.uploadPath = path
	s.uploadSize = size
	s.overwrite = overwrite
	_, err := io.Copy(&s.uploaded, r)
	return err
}

func (s *stubFiles) HandleAgentResponse(env protocol.Envelope) bool {
	s.handled = append(s.handled, env)
	return true
}

func newFsTestRouter(t *testing.T, stub *stubFiles) (http.Handler, []*http.Cookie) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := db.CreateDevice(ctx, store.Device{ID: "dev-1", Name: "laptop", Platform: "linux", AgentVersion: "0.1.0", Fingerprint: "fp", CredentialHash: "h", Authorized: true}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	router := NewRouter(Deps{
		Store:           db,
		Sessions:        auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
		Files:           stub,
		FsMaxUploadSize: 1024,
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginRR.Code)
	}
	return router, loginRR.Result().Cookies()
}

func TestFsListReturnsEntries(t *testing.T) {
	stub := &stubFiles{listResult: protocol.FsListResult{Path: "/home/dev", Entries: []protocol.FsEntry{{Name: "a.txt", Size: 3}}}}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=~", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var result protocol.FsListResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Path != "/home/dev" || len(result.Entries) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestFsListRequiresAuth(t *testing.T) {
	router, _ := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=/tmp", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsListMissingPath(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsErrorMapping(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{files.CodeAgentOffline, http.StatusServiceUnavailable},
		{files.CodeAgentUnsupported, http.StatusServiceUnavailable},
		{"not_found", http.StatusNotFound},
		{"permission_denied", http.StatusForbidden},
		{"not_a_directory", http.StatusBadRequest},
		{"already_exists", http.StatusConflict},
		{files.CodeTimeout, http.StatusGatewayTimeout},
		{files.CodeBusy, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		stub := &stubFiles{err: &files.OpError{Code: tc.code, Message: tc.code}}
		router, cookies := newFsTestRouter(t, stub)
		req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs?path=/tmp", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("code %s: status = %d, want %d", tc.code, rr.Code, tc.want)
		}
	}
}

func TestFsDownloadSetsHeadersAndBody(t *testing.T) {
	stub := &stubFiles{downloadData: []byte("hello"), downloadSize: 5}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodGet, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fhello.txt", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello" {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("Content-Length"); got != "5" {
		t.Fatalf("content-length = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename=hello.txt` {
		t.Fatalf("content-disposition = %q", got)
	}
}

func TestFsUploadStreamsBody(t *testing.T) {
	stub := &stubFiles{}
	router, cookies := newFsTestRouter(t, stub)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fup.bin&overwrite=true", bytes.NewBufferString("payload"))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.uploaded.String() != "payload" || stub.uploadPath != "/tmp/up.bin" || !stub.overwrite || stub.uploadSize != 7 {
		t.Fatalf("stub = %#v", stub)
	}
}

func TestFsUploadRejectsOversize(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	big := bytes.NewBuffer(make([]byte, 2048))
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fbig.bin", big)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsUploadRequiresContentLength(t *testing.T) {
	router, cookies := newFsTestRouter(t, &stubFiles{})
	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-1/fs/file?path=%2Ftmp%2Fx", bytes.NewBufferString("x"))
	req.ContentLength = -1
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusLengthRequired {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestFsAgentEnvelopeRoutesToFilesService(t *testing.T) {
	stub := &stubFiles{}
	db := testutil.NewStore(t)
	r := &router{store: db, files: stub}
	env := protocol.Envelope{Type: protocol.TypeFsListResult, RequestID: "req-9", Payload: []byte(`{}`)}
	r.handleAgentEnvelope(context.Background(), "dev-1", env, nil)
	if len(stub.handled) != 1 || stub.handled[0].RequestID != "req-9" {
		t.Fatalf("handled = %#v", stub.handled)
	}
}
```

Run: `cd server && go test ./internal/httpapi/` → FAIL（`Deps` 无 `Files` 字段等）。

- [ ] **Step 4: 实现 httpapi**

`server/internal/httpapi/router.go`：

4a. import 增加 `"io"`、`"mime"`、`"strconv"`、`"github.com/djy/vibe-terminal/server/internal/files"`。

4b. 在 `Deps` 定义前加接口，并给 `Deps`、`router` 增加字段：

```go
type FsService interface {
	List(ctx context.Context, deviceID string, path string) (protocol.FsListResult, error)
	Download(ctx context.Context, deviceID string, path string, onSize func(int64), w io.Writer) error
	Upload(ctx context.Context, deviceID string, path string, size int64, overwrite bool, r io.Reader) error
	HandleAgentResponse(env protocol.Envelope) bool
}
```

`Deps` 加 `Files FsService` 和 `FsMaxUploadSize int64`；`router` 结构体加 `files FsService` 和 `fsMaxUpload int64`。

4c. `NewRouter` 中，在 Hub 默认化之后加：

```go
	if deps.Files == nil {
		deps.Files = files.NewService(deps.Hub, deps.Presence)
	}
	if deps.FsMaxUploadSize <= 0 {
		deps.FsMaxUploadSize = 512 << 20
	}
```

并把 `files: deps.Files, fsMaxUpload: deps.FsMaxUploadSize` 写入 `r := &router{...}`。

4d. `handleDeviceRoutes` 中，把 `if suffix != "sessions" { ... 404 ... }` 与其后的 method switch 整体替换为：

```go
	switch {
	case suffix == "sessions" && req.Method == http.MethodPost:
		r.handleCreateSession(w, req, deviceID)
	case suffix == "sessions" && req.Method == http.MethodGet:
		r.handleListSessions(w, req, deviceID)
	case suffix == "sessions":
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	case suffix == "fs" && req.Method == http.MethodGet:
		r.handleFsList(w, req, deviceID)
	case suffix == "fs/file" && req.Method == http.MethodGet:
		r.handleFsDownload(w, req, deviceID)
	case suffix == "fs/file" && req.Method == http.MethodPost:
		r.handleFsUpload(w, req, deviceID)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
```

4e. 新增三个 handler 与错误映射（放在 `handleListSessions` 之后）：

```go
func (r *router) handleFsList(w http.ResponseWriter, req *http.Request, deviceID string) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	result, err := r.files.List(req.Context(), deviceID, path)
	if err != nil {
		writeFsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *router) handleFsDownload(w http.ResponseWriter, req *http.Request, deviceID string) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	filename := path[strings.LastIndex(path, "/")+1:]
	var fileSize int64
	wroteHeader := false
	err := r.files.Download(req.Context(), deviceID, path, func(size int64) {
		fileSize = size
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		wroteHeader = true
	}, w)
	if err != nil {
		if !wroteHeader {
			writeFsError(w, err)
		}
		// 流已开始时中断：连接直接断开，客户端按 Content-Length 检测到截断。
		return
	}
	metadata, _ := json.Marshal(map[string]any{"path": path, "bytes": fileSize})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:       user.ID,
		DeviceID:     deviceID,
		EventType:    "file_download",
		Summary:      "file downloaded from device",
		MetadataJSON: string(metadata),
	})
}

func (r *router) handleFsUpload(w http.ResponseWriter, req *http.Request, deviceID string) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_path", "path query parameter is required")
		return
	}
	overwrite := req.URL.Query().Get("overwrite") == "true"
	if _, err := r.store.GetDevice(req.Context(), deviceID); err != nil {
		writeStoreError(w, err, "device")
		return
	}
	if req.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if req.ContentLength > r.fsMaxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds upload size limit")
		return
	}
	body := http.MaxBytesReader(w, req.Body, r.fsMaxUpload)
	defer body.Close()
	if err := r.files.Upload(req.Context(), deviceID, path, req.ContentLength, overwrite, body); err != nil {
		writeFsError(w, err)
		return
	}
	metadata, _ := json.Marshal(map[string]any{"path": path, "bytes": req.ContentLength})
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:       user.ID,
		DeviceID:     deviceID,
		EventType:    "file_upload",
		Summary:      "file uploaded to device",
		MetadataJSON: string(metadata),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"path": path, "size": req.ContentLength})
}

func writeFsError(w http.ResponseWriter, err error) {
	var opErr *files.OpError
	if !errors.As(err, &opErr) {
		writeError(w, http.StatusInternalServerError, "fs_error", "file operation failed")
		return
	}
	status := http.StatusInternalServerError
	switch opErr.Code {
	case files.CodeAgentOffline, files.CodeAgentUnsupported:
		status = http.StatusServiceUnavailable
	case "not_found":
		status = http.StatusNotFound
	case "permission_denied":
		status = http.StatusForbidden
	case "not_a_file", "not_a_directory", "invalid_path", "invalid_request":
		status = http.StatusBadRequest
	case "already_exists":
		status = http.StatusConflict
	case files.CodeTimeout:
		status = http.StatusGatewayTimeout
	case files.CodeBusy:
		status = http.StatusTooManyRequests
	}
	writeError(w, status, opErr.Code, opErr.Message)
}
```

4f. `handleAgentWebSocket` 中，`r.presence.Set(device.ID, true)` 之后加一行：

```go
	r.presence.SetCapabilities(device.ID, hello.Capabilities)
```

4g. `handleAgentEnvelope` 的 switch 中，在 `case protocol.TypeError:` 之前加：

```go
	case protocol.TypeFsListResult, protocol.TypeFsReadResult, protocol.TypeFsWriteOpened,
		protocol.TypeFsWriteAck, protocol.TypeFsWriteResult, protocol.TypeFsError:
		// 迟到的响应（请求已超时删除）由 HandleAgentResponse 返回 false，直接丢弃。
		_ = r.files.HandleAgentResponse(env)
```

4h. `server/cmd/server/main.go` 的 `httpapi.Deps{...}` 中加 `FsMaxUploadSize: cfg.FsMaxUploadSize,`。

4i. `config.example.yaml` 末尾追加：

```yaml
# 上传单文件大小上限（字节），默认 512 MiB
fs_max_upload_size: 536870912
```

- [ ] **Step 5: 运行确认通过**

Run: `cd server && go test ./...`
Expected: PASS（全部包）

- [ ] **Step 6: 提交**

```bash
git add server/internal/httpapi/ server/internal/config/ server/cmd/server/main.go config.example.yaml
git commit -m "feat(server): fs REST endpoints bridged to agent channel"
```

---

### Task 5: Rust 协议层——Envelope.request_id 与 fs 结构体

**Files:**
- Modify: `agent/src/protocol.rs`
- Modify: `agent/src/client.rs`（仅 `send_payload` 签名与既有调用点）

**Interfaces:**
- Consumes: 无（与 Task 1 的 JSON 字段名保持一致：`upload_id`、`file_size`、`modified_at` 等 snake_case）。
- Produces:
  - `Envelope<T>` 增加 `pub request_id: Option<String>`（`#[serde(default, skip_serializing_if = "Option::is_none")]`）。
  - 结构体（全部 `#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]`）：`FsList{path: String}`、`FsEntry{name: String, is_dir: bool, size: u64, mode: u32, modified_at: i64}`、`FsListResult{path: String, entries: Vec<FsEntry>}`、`FsRead{path: String, offset: u64, length: u32}`、`FsReadResult{data: String, eof: bool, file_size: u64}`、`FsWriteOpen{upload_id: String, path: String, size: u64, overwrite: bool}`、`FsWriteOpened{upload_id: String}`、`FsWriteChunk{upload_id: String, offset: u64, data: String}`、`FsWriteAck{upload_id: String, offset: u64}`、`FsWriteClose{upload_id: String, total_size: u64}`、`FsWriteResult{upload_id: String}`。
  - `send_payload` 签名变为 `send_payload(write, message_type, request_id: Option<&str>, session_id: Option<&str>, payload)`，envelope JSON 顶层带 `request_id`。

- [ ] **Step 1: 写失败测试**

`agent/src/protocol.rs` 的 `mod tests` 中追加：

```rust
    #[test]
    fn envelope_decodes_request_id() {
        let json = r#"{"type":"fs_list","request_id":"req-1","payload":{"path":"/tmp"}}"#;
        let envelope: Envelope<FsList> = serde_json::from_str(json).expect("decode");
        assert_eq!(envelope.request_id.as_deref(), Some("req-1"));
        assert_eq!(envelope.payload.path, "/tmp");
    }

    #[test]
    fn envelope_without_request_id_still_decodes() {
        let json = r#"{"type":"stdin","session_id":"s1","payload":{"session_id":"s1","data":"ls"}}"#;
        let envelope: Envelope<Stdin> = serde_json::from_str(json).expect("decode");
        assert!(envelope.request_id.is_none());
    }

    #[test]
    fn fs_read_result_round_trip() {
        let value = FsReadResult { data: "aGk=".into(), eof: true, file_size: 2 };
        let json = serde_json::to_string(&value).expect("serialize");
        assert!(json.contains("\"file_size\":2"));
        let back: FsReadResult = serde_json::from_str(&json).expect("deserialize");
        assert_eq!(back, value);
    }
```

- [ ] **Step 2: 运行确认失败**

Run: `cd agent && cargo test`
Expected: FAIL，`no field request_id`、`cannot find type FsList`。

- [ ] **Step 3: 实现**

3a. `Envelope<T>` 改为：

```rust
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Envelope<T> {
    #[serde(rename = "type")]
    pub message_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub request_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub session_id: Option<String>,
    pub payload: T,
}
```

3b. `protocol.rs` 中现有测试 `start_session_round_trip` 构造 `Envelope { ... }` 字面量处，补 `request_id: None,` 字段（编译要求）。

3c. 文件末尾（tests 模块之前）追加 fs 结构体（字段见上方 Produces，全部 pub，serde 默认 snake_case 已与 JSON 对齐，无需 rename）：

```rust
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsList {
    pub path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsEntry {
    pub name: String,
    pub is_dir: bool,
    pub size: u64,
    pub mode: u32,
    pub modified_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsListResult {
    pub path: String,
    pub entries: Vec<FsEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsRead {
    pub path: String,
    pub offset: u64,
    pub length: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsReadResult {
    pub data: String,
    pub eof: bool,
    pub file_size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteOpen {
    pub upload_id: String,
    pub path: String,
    pub size: u64,
    pub overwrite: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteOpened {
    pub upload_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteChunk {
    pub upload_id: String,
    pub offset: u64,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteAck {
    pub upload_id: String,
    pub offset: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteClose {
    pub upload_id: String,
    pub total_size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteResult {
    pub upload_id: String,
}
```

3d. `agent/src/client.rs` 中 `send_payload` 改签名并更新 envelope 构造：

```rust
async fn send_payload<S, T>(
    write: &mut S,
    message_type: &str,
    request_id: Option<&str>,
    session_id: Option<&str>,
    payload: T,
) -> Result<()>
where
    S: Sink<Message> + Unpin,
    S::Error: std::error::Error + Send + Sync + 'static,
    T: Serialize,
{
    let envelope = serde_json::json!({
        "type": message_type,
        "request_id": request_id,
        "session_id": session_id,
        "payload": payload,
    });
    write
        .send(Message::Text(envelope.to_string()))
        .await
        .context("send websocket payload")
}
```

3e. `client.rs` 内所有既有 `send_payload(...)` 调用点（`session_started`、`session_exit`×2、`stdout`）在 `message_type` 之后插入 `None,` 作为 request_id 实参。

- [ ] **Step 4: 运行确认通过**

Run: `cd agent && cargo test`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/src/protocol.rs agent/src/client.rs
git commit -m "feat(agent): request-scoped envelopes and fs protocol types"
```

---

### Task 6: Rust fs 模块——目录列表、分块读与上传管理

**Files:**
- Create: `agent/src/fs.rs`（含 `#[cfg(test)] mod tests`）
- Modify: `agent/src/lib.rs`（加 `pub mod fs;`）

**Interfaces:**
- Consumes: Task 5 的 protocol fs 结构体；crate 依赖 `base64`、`dirs`（已在 Cargo.toml）。
- Produces（Task 7 依赖）:
  - `pub enum FsOpError`（变体 `NotFound(String)`、`PermissionDenied(String)`、`NotAFile(String)`、`NotADirectory(String)`、`AlreadyExists(String)`、`InvalidPath(String)`、`InvalidRequest(String)`、`Io(String)`），方法 `pub fn code(&self) -> &'static str`（对应 `not_found`/`permission_denied`/`not_a_file`/`not_a_directory`/`already_exists`/`invalid_path`/`invalid_request`/`io_error`）、`pub fn message(&self) -> &str`。
  - `pub fn list_dir(raw_path: &str) -> Result<FsListResult, FsOpError>`——`~` 展开为 home，目录优先按名排序，返回解析后的绝对路径。
  - `pub fn read_chunk(raw_path: &str, offset: u64, length: u32) -> Result<FsReadResult, FsOpError>`——无状态 pread，`eof = 实读 < 请求长度`。
  - `pub struct UploadManager`：`new()`、`open(&mut self, upload_id: &str, raw_path: &str, overwrite: bool) -> Result<(), FsOpError>`、`write_chunk(&mut self, upload_id: &str, offset: u64, data: &[u8]) -> Result<u64, FsOpError>`（返回新写入总量；出错即中止并删临时文件）、`close(&mut self, upload_id: &str, total_size: u64) -> Result<(), FsOpError>`（校验字节数、fsync、原子 rename）、`abort(&mut self, upload_id: &str)`、`cleanup_stale(&mut self, max_age: Duration) -> usize`。
  - `pub fn handle_fs_message(uploads: &mut UploadManager, message_type: &str, payload: serde_json::Value) -> Option<(String, serde_json::Value)>`——非 fs 消息返回 `None`；fs 消息返回 `(响应类型, 响应 payload)`，失败时为 `("fs_error", {"code","message"})`。

- [ ] **Step 1: 写失败测试**

新建 `agent/src/fs.rs`，先只写测试骨架与 `use`（实现留空导致编译失败即可视为红灯；Rust 下更实际的做法是同文件先写完整测试 + `todo!()` 实现桩）。测试代码（置于文件底部）：

```rust
#[cfg(test)]
mod tests {
    use super::*;
    use std::fs as stdfs;
    use std::time::Duration;

    #[test]
    fn list_dir_sorts_directories_first() {
        let dir = tempfile::tempdir().expect("tempdir");
        stdfs::write(dir.path().join("b.txt"), b"hello").expect("write");
        stdfs::create_dir(dir.path().join("a_dir")).expect("mkdir");
        let result = list_dir(dir.path().to_str().unwrap()).expect("list");
        assert_eq!(result.entries[0].name, "a_dir");
        assert!(result.entries[0].is_dir);
        assert_eq!(result.entries[1].name, "b.txt");
        assert_eq!(result.entries[1].size, 5);
    }

    #[test]
    fn list_dir_rejects_relative_path() {
        let err = list_dir("relative/path").unwrap_err();
        assert_eq!(err.code(), "invalid_path");
    }

    #[test]
    fn list_dir_missing_path_is_not_found() {
        let err = list_dir("/definitely/missing/dir-xyz").unwrap_err();
        assert_eq!(err.code(), "not_found");
    }

    #[test]
    fn read_chunk_respects_offset_and_eof() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("data.bin");
        stdfs::write(&path, b"hello world!").expect("write");
        let first = read_chunk(path.to_str().unwrap(), 0, 5).expect("read");
        assert_eq!(first.data, base64_encode(b"hello"));
        assert!(!first.eof);
        assert_eq!(first.file_size, 12);
        let last = read_chunk(path.to_str().unwrap(), 10, 5).expect("read tail");
        assert_eq!(last.data, base64_encode(b"d!"));
        assert!(last.eof);
    }

    #[test]
    fn read_chunk_on_directory_is_not_a_file() {
        let dir = tempfile::tempdir().expect("tempdir");
        let err = read_chunk(dir.path().to_str().unwrap(), 0, 4).unwrap_err();
        assert_eq!(err.code(), "not_a_file");
    }

    #[test]
    fn upload_full_cycle_renames_into_place() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("upload.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-1", target.to_str().unwrap(), false)
            .expect("open");
        uploads.write_chunk("up-1", 0, b"hello ").expect("chunk 1");
        uploads.write_chunk("up-1", 6, b"world!").expect("chunk 2");
        uploads.close("up-1", 12).expect("close");
        assert_eq!(stdfs::read(&target).expect("read"), b"hello world!");
        assert!(!dir.path().join(".vibe-upload-up-1.tmp").exists());
    }

    #[test]
    fn upload_without_overwrite_rejects_existing_target() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("exists.txt");
        stdfs::write(&target, b"old").expect("write");
        let mut uploads = UploadManager::new();
        let err = uploads
            .open("up-2", target.to_str().unwrap(), false)
            .unwrap_err();
        assert_eq!(err.code(), "already_exists");
    }

    #[test]
    fn upload_wrong_offset_aborts_and_cleans_tmp() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("bad.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-3", target.to_str().unwrap(), false)
            .expect("open");
        let err = uploads.write_chunk("up-3", 5, b"data").unwrap_err();
        assert_eq!(err.code(), "invalid_request");
        assert!(!dir.path().join(".vibe-upload-up-3.tmp").exists());
        let err = uploads.write_chunk("up-3", 0, b"data").unwrap_err();
        assert_eq!(err.code(), "not_found");
    }

    #[test]
    fn close_with_size_mismatch_fails_and_cleans() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("short.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-4", target.to_str().unwrap(), false)
            .expect("open");
        uploads.write_chunk("up-4", 0, b"abc").expect("chunk");
        let err = uploads.close("up-4", 99).unwrap_err();
        assert_eq!(err.code(), "invalid_request");
        assert!(!target.exists());
    }

    #[test]
    fn cleanup_stale_removes_idle_uploads() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("stale.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-5", target.to_str().unwrap(), false)
            .expect("open");
        assert_eq!(uploads.cleanup_stale(Duration::from_secs(3600)), 0);
        assert_eq!(uploads.cleanup_stale(Duration::ZERO), 1);
        assert!(!dir.path().join(".vibe-upload-up-5.tmp").exists());
    }

    #[test]
    fn handle_fs_message_dispatches_list() {
        let dir = tempfile::tempdir().expect("tempdir");
        stdfs::write(dir.path().join("x.txt"), b"x").expect("write");
        let mut uploads = UploadManager::new();
        let payload = serde_json::json!({"path": dir.path().to_str().unwrap()});
        let (reply_type, reply) =
            handle_fs_message(&mut uploads, "fs_list", payload).expect("handled");
        assert_eq!(reply_type, "fs_list_result");
        assert_eq!(reply["entries"][0]["name"], "x.txt");
    }

    #[test]
    fn handle_fs_message_returns_error_payload() {
        let mut uploads = UploadManager::new();
        let payload = serde_json::json!({"path": "/missing-dir-xyz"});
        let (reply_type, reply) =
            handle_fs_message(&mut uploads, "fs_list", payload).expect("handled");
        assert_eq!(reply_type, "fs_error");
        assert_eq!(reply["code"], "not_found");
    }

    #[test]
    fn handle_fs_message_ignores_unknown_types() {
        let mut uploads = UploadManager::new();
        assert!(handle_fs_message(&mut uploads, "stdin", serde_json::json!({})).is_none());
    }

    fn base64_encode(data: &[u8]) -> String {
        use base64::Engine;
        base64::engine::general_purpose::STANDARD.encode(data)
    }
}
```

同时在 `agent/src/lib.rs` 中按字母序加入 `pub mod fs;`。

- [ ] **Step 2: 运行确认失败**

Run: `cd agent && cargo test`
Expected: FAIL（`list_dir` 等未定义/`todo!()` panic）。

- [ ] **Step 3: 实现 fs.rs（测试模块之上）**

```rust
use base64::engine::general_purpose::STANDARD as BASE64;
use base64::Engine;
use serde_json::Value;
use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::protocol::{
    FsEntry, FsList, FsListResult, FsRead, FsReadResult, FsWriteAck, FsWriteChunk, FsWriteClose,
    FsWriteOpen, FsWriteOpened, FsWriteResult,
};

// 拒绝异常超大块，防止内存滥用。
pub const MAX_CHUNK_LEN: u32 = 1024 * 1024;

#[derive(Debug)]
pub enum FsOpError {
    NotFound(String),
    PermissionDenied(String),
    NotAFile(String),
    NotADirectory(String),
    AlreadyExists(String),
    InvalidPath(String),
    InvalidRequest(String),
    Io(String),
}

impl FsOpError {
    pub fn code(&self) -> &'static str {
        match self {
            FsOpError::NotFound(_) => "not_found",
            FsOpError::PermissionDenied(_) => "permission_denied",
            FsOpError::NotAFile(_) => "not_a_file",
            FsOpError::NotADirectory(_) => "not_a_directory",
            FsOpError::AlreadyExists(_) => "already_exists",
            FsOpError::InvalidPath(_) => "invalid_path",
            FsOpError::InvalidRequest(_) => "invalid_request",
            FsOpError::Io(_) => "io_error",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            FsOpError::NotFound(m)
            | FsOpError::PermissionDenied(m)
            | FsOpError::NotAFile(m)
            | FsOpError::NotADirectory(m)
            | FsOpError::AlreadyExists(m)
            | FsOpError::InvalidPath(m)
            | FsOpError::InvalidRequest(m)
            | FsOpError::Io(m) => m,
        }
    }
}

fn io_error(path: &Path, err: std::io::Error) -> FsOpError {
    match err.kind() {
        std::io::ErrorKind::NotFound => {
            FsOpError::NotFound(format!("{} not found", path.display()))
        }
        std::io::ErrorKind::PermissionDenied => {
            FsOpError::PermissionDenied(format!("permission denied for {}", path.display()))
        }
        _ => FsOpError::Io(format!("{}: {}", path.display(), err)),
    }
}

fn resolve_path(raw: &str) -> Result<PathBuf, FsOpError> {
    if raw == "~" {
        return dirs::home_dir()
            .ok_or_else(|| FsOpError::InvalidPath("home directory unavailable".into()));
    }
    if let Some(rest) = raw.strip_prefix("~/") {
        let home = dirs::home_dir()
            .ok_or_else(|| FsOpError::InvalidPath("home directory unavailable".into()))?;
        return Ok(home.join(rest));
    }
    let path = PathBuf::from(raw);
    if !path.is_absolute() {
        return Err(FsOpError::InvalidPath(format!(
            "path must be absolute: {raw}"
        )));
    }
    Ok(path)
}

pub fn list_dir(raw_path: &str) -> Result<FsListResult, FsOpError> {
    let path = resolve_path(raw_path)?;
    let meta = fs::metadata(&path).map_err(|e| io_error(&path, e))?;
    if !meta.is_dir() {
        return Err(FsOpError::NotADirectory(format!(
            "{} is not a directory",
            path.display()
        )));
    }
    let mut entries = Vec::new();
    for entry in fs::read_dir(&path).map_err(|e| io_error(&path, e))? {
        let entry = entry.map_err(|e| io_error(&path, e))?;
        let name = entry.file_name().to_string_lossy().to_string();
        // 损坏的符号链接读不到 metadata 时跳过，不让整个列表失败。
        let Ok(meta) = entry.metadata() else {
            continue;
        };
        entries.push(FsEntry {
            name,
            is_dir: meta.is_dir(),
            size: meta.len(),
            mode: file_mode(&meta),
            modified_at: modified_unix(&meta),
        });
    }
    entries.sort_by(|a, b| b.is_dir.cmp(&a.is_dir).then_with(|| a.name.cmp(&b.name)));
    Ok(FsListResult {
        path: path.to_string_lossy().to_string(),
        entries,
    })
}

#[cfg(unix)]
fn file_mode(meta: &fs::Metadata) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode()
}

#[cfg(not(unix))]
fn file_mode(_meta: &fs::Metadata) -> u32 {
    0
}

fn modified_unix(meta: &fs::Metadata) -> i64 {
    meta.modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

pub fn read_chunk(raw_path: &str, offset: u64, length: u32) -> Result<FsReadResult, FsOpError> {
    if length == 0 || length > MAX_CHUNK_LEN {
        return Err(FsOpError::InvalidRequest(format!(
            "invalid chunk length {length}"
        )));
    }
    let path = resolve_path(raw_path)?;
    let meta = fs::metadata(&path).map_err(|e| io_error(&path, e))?;
    if meta.is_dir() {
        return Err(FsOpError::NotAFile(format!(
            "{} is a directory",
            path.display()
        )));
    }
    let mut file = File::open(&path).map_err(|e| io_error(&path, e))?;
    file.seek(SeekFrom::Start(offset))
        .map_err(|e| io_error(&path, e))?;
    let mut buf = vec![0u8; length as usize];
    let mut total = 0usize;
    loop {
        let n = file
            .read(&mut buf[total..])
            .map_err(|e| io_error(&path, e))?;
        if n == 0 {
            break;
        }
        total += n;
        if total == buf.len() {
            break;
        }
    }
    buf.truncate(total);
    Ok(FsReadResult {
        data: BASE64.encode(&buf),
        eof: total < length as usize,
        file_size: meta.len(),
    })
}

struct Upload {
    file: File,
    tmp_path: PathBuf,
    target: PathBuf,
    written: u64,
    last_activity: Instant,
}

pub struct UploadManager {
    uploads: HashMap<String, Upload>,
}

impl Default for UploadManager {
    fn default() -> Self {
        Self::new()
    }
}

impl UploadManager {
    pub fn new() -> Self {
        Self {
            uploads: HashMap::new(),
        }
    }

    pub fn open(
        &mut self,
        upload_id: &str,
        raw_path: &str,
        overwrite: bool,
    ) -> Result<(), FsOpError> {
        let target = resolve_path(raw_path)?;
        let Some(parent) = target.parent().map(Path::to_path_buf) else {
            return Err(FsOpError::InvalidPath(
                "upload target has no parent directory".into(),
            ));
        };
        let parent_meta = fs::metadata(&parent).map_err(|e| io_error(&parent, e))?;
        if !parent_meta.is_dir() {
            return Err(FsOpError::NotADirectory(format!(
                "{} is not a directory",
                parent.display()
            )));
        }
        if target.is_dir() {
            return Err(FsOpError::NotAFile(format!(
                "{} is a directory",
                target.display()
            )));
        }
        if !overwrite && target.exists() {
            return Err(FsOpError::AlreadyExists(format!(
                "{} already exists",
                target.display()
            )));
        }
        let tmp_path = parent.join(format!(".vibe-upload-{upload_id}.tmp"));
        let file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&tmp_path)
            .map_err(|e| io_error(&tmp_path, e))?;
        self.uploads.insert(
            upload_id.to_string(),
            Upload {
                file,
                tmp_path,
                target,
                written: 0,
                last_activity: Instant::now(),
            },
        );
        Ok(())
    }

    pub fn write_chunk(
        &mut self,
        upload_id: &str,
        offset: u64,
        data: &[u8],
    ) -> Result<u64, FsOpError> {
        let result = {
            let Some(upload) = self.uploads.get_mut(upload_id) else {
                return Err(FsOpError::NotFound(format!("upload {upload_id} not found")));
            };
            if offset != upload.written {
                Err(FsOpError::InvalidRequest(format!(
                    "unexpected offset {offset}, expected {}",
                    upload.written
                )))
            } else if let Err(err) = upload.file.write_all(data) {
                Err(FsOpError::Io(format!(
                    "{}: {}",
                    upload.tmp_path.display(),
                    err
                )))
            } else {
                upload.written += data.len() as u64;
                upload.last_activity = Instant::now();
                Ok(upload.written)
            }
        };
        if result.is_err() {
            self.abort(upload_id);
        }
        result
    }

    pub fn close(&mut self, upload_id: &str, total_size: u64) -> Result<(), FsOpError> {
        let Some(upload) = self.uploads.remove(upload_id) else {
            return Err(FsOpError::NotFound(format!("upload {upload_id} not found")));
        };
        if upload.written != total_size {
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(FsOpError::InvalidRequest(format!(
                "size mismatch: wrote {}, expected {total_size}",
                upload.written
            )));
        }
        if let Err(err) = upload.file.sync_all() {
            let code = io_error(&upload.tmp_path, err);
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(code);
        }
        drop(upload.file);
        if let Err(err) = fs::rename(&upload.tmp_path, &upload.target) {
            let code = io_error(&upload.target, err);
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(code);
        }
        Ok(())
    }

    pub fn abort(&mut self, upload_id: &str) {
        if let Some(upload) = self.uploads.remove(upload_id) {
            drop(upload.file);
            let _ = fs::remove_file(&upload.tmp_path);
        }
    }

    pub fn cleanup_stale(&mut self, max_age: Duration) -> usize {
        let stale: Vec<String> = self
            .uploads
            .iter()
            .filter(|(_, upload)| upload.last_activity.elapsed() >= max_age)
            .map(|(id, _)| id.clone())
            .collect();
        for id in &stale {
            self.abort(id);
        }
        stale.len()
    }
}

pub fn handle_fs_message(
    uploads: &mut UploadManager,
    message_type: &str,
    payload: Value,
) -> Option<(String, Value)> {
    match message_type {
        "fs_list" => Some(reply(
            decode::<FsList>(payload).and_then(|req| list_dir(&req.path)),
            "fs_list_result",
        )),
        "fs_read" => Some(reply(
            decode::<FsRead>(payload).and_then(|req| read_chunk(&req.path, req.offset, req.length)),
            "fs_read_result",
        )),
        "fs_write_open" => Some(reply(
            decode::<FsWriteOpen>(payload).and_then(|req| {
                uploads
                    .open(&req.upload_id, &req.path, req.overwrite)
                    .map(|_| FsWriteOpened {
                        upload_id: req.upload_id,
                    })
            }),
            "fs_write_opened",
        )),
        "fs_write_chunk" => Some(reply(
            decode::<FsWriteChunk>(payload).and_then(|req| {
                let data = BASE64
                    .decode(&req.data)
                    .map_err(|_| FsOpError::InvalidRequest("invalid chunk encoding".into()))?;
                uploads
                    .write_chunk(&req.upload_id, req.offset, &data)
                    .map(|offset| FsWriteAck {
                        upload_id: req.upload_id,
                        offset,
                    })
            }),
            "fs_write_ack",
        )),
        "fs_write_close" => Some(reply(
            decode::<FsWriteClose>(payload).and_then(|req| {
                uploads
                    .close(&req.upload_id, req.total_size)
                    .map(|_| FsWriteResult {
                        upload_id: req.upload_id,
                    })
            }),
            "fs_write_result",
        )),
        _ => None,
    }
}

fn decode<T: serde::de::DeserializeOwned>(payload: Value) -> Result<T, FsOpError> {
    serde_json::from_value(payload)
        .map_err(|e| FsOpError::InvalidRequest(format!("invalid payload: {e}")))
}

fn reply<T: serde::Serialize>(result: Result<T, FsOpError>, ok_type: &str) -> (String, Value) {
    match result {
        Ok(value) => (
            ok_type.to_string(),
            serde_json::to_value(value).unwrap_or(Value::Null),
        ),
        Err(err) => (
            "fs_error".to_string(),
            serde_json::json!({"code": err.code(), "message": err.message()}),
        ),
    }
}
```

注意：`upload_wrong_offset_aborts_and_cleans_tmp` 测试要求 offset 错误即中止上传（`write_chunk` 的 `result.is_err()` 分支）——服务端逐块确认模式下 offset 错位说明状态已不可信，直接作废最安全。

- [ ] **Step 4: 运行确认通过**

Run: `cd agent && cargo test`
Expected: PASS（含既有测试）

- [ ] **Step 5: 提交**

```bash
git add agent/src/fs.rs agent/src/lib.rs
git commit -m "feat(agent): filesystem list, chunked read and managed uploads"
```

---

### Task 7: Rust client——fs 消息分发、capabilities 声明与超时清理

**Files:**
- Modify: `agent/src/client.rs`

**Interfaces:**
- Consumes: Task 6 的 `fs::UploadManager`、`fs::handle_fs_message`；Task 5 的 `Envelope.request_id`、新 `send_payload` 签名。
- Produces: agent 行为——`agent_hello` 带 `"capabilities": ["fs"]`；收到 fs 请求回带同 `request_id` 的响应；上传 60 秒无活动自动清理。

- [ ] **Step 1: 写失败测试**

`client.rs` 的 `mod tests` 中追加：

```rust
    #[test]
    fn agent_hello_declares_fs_capability() {
        let state = ClientState {
            config: AgentConfig {
                server_url: "http://localhost:8080".into(),
                device_id: "dev-1".into(),
                credential: "cred".into(),
                device_name: "test".into(),
            },
            registry: SessionRegistry::default(),
        };
        let hello = state.agent_hello_json().expect("hello");
        let value: serde_json::Value = serde_json::from_str(&hello).expect("json");
        assert_eq!(value["payload"]["capabilities"][0], "fs");
    }
```

（若 `AgentConfig` 字段与上不符，以 `agent/src/config.rs` 实际字段为准调整构造。）

- [ ] **Step 2: 运行确认失败**

Run: `cd agent && cargo test agent_hello_declares_fs_capability`
Expected: FAIL（capabilities 为 null）。

- [ ] **Step 3: 实现**

3a. `agent_hello_json` 的 payload json! 中加一行：

```rust
            "capabilities": ["fs"],
```

3b. `run_control_loop` 中，`let mut buffers: BTreeMap<...>` 之后加：

```rust
    let mut uploads = crate::fs::UploadManager::new();
```

3c. 消息 match 的 `_ => {}` 分支替换为：

```rust
                    other => {
                        let request_id = envelope.request_id.clone();
                        if let Some((reply_type, reply_payload)) =
                            crate::fs::handle_fs_message(&mut uploads, other, envelope.payload)
                        {
                            send_payload(
                                &mut write,
                                &reply_type,
                                request_id.as_deref(),
                                None,
                                reply_payload,
                            )
                            .await?;
                        }
                    }
```

注意：现有各已知分支（`start_session` 等）里 `envelope.payload` 被 `serde_json::from_value` 消费，`other` 分支拿到的是未匹配消息的 payload 所有权，无借用冲突。

3d. `output_tick.tick()` 分支末尾（`drain_exited` 循环之后）加：

```rust
                uploads.cleanup_stale(Duration::from_secs(60));
```

- [ ] **Step 4: 运行确认通过**

Run: `cd agent && cargo test`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/src/client.rs
git commit -m "feat(agent): serve fs requests over the control channel"
```

---

### Task 8: Web——文件 API 客户端与文件面板（浏览 + 下载）

**Files:**
- Modify: `web/src/api.ts`
- Create: `web/src/components/FileManagerPanel.tsx`
- Modify: `web/src/components/DeviceList.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/styles.css`
- Test: `web/src/test/FileManagerPanel.test.tsx`（新建）、`web/src/test/App.test.tsx`（扩展）

**Interfaces:**
- Consumes: Task 4 的 REST 端点。
- Produces:
  - `api.FsEntry = { name: string; is_dir: boolean; size: number; mode: number; modified_at: number }`；`api.FsListing = { path: string; entries: FsEntry[] | null }`（Go 对空切片序列化为 null）。
  - `api.listDeviceFiles(deviceId: string, path: string): Promise<FsListing>`；`api.deviceFileURL(deviceId: string, path: string): string`。
  - `FileManagerPanel({ device, onClose }: { device: Device; onClose: () => void })`——默认打开 `~`，用响应里解析后的绝对路径渲染面包屑。
  - `DeviceList` 新可选 prop：`onOpenFiles?: (device: Device) => void`，在线设备显示「Browse files」图标按钮。

- [ ] **Step 1: 写失败测试**

新建 `web/src/test/FileManagerPanel.test.tsx`：

```tsx
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

  it('closes via the close button', async () => {
    mockedApi.listDeviceFiles.mockResolvedValueOnce({ path: '/', entries: [] });
    const onClose = vi.fn();
    render(<FileManagerPanel device={device} onClose={onClose} />);
    await userEvent.click(await screen.findByRole('button', { name: /close file manager/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
```

在 `web/src/test/App.test.tsx` 中：`vi.mock('../api', ...)` 工厂追加 `listDeviceFiles: vi.fn(), deviceFileURL: vi.fn(() => ''), uploadDeviceFile: vi.fn(),`；并新增测试：

```tsx
  it('opens the file manager from the device list', async () => {
    mockedApi.listDeviceFiles.mockResolvedValue({ path: '/home/dev', entries: [] });
    render(
      <AppView
        user={{ id: 'user-1', username: 'admin' }}
        devices={[{ id: 'dev-1', name: 'laptop', platform: 'linux', online: true }]}
        sessions={{}}
        onLogin={vi.fn()}
        onCloseSession={vi.fn()}
        onCreateSession={vi.fn()}
        onRenameSession={vi.fn()}
        agentTokens={[]}
        createdAgentToken={null}
        tokenLoading={false}
        tokenError={null}
        onCreateAgentToken={vi.fn()}
        onRevokeAgentToken={vi.fn()}
        onRefreshAgentTokens={vi.fn()}
      />
    );
    await userEvent.click(screen.getByRole('button', { name: /browse files on laptop/i }));
    expect(await screen.findByRole('dialog', { name: /files on laptop/i })).toBeInTheDocument();
  });
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npm test -- --run`
Expected: FAIL（`FileManagerPanel` 模块不存在、`listDeviceFiles` 未导出）。

- [ ] **Step 3: 实现 api.ts 扩展**

`web/src/api.ts` 末尾追加：

```ts
export type FsEntry = {
  name: string;
  is_dir: boolean;
  size: number;
  mode: number;
  modified_at: number;
};
export type FsListing = { path: string; entries: FsEntry[] | null };

export function listDeviceFiles(deviceId: string, path: string): Promise<FsListing> {
  return request<FsListing>(`/api/devices/${deviceId}/fs?path=${encodeURIComponent(path)}`);
}

export function deviceFileURL(deviceId: string, path: string): string {
  return `/api/devices/${deviceId}/fs/file?path=${encodeURIComponent(path)}`;
}
```

- [ ] **Step 4: 实现 FileManagerPanel**

新建 `web/src/components/FileManagerPanel.tsx`（上传部分 Task 9 再加）：

```tsx
import { useCallback, useEffect, useState } from 'react';
import { ArrowUp, Download, File as FileIcon, Folder, RefreshCw, X } from 'lucide-react';
import type { Device, FsEntry } from '../api';
import * as api from '../api';

function formatSize(size: number): string {
  if (size < 1024) return `${size} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = size;
  let unit = -1;
  do {
    value /= 1024;
    unit += 1;
  } while (value >= 1024 && unit < units.length - 1);
  return `${value.toFixed(1)} ${units[unit]}`;
}

function formatTime(unixSeconds: number): string {
  if (!unixSeconds) return '';
  return new Date(unixSeconds * 1000).toLocaleString();
}

function joinPath(dir: string, name: string): string {
  return dir.endsWith('/') ? `${dir}${name}` : `${dir}/${name}`;
}

function parentPath(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const index = trimmed.lastIndexOf('/');
  return index <= 0 ? '/' : trimmed.slice(0, index);
}

function breadcrumbSegments(path: string): Array<{ label: string; target: string }> {
  const segments = [{ label: '/', target: '/' }];
  let acc = '';
  for (const part of path.split('/').filter(Boolean)) {
    acc += `/${part}`;
    segments.push({ label: part, target: acc });
  }
  return segments;
}

export function FileManagerPanel({ device, onClose }: { device: Device; onClose: () => void }) {
  const [path, setPath] = useState('~');
  const [entries, setEntries] = useState<FsEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (target: string) => {
      setLoading(true);
      setError(null);
      try {
        const listing = await api.listDeviceFiles(device.id, target);
        setPath(listing.path);
        setEntries(listing.entries ?? []);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to list directory');
      } finally {
        setLoading(false);
      }
    },
    [device.id]
  );

  useEffect(() => {
    void load('~');
  }, [load]);

  function downloadEntry(entry: FsEntry) {
    const anchor = document.createElement('a');
    anchor.href = api.deviceFileURL(device.id, joinPath(path, entry.name));
    anchor.download = entry.name;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
  }

  return (
    <div className="filePanelOverlay" role="dialog" aria-label={`Files on ${device.name}`}>
      <section className="filePanel">
        <header className="filePanelHeader">
          <h2>
            <Folder size={16} aria-hidden="true" />
            <span>Files</span>
            <span className="filePanelDevice">{device.name}</span>
          </h2>
          <button className="iconButton" type="button" aria-label="Close file manager" onClick={onClose}>
            <X aria-hidden="true" size={16} />
          </button>
        </header>
        <div className="filePanelToolbar">
          <button
            className="iconButton"
            type="button"
            aria-label="Parent directory"
            disabled={loading || path === '/'}
            onClick={() => void load(parentPath(path))}
          >
            <ArrowUp aria-hidden="true" size={14} />
          </button>
          <nav className="fileBreadcrumbs" aria-label="Current path">
            {breadcrumbSegments(path).map((segment) => (
              <button
                key={segment.target}
                type="button"
                disabled={loading}
                onClick={() => void load(segment.target)}
              >
                {segment.label}
              </button>
            ))}
          </nav>
          <button
            className="iconButton"
            type="button"
            aria-label="Refresh"
            disabled={loading}
            onClick={() => void load(path)}
          >
            <RefreshCw aria-hidden="true" size={14} />
          </button>
        </div>
        {error && (
          <div className="filePanelError" role="alert">
            {error}
          </div>
        )}
        <div className="fileList">
          {entries.map((entry) => (
            <div className="fileRow" key={entry.name}>
              {entry.is_dir ? (
                <button
                  className="fileName"
                  type="button"
                  aria-label={`Open ${entry.name}`}
                  onClick={() => void load(joinPath(path, entry.name))}
                >
                  <Folder aria-hidden="true" size={14} />
                  <span>{entry.name}</span>
                </button>
              ) : (
                <span className="fileName">
                  <FileIcon aria-hidden="true" size={14} />
                  <span>{entry.name}</span>
                </span>
              )}
              <span className="fileSize">{entry.is_dir ? '' : formatSize(entry.size)}</span>
              <span className="fileTime">{formatTime(entry.modified_at)}</span>
              <span className="fileActions">
                {!entry.is_dir && (
                  <button
                    className="iconButton"
                    type="button"
                    aria-label={`Download ${entry.name}`}
                    onClick={() => downloadEntry(entry)}
                  >
                    <Download aria-hidden="true" size={14} />
                  </button>
                )}
              </span>
            </div>
          ))}
          {!loading && !error && entries.length === 0 && <div className="fileEmpty">Empty directory</div>}
        </div>
      </section>
    </div>
  );
}
```

注意：lucide 的 `File` 图标必须重命名导入为 `FileIcon`，避免遮蔽 DOM 的 `File` 类型（Task 9 上传要用）。

- [ ] **Step 5: DeviceList 加文件按钮**

`web/src/components/DeviceList.tsx`：

- import 行加 `FolderOpen`：`import { Check, FolderOpen, Pencil, Terminal, X } from 'lucide-react';`
- props 加 `onOpenFiles`：

```tsx
export function DeviceList({
  devices,
  onCreateSession,
  onRenameDevice,
  onOpenFiles,
  compact = false,
}: {
  devices: Device[];
  onCreateSession: (deviceId: string) => Promise<void>;
  onRenameDevice?: (deviceId: string, name: string) => Promise<void>;
  onOpenFiles?: (device: Device) => void;
  compact?: boolean;
}) {
```

- `deviceActions` div 中「New terminal」按钮之前加：

```tsx
              {onOpenFiles && (
                <button
                  className="iconButton"
                  type="button"
                  aria-label={`Browse files on ${device.name}`}
                  disabled={!device.online || isPending}
                  onClick={() => onOpenFiles(device)}
                >
                  <FolderOpen aria-hidden="true" size={14} />
                </button>
              )}
```

- [ ] **Step 6: App 接线**

`web/src/App.tsx` 的 `AppView` 中：

- import 加 `import { FileManagerPanel } from './components/FileManagerPanel';`
- `const [viewMode, setViewMode] = useState<ViewMode>('terminals');` 之后加：

```tsx
  const [filesDevice, setFilesDevice] = useState<Device | null>(null);
```

- `<DeviceList ... />` 加 prop `onOpenFiles={setFilesDevice}`。
- 根 `<div className="shell">` 的闭合标签前（`AgentTokenManager` 三元表达式之后）加：

```tsx
      {filesDevice && <FileManagerPanel device={filesDevice} onClose={() => setFilesDevice(null)} />}
```

- [ ] **Step 7: 样式**

`web/src/styles.css` 末尾追加（复用现有玻璃令牌）：

```css
/* 文件管理器抽屉 */
.filePanelOverlay {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  justify-content: flex-end;
  background: rgba(5, 6, 12, 0.45);
  backdrop-filter: blur(4px);
}

.filePanel {
  display: flex;
  flex-direction: column;
  width: min(720px, 92vw);
  margin: 16px;
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg-strong);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
  overflow: hidden;
}

.filePanelHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px solid var(--glass-border);
}

.filePanelHeader h2 {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 15px;
  color: var(--text);
}

.filePanelDevice {
  padding: 2px 10px;
  border-radius: var(--r-pill);
  border: 1px solid var(--glass-border);
  background: var(--accent-soft);
  font-size: 12px;
  color: var(--text-dim);
}

.filePanelToolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--glass-border);
}

.fileBreadcrumbs {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px;
  min-width: 0;
}

.fileBreadcrumbs button {
  padding: 2px 6px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--text-dim);
  font-size: 12px;
  font-family: var(--font-mono);
  cursor: pointer;
}

.fileBreadcrumbs button:hover:not(:disabled) {
  background: var(--glass-bg);
  color: var(--text);
}

.filePanelError {
  margin: 10px 16px 0;
  padding: 8px 12px;
  border-radius: var(--r-ctrl);
  border: 1px solid rgba(251, 113, 133, 0.35);
  background: rgba(251, 113, 133, 0.12);
  color: var(--danger);
  font-size: 12px;
}

.fileList {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.fileRow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 90px 170px 40px;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: var(--r-ctrl);
}

.fileRow:hover {
  background: var(--glass-bg);
}

.fileName {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text);
  font-size: 13px;
  text-align: left;
}

.fileName span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

button.fileName {
  cursor: pointer;
}

button.fileName:hover {
  color: var(--accent);
}

.fileSize,
.fileTime {
  color: var(--text-faint);
  font-size: 12px;
  font-family: var(--font-mono);
  text-align: right;
  white-space: nowrap;
}

.fileActions {
  display: flex;
  justify-content: flex-end;
}

.fileEmpty {
  padding: 24px;
  text-align: center;
  color: var(--text-faint);
  font-size: 13px;
}

@media (max-width: 720px) {
  .fileRow {
    grid-template-columns: minmax(0, 1fr) 80px 40px;
  }

  .fileTime {
    display: none;
  }
}
```

- [ ] **Step 8: 运行确认通过**

Run: `cd web && npm test -- --run && npm run build`
Expected: 全部 PASS，构建成功。

- [ ] **Step 9: 提交**

```bash
git add web/src/api.ts web/src/components/FileManagerPanel.tsx web/src/components/DeviceList.tsx web/src/App.tsx web/src/styles.css web/src/test/FileManagerPanel.test.tsx web/src/test/App.test.tsx
git commit -m "feat(web): device file manager panel with browse and download"
```

---

### Task 9: Web——上传（XHR 进度与覆盖确认）

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/components/FileManagerPanel.tsx`
- Modify: `web/src/styles.css`
- Test: `web/src/test/FileManagerPanel.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 `POST /api/devices/{id}/fs/file`（409 = 已存在）。
- Produces:
  - `api.UploadError extends Error`，带 `status: number`。
  - `api.uploadDeviceFile(deviceId: string, filePath: string, file: Blob, options?: { overwrite?: boolean; onProgress?: (percent: number) => void }): Promise<void>`。

- [ ] **Step 1: 写失败测试**

`web/src/test/FileManagerPanel.test.tsx` 追加（`api.uploadDeviceFile`、`api.UploadError` 已在 Task 8 的 mock 工厂里）：

```tsx
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
```

注意：Task 8 的 mock 工厂里 `UploadError` 已是可实例化的 class，`new api.UploadError(409, ...)` 可直接用。

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npm test -- --run`
Expected: FAIL（找不到 `Upload file` 输入）。

- [ ] **Step 3: 实现 api.ts 上传**

`web/src/api.ts` 末尾追加：

```ts
export class UploadError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message || `upload failed with status ${status}`);
    this.status = status;
  }
}

export function uploadDeviceFile(
  deviceId: string,
  filePath: string,
  file: Blob,
  options: { overwrite?: boolean; onProgress?: (percent: number) => void } = {}
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const overwrite = options.overwrite ? '&overwrite=true' : '';
    xhr.open('POST', `/api/devices/${deviceId}/fs/file?path=${encodeURIComponent(filePath)}${overwrite}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        options.onProgress?.(Math.round((event.loaded / event.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new UploadError(xhr.status, `${xhr.status} ${xhr.responseText}`));
      }
    };
    xhr.onerror = () => reject(new UploadError(0, 'network error'));
    xhr.send(file);
  });
}
```

- [ ] **Step 4: 实现面板上传 UI**

`web/src/components/FileManagerPanel.tsx`：

- import 调整：`import { useCallback, useEffect, useRef, useState } from 'react';`、`import type { ChangeEvent } from 'react';`，lucide 增加 `Upload`。
- 组件内加状态与 ref：

```tsx
  const [uploadProgress, setUploadProgress] = useState<number | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
```

- `downloadEntry` 之后加：

```tsx
  async function uploadFile(file: File, overwrite: boolean) {
    setUploadProgress(0);
    setError(null);
    try {
      await api.uploadDeviceFile(device.id, joinPath(path, file.name), file, {
        overwrite,
        onProgress: setUploadProgress,
      });
      setUploadProgress(null);
      await load(path);
    } catch (err) {
      setUploadProgress(null);
      if (err instanceof api.UploadError && err.status === 409 && !overwrite) {
        if (window.confirm(`${file.name} already exists. Overwrite?`)) {
          await uploadFile(file, true);
        }
        return;
      }
      setError(err instanceof Error ? err.message : 'Upload failed');
    }
  }

  function handleFileChosen(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (file) {
      void uploadFile(file, false);
    }
  }
```

- 工具栏 Refresh 按钮之后加：

```tsx
          <button
            className="iconButton"
            type="button"
            aria-label="Upload"
            disabled={loading || uploadProgress !== null}
            onClick={() => fileInputRef.current?.click()}
          >
            <Upload aria-hidden="true" size={14} />
          </button>
          <input
            ref={fileInputRef}
            type="file"
            aria-label="Upload file"
            className="fileUploadInput"
            onChange={handleFileChosen}
          />
```

- error 块之前加进度条：

```tsx
        {uploadProgress !== null && (
          <div className="fileUploadProgress" role="progressbar" aria-valuenow={uploadProgress} aria-valuemin={0} aria-valuemax={100}>
            <div className="fileUploadProgressFill" style={{ width: `${uploadProgress}%` }} />
          </div>
        )}
```

- [ ] **Step 5: 样式**

`web/src/styles.css` 追加：

```css
.fileUploadInput {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

.fileUploadProgress {
  height: 4px;
  margin: 10px 16px 0;
  border-radius: var(--r-pill);
  background: var(--glass-bg);
  overflow: hidden;
}

.fileUploadProgressFill {
  height: 100%;
  border-radius: var(--r-pill);
  background: var(--accent-grad);
  transition: width 0.15s ease;
}
```

- [ ] **Step 6: 运行确认通过**

Run: `cd web && npm test -- --run && npm run build`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add web/src/api.ts web/src/components/FileManagerPanel.tsx web/src/styles.css web/src/test/FileManagerPanel.test.tsx
git commit -m "feat(web): file upload with progress and overwrite confirmation"
```

---

### Task 10: Web——终端输出搜索（Ctrl+F）

**Files:**
- Modify: `web/package.json`（依赖）
- Create: `web/src/components/TerminalSearchBar.tsx`
- Modify: `web/src/components/TerminalPane.tsx`
- Modify: `web/src/styles.css`
- Test: `web/src/test/TerminalSearchBar.test.tsx`（新建）、`web/src/test/TerminalPane.test.tsx`（扩展）

**Interfaces:**
- Consumes: xterm 5.3 旧发行系。
- Produces:
  - `TerminalSearchBar({ onSearch, onClose })`，`onSearch(query: { term: string; caseSensitive: boolean }, direction: 'next' | 'previous')`；Enter=下一个、Shift+Enter=上一个、Esc=关闭。
  - TerminalPane：Ctrl+F/Cmd+F 或右上角搜索按钮打开搜索条；命中用 decorations 高亮；关闭时 `clearDecorations()`。

- [ ] **Step 1: 安装依赖**

Run: `cd web && npm install xterm-addon-search@^0.13.0`
Expected: `package.json` dependencies 出现 `"xterm-addon-search": "^0.13.0"`（该版本配套 xterm 5.x 旧 scope；不要装 `@xterm/addon-search`）。

- [ ] **Step 2: 写失败测试**

新建 `web/src/test/TerminalSearchBar.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { TerminalSearchBar } from '../components/TerminalSearchBar';

describe('TerminalSearchBar', () => {
  it('searches forward on Enter and backward on Shift+Enter', async () => {
    const onSearch = vi.fn();
    render(<TerminalSearchBar onSearch={onSearch} onClose={vi.fn()} />);
    const input = screen.getByLabelText('Search terminal');
    await userEvent.type(input, 'error{Enter}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'error', caseSensitive: false }, 'next');
    await userEvent.type(input, '{Shift>}{Enter}{/Shift}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'error', caseSensitive: false }, 'previous');
  });

  it('toggles case sensitivity', async () => {
    const onSearch = vi.fn();
    render(<TerminalSearchBar onSearch={onSearch} onClose={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /match case/i }));
    await userEvent.type(screen.getByLabelText('Search terminal'), 'X{Enter}');
    expect(onSearch).toHaveBeenLastCalledWith({ term: 'X', caseSensitive: true }, 'next');
  });

  it('closes on Escape and close button', async () => {
    const onClose = vi.fn();
    render(<TerminalSearchBar onSearch={vi.fn()} onClose={onClose} />);
    await userEvent.type(screen.getByLabelText('Search terminal'), '{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: /close search/i }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
```

在 `web/src/test/TerminalPane.test.tsx` 中：

- 现有 `vi.mock('xterm', ...)` 工厂返回的 terminal 对象上追加 `attachCustomKeyEventHandler: vi.fn(),`（否则实现会在真调用处崩）。
- 新增 hoisted 状态与 mock：

```tsx
const searchAddonState = vi.hoisted(() => ({
  instances: [] as Array<{
    findNext: ReturnType<typeof vi.fn>;
    findPrevious: ReturnType<typeof vi.fn>;
    clearDecorations: ReturnType<typeof vi.fn>;
  }>,
}));

vi.mock('xterm-addon-search', () => ({
  SearchAddon: vi.fn().mockImplementation(function () {
    const instance = {
      findNext: vi.fn(),
      findPrevious: vi.fn(),
      clearDecorations: vi.fn(),
    };
    searchAddonState.instances.push(instance);
    return instance;
  }),
}));
```

- 新增测试（放进现有 `describe`，沿用现有渲染辅助；`listSessionOutput` mock 返回 `[]`）：

```tsx
  it('opens search from the toolbar button and forwards queries to the addon', async () => {
    mockedApi.listSessionOutput.mockResolvedValue([]);
    render(<TerminalPane sessionId="sess-1" readOnly={false} />);
    await userEvent.click(screen.getByRole('button', { name: /search terminal output/i }));
    const input = screen.getByLabelText('Search terminal');
    await userEvent.type(input, 'panic{Enter}');
    const addon = searchAddonState.instances.at(-1);
    expect(addon?.findNext).toHaveBeenCalledWith('panic', expect.objectContaining({ caseSensitive: false }));
    await userEvent.type(input, '{Escape}');
    expect(addon?.clearDecorations).toHaveBeenCalled();
  });
```

- [ ] **Step 3: 运行确认失败**

Run: `cd web && npm test -- --run`
Expected: FAIL（`TerminalSearchBar` 不存在、搜索按钮找不到）。

- [ ] **Step 4: 实现 TerminalSearchBar**

新建 `web/src/components/TerminalSearchBar.tsx`：

```tsx
import type { KeyboardEvent } from 'react';
import { useEffect, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, X } from 'lucide-react';

export type SearchQuery = { term: string; caseSensitive: boolean };

export function TerminalSearchBar({
  onSearch,
  onClose,
}: {
  onSearch: (query: SearchQuery, direction: 'next' | 'previous') => void;
  onClose: () => void;
}) {
  const [term, setTerm] = useState('');
  const [caseSensitive, setCaseSensitive] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter') {
      event.preventDefault();
      onSearch({ term, caseSensitive }, event.shiftKey ? 'previous' : 'next');
    } else if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
    }
  }

  return (
    <div className="terminalSearchBar" role="search">
      <input
        ref={inputRef}
        value={term}
        placeholder="Search"
        aria-label="Search terminal"
        onChange={(event) => setTerm(event.target.value)}
        onKeyDown={handleKeyDown}
      />
      <button
        className={caseSensitive ? 'iconButton searchCaseActive' : 'iconButton'}
        type="button"
        aria-label="Match case"
        aria-pressed={caseSensitive}
        onClick={() => setCaseSensitive((current) => !current)}
      >
        Aa
      </button>
      <button
        className="iconButton"
        type="button"
        aria-label="Previous match"
        onClick={() => onSearch({ term, caseSensitive }, 'previous')}
      >
        <ArrowUp aria-hidden="true" size={14} />
      </button>
      <button
        className="iconButton"
        type="button"
        aria-label="Next match"
        onClick={() => onSearch({ term, caseSensitive }, 'next')}
      >
        <ArrowDown aria-hidden="true" size={14} />
      </button>
      <button className="iconButton" type="button" aria-label="Close search" onClick={onClose}>
        <X aria-hidden="true" size={14} />
      </button>
    </div>
  );
}
```

- [ ] **Step 5: TerminalPane 集成**

`web/src/components/TerminalPane.tsx`：

- import 增加：

```tsx
import { SearchAddon } from 'xterm-addon-search';
import { Search } from 'lucide-react';
import { TerminalSearchBar, type SearchQuery } from './TerminalSearchBar';
```

- 组件内加状态与 ref：

```tsx
  const [searchOpen, setSearchOpen] = useState(false);
  const searchAddonRef = useRef<SearchAddon | null>(null);
```

- `new Terminal({...})` 的 options 首行加 `allowProposedApi: true,`（搜索高亮的 decorations 依赖 proposed API）。
- effect 中 `terminal.loadAddon(fit);` 之后加：

```tsx
      const search = new SearchAddon();
      terminal.loadAddon(search);
      searchAddonRef.current = search;
      terminal.attachCustomKeyEventHandler((event) => {
        if (event.type === 'keydown' && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f') {
          setSearchOpen(true);
          return false;
        }
        return true;
      });
```

- effect 返回的清理函数中 `terminal?.dispose();` 之前加 `searchAddonRef.current = null;`。
- 组件函数体内（effect 之外）加：

```tsx
  function runSearch(query: SearchQuery, direction: 'next' | 'previous') {
    const addon = searchAddonRef.current;
    if (!addon || !query.term) return;
    const options = {
      caseSensitive: query.caseSensitive,
      decorations: {
        matchBackground: '#4c3a78',
        matchOverviewRuler: '#a78bfa',
        activeMatchBackground: '#7c5cbf',
        activeMatchColorOverviewRuler: '#c4b5fd',
      },
    };
    if (direction === 'next') {
      addon.findNext(query.term, options);
    } else {
      addon.findPrevious(query.term, options);
    }
  }

  function closeSearch() {
    searchAddonRef.current?.clearDecorations();
    setSearchOpen(false);
  }
```

- JSX 的 `terminalPaneShell` 内、`connectionMessage` 之前加：

```tsx
      <div className="terminalPaneTools">
        {searchOpen ? (
          <TerminalSearchBar onSearch={runSearch} onClose={closeSearch} />
        ) : (
          <button
            className="iconButton"
            type="button"
            aria-label="Search terminal output"
            onClick={() => setSearchOpen(true)}
          >
            <Search aria-hidden="true" size={14} />
          </button>
        )}
      </div>
```

- [ ] **Step 6: 样式**

`web/src/styles.css` 追加：

```css
/* 终端搜索 */
.terminalPaneShell {
  position: relative;
}

.terminalPaneTools {
  position: absolute;
  top: 8px;
  right: 16px;
  z-index: 5;
}

.terminalSearchBar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 6px;
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border-strong);
  background: var(--glass-bg-strong);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow);
}

.terminalSearchBar input {
  width: 180px;
  padding: 4px 8px;
  border: none;
  background: transparent;
  color: var(--text);
  font-size: 12px;
  font-family: var(--font-mono);
  outline: none;
}

.searchCaseActive {
  color: var(--accent);
  border-color: var(--accent);
}
```

注意：若 `.terminalPaneShell` 已有 `position` 声明则不重复添加。

- [ ] **Step 7: 运行确认通过**

Run: `cd web && npm test -- --run && npm run build`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add web/package.json web/package-lock.json web/src/components/TerminalSearchBar.tsx web/src/components/TerminalPane.tsx web/src/styles.css web/src/test/TerminalSearchBar.test.tsx web/src/test/TerminalPane.test.tsx
git commit -m "feat(web): terminal scrollback search with highlight"
```

---

### Task 11: store——command_snippets 表与 CRUD

**Files:**
- Modify: `server/internal/store/store.go`
- Test: `server/internal/store/store_test.go`

**Interfaces:**
- Consumes: 无。
- Produces（Task 12 依赖）:
  - `store.CommandSnippet{ID, Name, Command string; CreatedAt, UpdatedAt time.Time}`。
  - `CreateCommandSnippet(ctx, snippet CommandSnippet) (CommandSnippet, error)`、`ListCommandSnippets(ctx) ([]CommandSnippet, error)`（按 created_at, id 排序）、`GetCommandSnippet(ctx, id) (CommandSnippet, error)`、`UpdateCommandSnippet(ctx, id, name, command string) (CommandSnippet, error)`、`DeleteCommandSnippet(ctx, id string) error`——未命中返回 `ErrNotFound`。

- [ ] **Step 1: 写失败测试**

`server/internal/store/store_test.go` 追加（若文件内已有打开内存库的 helper 就复用；下面的 helper 名不与常见现有名冲突）：

```go
func newSnippetTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCommandSnippetCRUD(t *testing.T) {
	ctx := context.Background()
	db := newSnippetTestDB(t)

	created, err := db.CreateCommandSnippet(ctx, CommandSnippet{ID: "snip-1", Name: "disk", Command: "df -h"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be set")
	}

	list, err := db.ListCommandSnippets(ctx)
	if err != nil || len(list) != 1 || list[0].Command != "df -h" {
		t.Fatalf("list = %#v err = %v", list, err)
	}

	updated, err := db.UpdateCommandSnippet(ctx, "snip-1", "disk usage", "df -h /")
	if err != nil || updated.Name != "disk usage" || updated.Command != "df -h /" {
		t.Fatalf("update = %#v err = %v", updated, err)
	}

	if err := db.DeleteCommandSnippet(ctx, "snip-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.GetCommandSnippet(ctx, "snip-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := db.DeleteCommandSnippet(ctx, "snip-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
	if _, err := db.UpdateCommandSnippet(ctx, "snip-1", "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
	}
}
```

（若 store_test.go 缺 `context`/`errors` import 则补上。）

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/store/`
Expected: FAIL（`CommandSnippet` 未定义）。

- [ ] **Step 3: 实现**

3a. `Migrate` 的 statements 列表末尾加：

```go
		`create table if not exists command_snippets (
			id text primary key,
			name text not null,
			command text not null,
			created_at datetime not null,
			updated_at datetime not null
		)`,
```

3b. `store.go` 类型区（`OutputChunk` 之后）加：

```go
type CommandSnippet struct {
	ID        string
	Name      string
	Command   string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

3c. 文件末尾（helper 之前）加 CRUD：

```go
func (db *DB) CreateCommandSnippet(ctx context.Context, snippet CommandSnippet) (CommandSnippet, error) {
	now := time.Now().UTC()
	if snippet.CreatedAt.IsZero() {
		snippet.CreatedAt = now
	}
	if snippet.UpdatedAt.IsZero() {
		snippet.UpdatedAt = now
	}
	_, err := db.SQL.ExecContext(ctx,
		`insert into command_snippets (id, name, command, created_at, updated_at) values (?, ?, ?, ?, ?)`,
		snippet.ID, snippet.Name, snippet.Command, snippet.CreatedAt, snippet.UpdatedAt)
	return snippet, err
}

func (db *DB) GetCommandSnippet(ctx context.Context, id string) (CommandSnippet, error) {
	row := db.SQL.QueryRowContext(ctx,
		`select id, name, command, created_at, updated_at from command_snippets where id = ?`, id)
	var snippet CommandSnippet
	err := row.Scan(&snippet.ID, &snippet.Name, &snippet.Command, &snippet.CreatedAt, &snippet.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandSnippet{}, ErrNotFound
	}
	return snippet, err
}

func (db *DB) ListCommandSnippets(ctx context.Context) ([]CommandSnippet, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`select id, name, command, created_at, updated_at from command_snippets order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snippets []CommandSnippet
	for rows.Next() {
		var snippet CommandSnippet
		if err := rows.Scan(&snippet.ID, &snippet.Name, &snippet.Command, &snippet.CreatedAt, &snippet.UpdatedAt); err != nil {
			return nil, err
		}
		snippets = append(snippets, snippet)
	}
	return snippets, rows.Err()
}

func (db *DB) UpdateCommandSnippet(ctx context.Context, id string, name string, command string) (CommandSnippet, error) {
	result, err := db.SQL.ExecContext(ctx,
		`update command_snippets set name = ?, command = ?, updated_at = ? where id = ?`,
		name, command, time.Now().UTC(), id)
	if err != nil {
		return CommandSnippet{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandSnippet{}, err
	}
	if affected == 0 {
		return CommandSnippet{}, ErrNotFound
	}
	return db.GetCommandSnippet(ctx, id)
}

func (db *DB) DeleteCommandSnippet(ctx context.Context, id string) error {
	result, err := db.SQL.ExecContext(ctx, `delete from command_snippets where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd server && go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add server/internal/store/store.go server/internal/store/store_test.go
git commit -m "feat(server): command snippet storage"
```

---

### Task 12: httpapi——快捷命令 REST CRUD

**Files:**
- Modify: `server/internal/httpapi/router.go`
- Test: `server/internal/httpapi/router_snippets_test.go`（新建）

**Interfaces:**
- Consumes: Task 11 的 store CRUD。
- Produces:
  - `GET /api/snippets` → 200 `[{id, name, command, created_at, updated_at}]`（空表返回 `[]` 而非 null）。
  - `POST /api/snippets` body `{name, command}` → 201；name/command 去空白后为空 → 400 `invalid_snippet`。
  - `PUT /api/snippets/{id}` body `{name, command}` → 200；不存在 → 404。
  - `DELETE /api/snippets/{id}` → 204；不存在 → 404。
  - 全部要求管理员 Cookie。

- [ ] **Step 1: 写失败测试**

新建 `server/internal/httpapi/router_snippets_test.go`：

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djy/vibe-terminal/server/internal/auth"
	"github.com/djy/vibe-terminal/server/internal/store"
	"github.com/djy/vibe-terminal/server/internal/testutil"
)

func newSnippetRouter(t *testing.T) (http.Handler, []*http.Cookie) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	router := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginRR.Code)
	}
	return router, loginRR.Result().Cookies()
}

func doSnippetRequest(t *testing.T, router http.Handler, cookies []*http.Cookie, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestSnippetCRUDFlow(t *testing.T) {
	router, cookies := newSnippetRouter(t)

	rr := doSnippetRequest(t, router, cookies, http.MethodGet, "/api/snippets", "")
	if rr.Code != http.StatusOK || rr.Body.String() != "[]\n" {
		t.Fatalf("empty list status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"disk","command":"df -h"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created["id"] == "" || created["command"] != "df -h" {
		t.Fatalf("created = %#v", created)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodPut, "/api/snippets/"+created["id"], `{"name":"disk usage","command":"df -h /"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodGet, "/api/snippets", "")
	var list []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0]["name"] != "disk usage" {
		t.Fatalf("list = %#v", list)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodDelete, "/api/snippets/"+created["id"], "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rr.Code)
	}

	rr = doSnippetRequest(t, router, cookies, http.MethodDelete, "/api/snippets/"+created["id"], "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d", rr.Code)
	}
}

func TestSnippetValidationAndAuth(t *testing.T) {
	router, cookies := newSnippetRouter(t)

	rr := doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"  ","command":"ls"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank name status=%d", rr.Code)
	}
	rr = doSnippetRequest(t, router, cookies, http.MethodPost, "/api/snippets", `{"name":"ls","command":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank command status=%d", rr.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/snippets", nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", rr.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/httpapi/`
Expected: FAIL（404，路由未注册）。

- [ ] **Step 3: 实现**

3a. `routes()` 中 `POST /api/agents/register` 之前加：

```go
	r.mux.HandleFunc("GET /api/snippets", r.handleListSnippets)
	r.mux.HandleFunc("POST /api/snippets", r.handleCreateSnippet)
	r.mux.HandleFunc("PUT /api/snippets/", r.handleUpdateSnippet)
	r.mux.HandleFunc("DELETE /api/snippets/", r.handleDeleteSnippet)
```

3b. handler 实现（放在 token handler 区之后）：

```go
type snippetBody struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func (b *snippetBody) normalize() bool {
	b.Name = strings.TrimSpace(b.Name)
	return b.Name != "" && strings.TrimSpace(b.Command) != ""
}

func snippetResponse(snippet store.CommandSnippet) map[string]string {
	return map[string]string{
		"id":         snippet.ID,
		"name":       snippet.Name,
		"command":    snippet.Command,
		"created_at": snippet.CreatedAt.Format(time.RFC3339),
		"updated_at": snippet.UpdatedAt.Format(time.RFC3339),
	}
}

func snippetIDFromPath(path string) string {
	id := strings.TrimPrefix(path, "/api/snippets/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func (r *router) handleListSnippets(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	snippets, err := r.store.ListCommandSnippets(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snippet_error", "failed to list snippets")
		return
	}
	out := make([]map[string]string, 0, len(snippets))
	for _, snippet := range snippets {
		out = append(out, snippetResponse(snippet))
	}
	writeJSON(w, http.StatusOK, out)
}

func (r *router) handleCreateSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	var body snippetBody
	if !readJSON(w, req, &body) {
		return
	}
	if !body.normalize() {
		writeError(w, http.StatusBadRequest, "invalid_snippet", "name and command are required")
		return
	}
	snippet, err := r.store.CreateCommandSnippet(req.Context(), store.CommandSnippet{
		ID:      uuid.NewString(),
		Name:    body.Name,
		Command: body.Command,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snippet_error", "failed to create snippet")
		return
	}
	writeJSON(w, http.StatusCreated, snippetResponse(snippet))
}

func (r *router) handleUpdateSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	id := snippetIDFromPath(req.URL.Path)
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "snippet not found")
		return
	}
	var body snippetBody
	if !readJSON(w, req, &body) {
		return
	}
	if !body.normalize() {
		writeError(w, http.StatusBadRequest, "invalid_snippet", "name and command are required")
		return
	}
	snippet, err := r.store.UpdateCommandSnippet(req.Context(), id, body.Name, body.Command)
	if err != nil {
		writeStoreError(w, err, "snippet")
		return
	}
	writeJSON(w, http.StatusOK, snippetResponse(snippet))
}

func (r *router) handleDeleteSnippet(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.requireUser(w, req); !ok {
		return
	}
	id := snippetIDFromPath(req.URL.Path)
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "snippet not found")
		return
	}
	if err := r.store.DeleteCommandSnippet(req.Context(), id); err != nil {
		writeStoreError(w, err, "snippet")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd server && go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add server/internal/httpapi/router.go server/internal/httpapi/router_snippets_test.go
git commit -m "feat(server): snippet REST endpoints"
```

---

### Task 13: Web——快捷命令 UI 与终端注入

**Files:**
- Modify: `web/src/api.ts`
- Create: `web/src/components/SnippetsBar.tsx`
- Modify: `web/src/components/TerminalPane.tsx`（forwardRef + sendText）
- Modify: `web/src/components/TerminalTabs.tsx`
- Modify: `web/src/styles.css`
- Test: `web/src/test/SnippetsBar.test.tsx`（新建）、`web/src/test/TerminalPane.test.tsx`（扩展）

**Interfaces:**
- Consumes: Task 12 的 REST；现有 `ws.encodeStdin`。
- Produces:
  - `api.Snippet = { id: string; name: string; command: string; created_at: string; updated_at: string }`；`api.listSnippets()`、`api.createSnippet(name, command)`、`api.updateSnippet(id, name, command)`、`api.deleteSnippet(id)`。
  - `TerminalPaneHandle = { sendText: (text: string) => void }`；`TerminalPane` 改为 `forwardRef<TerminalPaneHandle, TerminalPaneProps>`，`sendText` 经当前 WebSocket 发 `encodeStdin(sessionId, text)`（只读或未连接时忽略）。
  - `SnippetsBar({ onInsert }: { onInsert: (command: string) => void })`——点击条目调用 `onInsert(command)` 并收起；内置增/改/删管理；提示文案说明「插入不执行，需自行回车」。

- [ ] **Step 1: 写失败测试**

新建 `web/src/test/SnippetsBar.test.tsx`：

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { SnippetsBar } from '../components/SnippetsBar';

vi.mock('../api', () => ({
  listSnippets: vi.fn(),
  createSnippet: vi.fn(),
  updateSnippet: vi.fn(),
  deleteSnippet: vi.fn(),
}));

const mockedApi = vi.mocked(api);
const snippet = {
  id: 'snip-1',
  name: 'disk',
  command: 'df -h',
  created_at: '2026-07-02T00:00:00Z',
  updated_at: '2026-07-02T00:00:00Z',
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SnippetsBar', () => {
  it('loads snippets when opened and inserts on click', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    const onInsert = vi.fn();
    render(<SnippetsBar onInsert={onInsert} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /insert disk/i }));
    expect(onInsert).toHaveBeenCalledWith('df -h');
    expect(screen.queryByRole('button', { name: /insert disk/i })).not.toBeInTheDocument();
  });

  it('creates a snippet from the form', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([]);
    mockedApi.createSnippet.mockResolvedValueOnce({ ...snippet, id: 'snip-2', name: 'uptime', command: 'uptime' });
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.type(await screen.findByLabelText('Snippet name'), 'uptime');
    await userEvent.type(screen.getByLabelText('Snippet command'), 'uptime');
    await userEvent.click(screen.getByRole('button', { name: /^add$/i }));
    await waitFor(() => expect(mockedApi.createSnippet).toHaveBeenCalledWith('uptime', 'uptime'));
    expect(await screen.findByRole('button', { name: /insert uptime/i })).toBeInTheDocument();
  });

  it('deletes a snippet', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    mockedApi.deleteSnippet.mockResolvedValueOnce(undefined);
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /delete disk/i }));
    await waitFor(() => expect(mockedApi.deleteSnippet).toHaveBeenCalledWith('snip-1'));
    expect(screen.queryByRole('button', { name: /insert disk/i })).not.toBeInTheDocument();
  });

  it('edits a snippet', async () => {
    mockedApi.listSnippets.mockResolvedValueOnce([snippet]);
    mockedApi.updateSnippet.mockResolvedValueOnce({ ...snippet, name: 'disk usage', command: 'df -h /' });
    render(<SnippetsBar onInsert={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: /quick commands/i }));
    await userEvent.click(await screen.findByRole('button', { name: /edit disk/i }));
    const nameInput = screen.getByLabelText('Snippet name') as HTMLInputElement;
    expect(nameInput.value).toBe('disk');
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, 'disk usage');
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(mockedApi.updateSnippet).toHaveBeenCalledWith('snip-1', 'disk usage', 'df -h'));
  });
});
```

在 `web/src/test/TerminalPane.test.tsx` 中追加 sendText 测试（沿用现有 webSocketState mock；`OPEN` 状态按现有 mock 的约定设置）：

```tsx
  it('sends snippet text through the session socket via the imperative handle', async () => {
    mockedApi.listSessionOutput.mockResolvedValue([]);
    const ref = { current: null as null | { sendText: (text: string) => void } };
    render(<TerminalPane ref={ref} sessionId="sess-1" readOnly={false} />);
    await waitFor(() => expect(ref.current).not.toBeNull());
    const socket = webSocketState.instances.at(-1);
    ref.current?.sendText('df -h');
    expect(socket?.send).toHaveBeenCalledWith(
      JSON.stringify({ type: 'stdin', session_id: 'sess-1', payload: { session_id: 'sess-1', data: 'df -h' } })
    );
  });
```

注意：现有 WebSocket mock 若 `readyState` 不是 `WebSocket.OPEN`，需在该 mock 实例上补 `readyState: WebSocket.OPEN`（对照现有 mock 实现调整——stdin 已有同样的 OPEN 判定，现有测试若能发 stdin 则无需改动）。

另外 `TerminalPane.test.tsx` / `App.test.tsx` 的 `vi.mock('../api', ...)` 工厂需补 `listSnippets: vi.fn(() => Promise.resolve([]))`（TerminalTabs 渲染 SnippetsBar，打开前不会调用，但补上更稳）。

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npm test -- --run`
Expected: FAIL（`SnippetsBar` 不存在、`TerminalPane` 不接受 ref）。

- [ ] **Step 3: 实现 api.ts**

`web/src/api.ts` 末尾追加：

```ts
export type Snippet = {
  id: string;
  name: string;
  command: string;
  created_at: string;
  updated_at: string;
};

export function listSnippets(): Promise<Snippet[]> {
  return request<Snippet[]>('/api/snippets');
}

export function createSnippet(name: string, command: string): Promise<Snippet> {
  return request<Snippet>('/api/snippets', {
    method: 'POST',
    body: JSON.stringify({ name, command }),
  });
}

export function updateSnippet(id: string, name: string, command: string): Promise<Snippet> {
  return request<Snippet>(`/api/snippets/${id}`, {
    method: 'PUT',
    body: JSON.stringify({ name, command }),
  });
}

export function deleteSnippet(id: string): Promise<void> {
  return request<void>(`/api/snippets/${id}`, {
    method: 'DELETE',
  });
}
```

- [ ] **Step 4: TerminalPane forwardRef + sendText**

`web/src/components/TerminalPane.tsx`：

- import 改为包含 `forwardRef, useImperativeHandle`：

```tsx
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
```

- 组件签名改为：

```tsx
export type TerminalPaneHandle = {
  sendText: (text: string) => void;
};

export const TerminalPane = forwardRef<TerminalPaneHandle, TerminalPaneProps>(function TerminalPane(
  { sessionId, readOnly, onSessionStateChange },
  ref
) {
```

（文件末尾补齐 `});` 收尾。）

- 组件内加 `const socketRef = useRef<WebSocket | null>(null);`；effect 中 `socket = new WebSocket(webSocketURL());` 之后加 `socketRef.current = socket;`；清理函数 `socket?.close();` 之前加 `socketRef.current = null;`。
- effect 之后加：

```tsx
  useImperativeHandle(
    ref,
    () => ({
      sendText(text: string) {
        const socket = socketRef.current;
        if (!readOnly && socket && socket.readyState === WebSocket.OPEN) {
          socket.send(encodeStdin(sessionId, text));
        }
      },
    }),
    [sessionId, readOnly]
  );
```

- [ ] **Step 5: 实现 SnippetsBar**

新建 `web/src/components/SnippetsBar.tsx`：

```tsx
import type { FormEvent } from 'react';
import { useEffect, useState } from 'react';
import { ChevronDown, Pencil, Plus, Trash2, Zap } from 'lucide-react';
import type { Snippet } from '../api';
import * as api from '../api';

export function SnippetsBar({ onInsert }: { onInsert: (command: string) => void }) {
  const [open, setOpen] = useState(false);
  const [snippets, setSnippets] = useState<Snippet[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Snippet | null>(null);
  const [draftName, setDraftName] = useState('');
  const [draftCommand, setDraftCommand] = useState('');
  const [pending, setPending] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .listSnippets()
      .then((items) => {
        if (!cancelled) setSnippets(items ?? []);
      })
      .catch(() => {
        if (!cancelled) setError('Failed to load snippets.');
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  function insert(snippet: Snippet) {
    onInsert(snippet.command);
    setOpen(false);
  }

  function startEdit(snippet: Snippet) {
    setEditing(snippet);
    setDraftName(snippet.name);
    setDraftCommand(snippet.command);
  }

  function resetForm() {
    setEditing(null);
    setDraftName('');
    setDraftCommand('');
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = draftName.trim();
    if (!name || !draftCommand.trim()) return;
    setPending(true);
    setError(null);
    try {
      if (editing) {
        const updated = await api.updateSnippet(editing.id, name, draftCommand);
        setSnippets((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      } else {
        const created = await api.createSnippet(name, draftCommand);
        setSnippets((current) => [...current, created]);
      }
      resetForm();
    } catch {
      setError('Failed to save snippet.');
    } finally {
      setPending(false);
    }
  }

  async function remove(snippet: Snippet) {
    setError(null);
    try {
      await api.deleteSnippet(snippet.id);
      setSnippets((current) => current.filter((item) => item.id !== snippet.id));
    } catch {
      setError('Failed to delete snippet.');
    }
  }

  return (
    <div className="snippetsBar">
      <button
        className="snippetsToggle"
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        <Zap size={14} aria-hidden="true" />
        <span>Quick commands</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <div className="snippetsPopover">
          {error && (
            <div className="snippetsError" role="alert">
              {error}
            </div>
          )}
          {!error && snippets.length === 0 && <div className="snippetsEmpty">No snippets yet</div>}
          <ul className="snippetsList">
            {snippets.map((snippet) => (
              <li key={snippet.id} className="snippetRow">
                <button
                  className="snippetInsert"
                  type="button"
                  aria-label={`Insert ${snippet.name}`}
                  title={snippet.command}
                  onClick={() => insert(snippet)}
                >
                  <strong>{snippet.name}</strong>
                  <code>{snippet.command}</code>
                </button>
                <button
                  className="iconButton"
                  type="button"
                  aria-label={`Edit ${snippet.name}`}
                  onClick={() => startEdit(snippet)}
                >
                  <Pencil aria-hidden="true" size={13} />
                </button>
                <button
                  className="iconButton danger"
                  type="button"
                  aria-label={`Delete ${snippet.name}`}
                  onClick={() => void remove(snippet)}
                >
                  <Trash2 aria-hidden="true" size={13} />
                </button>
              </li>
            ))}
          </ul>
          <form className="snippetForm" onSubmit={submit}>
            <input
              value={draftName}
              placeholder="Name"
              aria-label="Snippet name"
              disabled={pending}
              onChange={(event) => setDraftName(event.target.value)}
            />
            <input
              value={draftCommand}
              placeholder="Command"
              aria-label="Snippet command"
              disabled={pending}
              onChange={(event) => setDraftCommand(event.target.value)}
            />
            <button type="submit" disabled={pending || !draftName.trim() || !draftCommand.trim()}>
              <Plus size={14} aria-hidden="true" />
              {editing ? 'Save' : 'Add'}
            </button>
            {editing && (
              <button type="button" disabled={pending} onClick={resetForm}>
                Cancel
              </button>
            )}
          </form>
          <p className="snippetsHint">Click a snippet to type it into the active terminal, then press Enter yourself.</p>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 6: TerminalTabs 接线**

`web/src/components/TerminalTabs.tsx`：

- import 改动：

```tsx
import { useCallback, useEffect, useRef, useState } from 'react';
import { SnippetsBar } from './SnippetsBar';
import { TerminalPane, type TerminalPaneHandle } from './TerminalPane';
```

- 组件内加 `const paneRef = useRef<TerminalPaneHandle | null>(null);`
- `terminalHeader` 的 `<div>...</div>` 之后（header 闭合前）加：

```tsx
        <SnippetsBar onInsert={(command) => paneRef.current?.sendText(command)} />
```

- `<TerminalPane` 加 `ref={paneRef}`。

- [ ] **Step 7: 样式**

`web/src/styles.css` 追加：

```css
/* 快捷命令 */
.snippetsBar {
  position: relative;
}

.snippetsToggle {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: var(--r-ctrl);
  border: 1px solid var(--glass-border);
  background: var(--glass-bg);
  color: var(--text-dim);
  font-size: 12px;
  cursor: pointer;
}

.snippetsToggle:hover {
  color: var(--text);
  border-color: var(--glass-border-strong);
}

.snippetsPopover {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  z-index: 40;
  width: min(420px, 90vw);
  padding: 10px;
  border-radius: var(--r-card);
  border: 1px solid var(--glass-border-strong);
  background: var(--glass-bg-strong);
  backdrop-filter: var(--glass-blur);
  box-shadow: var(--glass-shadow), var(--glass-highlight);
}

.snippetsList {
  margin: 0;
  padding: 0;
  list-style: none;
  max-height: 240px;
  overflow-y: auto;
}

.snippetRow {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 2px 0;
}

.snippetInsert {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  min-width: 0;
  padding: 6px 8px;
  border: none;
  border-radius: var(--r-ctrl);
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}

.snippetInsert:hover {
  background: var(--glass-bg);
}

.snippetInsert strong {
  font-size: 12px;
}

.snippetInsert code {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-faint);
  font-size: 11px;
  font-family: var(--font-mono);
}

.snippetForm {
  display: flex;
  gap: 6px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--glass-border);
}

.snippetForm input {
  flex: 1;
  min-width: 0;
}

.snippetsEmpty,
.snippetsHint {
  padding: 6px 8px;
  color: var(--text-faint);
  font-size: 11px;
}

.snippetsHint {
  margin: 6px 0 0;
}

.snippetsError {
  padding: 6px 8px;
  border-radius: var(--r-ctrl);
  background: rgba(251, 113, 133, 0.12);
  color: var(--danger);
  font-size: 12px;
}
```

`terminalHeader` 若当前不是 flex 两端布局，需要补：

```css
.terminalHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
```

（若已有等效声明则跳过。）

- [ ] **Step 8: 运行确认通过**

Run: `cd web && npm test -- --run && npm run build`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add web/src/api.ts web/src/components/SnippetsBar.tsx web/src/components/TerminalPane.tsx web/src/components/TerminalTabs.tsx web/src/styles.css web/src/test/SnippetsBar.test.tsx web/src/test/TerminalPane.test.tsx web/src/test/App.test.tsx
git commit -m "feat(web): quick command snippets with terminal insertion"
```

---

### Task 14: 文档更新与全量验证

**Files:**
- Modify: `README.md`
- Modify: `docs/protocol/v1.md`

**Interfaces:**
- Consumes: 前面全部任务的最终行为。
- Produces: 与实现一致的文档。

- [ ] **Step 1: 更新 README.md**

- 「特性」列表追加三行：

```markdown
- 设备文件管理器：浏览目录、下载文件、上传文件（带进度与覆盖确认）。
- 终端输出搜索：Ctrl+F / Cmd+F 在回滚缓冲中查找并高亮。
- 快捷命令片段：保存常用命令，点击注入当前会话（不自动回车）。
```

- 「协议」的消息列表追加：`fs_list`、`fs_list_result`、`fs_read`、`fs_read_result`、`fs_write_open`、`fs_write_opened`、`fs_write_chunk`、`fs_write_ack`、`fs_write_close`、`fs_write_result`、`fs_error`；并注明 `agent_hello` 可携带 `capabilities`。
- 「配置」表格追加一行：

```markdown
| `fs_max_upload_size` | `536870912` | 上传单文件大小上限（字节） |
```

并在环境变量覆盖列表中补 `VIBE_FS_MAX_UPLOAD_SIZE`。
- 「已知限制」追加：

```markdown
- 文件传输期间被控端文件被并发修改时，下载内容可能撕裂（无快照一致性）。
- 终端搜索只作用于浏览器内的回滚缓冲，不搜索仅存于磁盘的历史输出。
- 文件管理器不提供删除/重命名/新建目录，请在终端内完成。
```

- [ ] **Step 2: 更新 docs/protocol/v1.md**

追加「文件传输扩展」小节：capabilities 声明方式、11 个 fs 消息的方向/payload 字段表、request_id 关联语义、分块与超时参数（256 KiB、30s、60s 清理）、错误码列表（`not_found`、`permission_denied`、`not_a_file`、`not_a_directory`、`already_exists`、`invalid_path`、`invalid_request`、`io_error`）。以该文件既有格式为准。

- [ ] **Step 3: 全量验证**

Run（仓库根目录）: `make test`
Expected: 四个子检查全部通过——server go test、agent cargo test、web vitest + build、docker compose config。

- [ ] **Step 4: 提交**

注意：`docs/` 目录在 .gitignore 中（项目约定：docs 只留本地），协议文档更新不提交，只提交 README。

```bash
git add README.md
git commit -m "docs: document file manager, terminal search and snippets"
```

---

## 计划自审结论

1. **规格覆盖**：文件管理器（协议扩展→Task 1/5，桥接→Task 2/3，REST/配置/审计→Task 4，agent 实现→Task 6/7，前端浏览/下载→Task 8、上传→Task 9）；终端搜索→Task 10；快捷命令（表→Task 11，REST→Task 12，UI/注入→Task 13）；文档→Task 14。规格「明确不做」清单未被任何任务实现。无缺口。
2. **占位符**：无 TBD/TODO；每个代码步骤给出完整代码；两处「以现有文件实际内容为准」的说明（AgentConfig 字段、protocol v1.md 格式）是对既有代码的对照指引，不是占位。
3. **类型一致性**：`FsListing.entries` 允许 null（Go nil 切片）与前端 `?? []` 对齐；Go `FsRead{Offset int64, Length int}` ↔ Rust `FsRead{offset: u64, length: u32}` JSON 兼容；`files.Service` 方法签名在 Task 3 Produces 与 Task 4 接口定义逐字一致；`TerminalPaneHandle.sendText`、`SnippetsBar.onInsert` 在 Task 13 内部自洽；`upload_id` 贯穿 open/chunk/close 三类消息两端命名一致。

## 执行交接

按 writing-plans 约定，执行时二选一：

1. **Subagent-Driven（推荐）**——用 superpowers:subagent-driven-development，每任务派发新子代理，任务间审查。
2. **Inline**——用 superpowers:executing-plans，本会话内分批执行加检查点。





