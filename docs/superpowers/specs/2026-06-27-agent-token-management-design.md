# Agent Token 管理页面设计

## 背景

当前系统已经支持管理员创建 agent 注册 token，并支持 agent 使用 token 完成注册。后端已有 `agent_tokens` 表，包含 `used_at`、`revoked_at` 和 `expires_at` 字段；注册逻辑已经拒绝已使用、已吊销或已过期的 token。前端目前只有登录、设备列表和终端标签页，还没有 token 管理入口。

本次功能目标是在前端加入 agent token 管理页面，并补齐后端吊销能力。删除语义采用软删除，也就是设置 `revoked_at`，保留历史记录和审计线索。

## 范围

本次包含：

- 管理员查看 agent token 列表。
- 管理员创建新的 agent token。
- 创建成功后一次性展示 token 原文，并提供复制按钮。
- 管理员吊销尚未吊销的 token。
- 前端展示 token 状态：可用、已使用、已过期、已吊销。
- 后端新增 token 吊销接口和存储层方法。
- 覆盖后端和前端关键测试。

本次不包含：

- 批量吊销。
- token 名称编辑。
- token 列表筛选或搜索。
- 物理删除 token 记录。
- 对已注册 device 的凭据吊销。

## 后端设计

### 存储层

在 `server/internal/store` 中新增：

```go
func (db *DB) RevokeAgentToken(ctx context.Context, id string, revokedAt time.Time) (AgentToken, error)
```

行为：

- 根据 `id` 查找 token。
- 如果不存在，返回 `store.ErrNotFound`。
- 如果 token 已吊销，返回当前 token，不重复更新时间。
- 如果未吊销，将 `revoked_at` 设置为传入时间，并返回更新后的 token。

注册逻辑 `UseAgentTokenByHash` 已经包含 `revoked_at is null` 条件，因此吊销后无需额外改注册校验。

### HTTP API

新增路由：

```http
DELETE /api/agent-tokens/{id}
```

行为：

- 需要管理员登录。
- 成功时返回被吊销后的 token 元数据，不包含 `token_hash` 和 token 原文。
- token 不存在时返回 `404`。
- 其他错误返回 `500`。
- 写入审计事件 `agent_token_revoked`。

现有接口保持不变：

- `GET /api/agent-tokens`
- `POST /api/agent-tokens`

列表和吊销响应都只返回元数据字段：

- `id`
- `name`
- `expires_at`
- `used_at`
- `revoked_at`
- `created_at`

创建接口继续额外返回一次性 `token` 原文。

## 前端设计

### API 封装

在 `web/src/api.ts` 中新增类型和函数：

- `AgentToken`
- `CreatedAgentToken`
- `listAgentTokens()`
- `createAgentToken(name: string, ttlHours: number)`
- `revokeAgentToken(id: string)`

`CreatedAgentToken` 在 `AgentToken` 元数据基础上包含 `token` 字段。

### 信息架构

现有 `AppView` 在登录后显示单一 shell。新增一个轻量视图状态：

```ts
type ViewMode = 'terminals' | 'agentTokens';
```

左侧栏顶部增加导航按钮：

- `Terminals`
- `Agent Tokens`

选择 `Terminals` 时保持现有设备列表和终端区域行为。选择 `Agent Tokens` 时主区域显示 token 管理页面，左侧仍保留导航和设备摘要，避免引入完整路由系统。

### Token 管理页面

新增组件 `AgentTokenManager`，负责：

- 登录后加载 token 列表。
- 提交名称和有效期小时数创建 token。
- 创建成功后在页面顶部显示一次性 token 原文。
- 提供复制按钮。
- 以表格或紧凑列表展示 token 元数据。
- 对未吊销 token 提供吊销按钮，并要求二次确认。
- 吊销成功后更新本地列表。
- 加载、错误、空列表状态。

状态规则：

- `revoked_at` 有值：已吊销。
- 否则 `used_at` 有值：已使用。
- 否则 `expires_at` 早于当前时间：已过期。
- 否则：可用。

按钮规则：

- 已吊销 token 的吊销按钮禁用或隐藏。
- 已过期 token 可以吊销，但吊销只是标记历史状态；页面仍显示最终状态为已吊销。
- 已使用 token 可以吊销，但不影响已经注册完成的 device 凭据。

### 安全与可用性

- 列表不展示 token 原文或 token hash。
- 新 token 原文只存在于创建成功响应和当前前端状态中，页面刷新后消失。
- 复制失败时显示明确错误。
- 创建表单默认名称为 `agent`，默认有效期为 `24` 小时。
- 有效期输入限制为正整数。

## 测试设计

### 后端

在 `server/internal/store/store_test.go` 中覆盖：

- 创建 token 后可以吊销。
- 已吊销 token 不能通过 `UseAgentTokenByHash` 使用。
- 重复吊销不改变已存在的 `revoked_at`。

在 `server/internal/httpapi/router_test.go` 中覆盖：

- 登录管理员可以创建、列表查看、吊销 token。
- 吊销后再次注册返回未授权。
- 未登录吊销返回未授权。
- 不存在的 token 返回 `404`。

### 前端

在 `web/src/test/App.test.tsx` 或新增组件测试中覆盖：

- 管理员可以切换到 `Agent Tokens` 页面。
- 页面加载并展示 token 列表与状态。
- 创建 token 后显示一次性 token 和复制按钮。
- 点击吊销并确认后调用 API，并更新状态。

## 验证

实现完成后运行：

```sh
go test ./...
npm test -- --run
npm run build
```

前端命令在 `web/` 目录执行。
