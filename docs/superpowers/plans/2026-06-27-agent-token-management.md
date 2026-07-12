# Agent Token Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为管理员提供 agent token 创建、查看和吊销页面，并补齐后端软删除接口。

**Architecture:** 后端复用现有 `agent_tokens.revoked_at` 字段实现软删除，新增 store 方法和 `DELETE /api/agent-tokens/{id}`。前端在现有登录后 shell 中加入轻量视图切换，新增独立 `AgentTokenManager` 组件负责 token 列表、创建、一次性 token 展示、复制和吊销确认。

**Tech Stack:** Go `net/http` + SQLite store + Vitest/Testing Library + React/Vite + TypeScript + lucide-react。

---

## 文件结构

- Modify: `server/internal/store/store.go`
  - 新增 `RevokeAgentToken`，返回更新后的 `AgentToken`。
- Modify: `server/internal/store/store_test.go`
  - 覆盖吊销、吊销后不可使用、重复吊销保持时间不变。
- Modify: `server/internal/httpapi/router.go`
  - 注册 `DELETE /api/agent-tokens/{id}`，新增响应转换 helper 和吊销 handler。
- Modify: `server/internal/httpapi/router_test.go`
  - 覆盖登录管理员吊销、吊销后注册失败、未登录失败、不存在返回 404。
- Modify: `web/src/api.ts`
  - 新增 `AgentToken`、`CreatedAgentToken` 类型和三个 API 方法。
- Create: `web/src/components/AgentTokenManager.tsx`
  - 新增 token 管理页面组件。
- Modify: `web/src/App.tsx`
  - 加入 `ViewMode` 状态和左侧导航，把 token 页面接入主界面。
- Modify: `web/src/styles.css`
  - 增加导航、token 页面、表单、状态徽章和响应式样式。
- Modify: `web/src/test/App.test.tsx`
  - 扩展 API mock，覆盖视图切换、创建和吊销交互。

## Task 1: 后端存储层吊销能力

**Files:**
- Modify: `server/internal/store/store_test.go`
- Modify: `server/internal/store/store.go`

- [ ] **Step 1: 写失败测试**

在 `server/internal/store/store_test.go` 中追加：

```go
func TestRevokeAgentToken(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour)
	_, err = db.CreateAgentToken(ctx, CreateAgentTokenParams{
		ID:        "tok-revoke",
		Name:      "laptop",
		TokenHash: "hash-revoke",
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	revokedAt := time.Now().UTC().Truncate(time.Second)
	revoked, err := db.RevokeAgentToken(ctx, "tok-revoke", revokedAt)
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if !revoked.RevokedAt.Valid {
		t.Fatal("revoked token should have revoked_at")
	}
	if !revoked.RevokedAt.Time.Equal(revokedAt) {
		t.Fatalf("revoked_at = %s, want %s", revoked.RevokedAt.Time, revokedAt)
	}

	_, err = db.UseAgentTokenByHash(ctx, "hash-revoke", time.Now().UTC())
	if err == nil {
		t.Fatal("revoked token should not be usable")
	}

	later := revokedAt.Add(time.Hour)
	again, err := db.RevokeAgentToken(ctx, "tok-revoke", later)
	if err != nil {
		t.Fatalf("revoke token again: %v", err)
	}
	if !again.RevokedAt.Time.Equal(revokedAt) {
		t.Fatalf("second revoke changed revoked_at to %s", again.RevokedAt.Time)
	}

	_, err = db.RevokeAgentToken(ctx, "missing-token", time.Now().UTC())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token error = %v, want ErrNotFound", err)
	}
}
```

同时把 `errors` 加入该测试文件 import：

```go
import (
	"context"
	"errors"
	"testing"
	"time"
)
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```sh
go test ./server/internal/store -run TestRevokeAgentToken
```

Expected: FAIL，错误包含 `db.RevokeAgentToken undefined`。

- [ ] **Step 3: 写最小实现**

在 `server/internal/store/store.go` 的 `UseAgentTokenByHash` 前加入：

```go
func (db *DB) RevokeAgentToken(ctx context.Context, id string, revokedAt time.Time) (AgentToken, error) {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return AgentToken{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`select id, name, token_hash, expires_at, used_at, revoked_at, created_at
		 from agent_tokens where id = ?`,
		id)
	var token AgentToken
	err = row.Scan(&token.ID, &token.Name, &token.TokenHash, &token.ExpiresAt, &token.UsedAt, &token.RevokedAt, &token.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentToken{}, ErrNotFound
	}
	if err != nil {
		return AgentToken{}, err
	}
	if token.RevokedAt.Valid {
		return token, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `update agent_tokens set revoked_at = ? where id = ?`, revokedAt, id)
	if err != nil {
		return AgentToken{}, err
	}
	token.RevokedAt = sql.NullTime{Time: revokedAt, Valid: true}
	if err := tx.Commit(); err != nil {
		return AgentToken{}, err
	}
	return token, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run:

```sh
go test ./server/internal/store -run TestRevokeAgentToken
```

Expected: PASS。

- [ ] **Step 5: 提交**

Run:

```sh
git add server/internal/store/store.go server/internal/store/store_test.go
git commit -m "feat: revoke agent tokens in store"
```

## Task 2: 后端 HTTP 吊销接口

**Files:**
- Modify: `server/internal/httpapi/router_test.go`
- Modify: `server/internal/httpapi/router.go`

- [ ] **Step 1: 写失败测试**

在 `server/internal/httpapi/router_test.go` 中追加：

```go
func TestAgentTokenRevokeFlow(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewStore(t)
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.CreateUser(ctx, store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})

	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)
	cookies := loginRR.Result().Cookies()

	createReq := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", bytes.NewBufferString(`{"name":"desk","ttl_hours":24}`))
	createReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		createReq.AddCookie(cookie)
	}
	createRR := httptest.NewRecorder()
	handler.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRR.Code, createRR.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(createRR.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/"+created["id"], nil)
	for _, cookie := range cookies {
		revokeReq.AddCookie(cookie)
	}
	revokeRR := httptest.NewRecorder()
	handler.ServeHTTP(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body=%s", revokeRR.Code, revokeRR.Body.String())
	}
	var revoked map[string]string
	if err := json.Unmarshal(revokeRR.Body.Bytes(), &revoked); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if revoked["revoked_at"] == "" {
		t.Fatalf("revoked response missing revoked_at: %#v", revoked)
	}
	if revoked["token"] != "" {
		t.Fatalf("revoke response must not include raw token: %#v", revoked)
	}

	registerReq := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"token":"`+created["token"]+`","name":"desk","platform":"linux","agent_version":"0.1.0","fingerprint":"fp-revoked"}`))
	registerReq.Header.Set("Content-Type", "application/json")
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, registerReq)
	if registerRR.Code != http.StatusUnauthorized {
		t.Fatalf("register with revoked token status = %d body=%s", registerRR.Code, registerRR.Body.String())
	}
}

func TestRevokeAgentTokenRequiresLoginAndHandlesMissingToken(t *testing.T) {
	db := testutil.NewStore(t)
	handler := NewRouter(Deps{
		Store:    db,
		Sessions: auth.NewSessionManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour),
	})

	unauthReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/missing", nil)
	unauthRR := httptest.NewRecorder()
	handler.ServeHTTP(unauthRR, unauthReq)
	if unauthRR.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated revoke status = %d body=%s", unauthRR.Code, unauthRR.Body.String())
	}

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.CreateUser(context.Background(), store.User{ID: "user-1", Username: "admin", PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginRR := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRR, loginReq)

	missingReq := httptest.NewRequest(http.MethodDelete, "/api/agent-tokens/missing", nil)
	for _, cookie := range loginRR.Result().Cookies() {
		missingReq.AddCookie(cookie)
	}
	missingRR := httptest.NewRecorder()
	handler.ServeHTTP(missingRR, missingReq)
	if missingRR.Code != http.StatusNotFound {
		t.Fatalf("missing revoke status = %d body=%s", missingRR.Code, missingRR.Body.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run:

```sh
go test ./server/internal/httpapi -run 'TestAgentTokenRevokeFlow|TestRevokeAgentTokenRequiresLoginAndHandlesMissingToken'
```

Expected: FAIL，`DELETE /api/agent-tokens/{id}` 返回 404 或方法未处理。

- [ ] **Step 3: 写最小实现**

在 `server/internal/httpapi/router.go` 中更新路由：

```go
r.mux.HandleFunc("POST /api/agent-tokens", r.handleCreateAgentToken)
r.mux.HandleFunc("GET /api/agent-tokens", r.handleListAgentTokens)
r.mux.HandleFunc("DELETE /api/agent-tokens/", r.handleRevokeAgentToken)
```

在 `handleListAgentTokens` 附近新增统一响应类型和转换函数：

```go
type agentTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
	UsedAt    string `json:"used_at,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func agentTokenToResponse(token store.AgentToken) agentTokenResponse {
	resp := agentTokenResponse{
		ID:        token.ID,
		Name:      token.Name,
		ExpiresAt: token.ExpiresAt.Format(time.RFC3339),
		CreatedAt: token.CreatedAt.Format(time.RFC3339),
	}
	if token.UsedAt.Valid {
		resp.UsedAt = token.UsedAt.Time.Format(time.RFC3339)
	}
	if token.RevokedAt.Valid {
		resp.RevokedAt = token.RevokedAt.Time.Format(time.RFC3339)
	}
	return resp
}
```

把 `handleListAgentTokens` 内部局部 `tokenResponse` 替换为：

```go
out := make([]agentTokenResponse, 0, len(tokens))
for _, token := range tokens {
	out = append(out, agentTokenToResponse(token))
}
writeJSON(w, http.StatusOK, out)
```

新增 handler：

```go
func (r *router) handleRevokeAgentToken(w http.ResponseWriter, req *http.Request) {
	user, ok := r.requireUser(w, req)
	if !ok {
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/agent-tokens/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	token, err := r.store.RevokeAgentToken(req.Context(), id, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "agent token not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "failed to revoke token")
		return
	}
	_ = r.audit.Log(req.Context(), store.AuditEvent{
		UserID:    user.ID,
		EventType: "agent_token_revoked",
		Summary:   "agent registration token revoked",
	})
	writeJSON(w, http.StatusOK, agentTokenToResponse(token))
}
```

- [ ] **Step 4: 运行测试确认通过**

Run:

```sh
go test ./server/internal/httpapi -run 'TestAgentTokenRevokeFlow|TestRevokeAgentTokenRequiresLoginAndHandlesMissingToken'
```

Expected: PASS。

- [ ] **Step 5: 提交**

Run:

```sh
git add server/internal/httpapi/router.go server/internal/httpapi/router_test.go
git commit -m "feat: add agent token revoke api"
```

## Task 3: 前端 API 类型和调用

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step 1: 添加类型和 API 函数**

在 `web/src/api.ts` 中 `SessionOutputChunk` 后加入：

```ts
export type AgentToken = {
  id: string;
  name: string;
  expires_at: string;
  used_at?: string;
  revoked_at?: string;
  created_at: string;
};

export type CreatedAgentToken = AgentToken & {
  token: string;
};
```

在 `me()` 后加入：

```ts
export function listAgentTokens(): Promise<AgentToken[]> {
  return request<AgentToken[]>('/api/agent-tokens');
}

export function createAgentToken(name: string, ttlHours: number): Promise<CreatedAgentToken> {
  return request<CreatedAgentToken>('/api/agent-tokens', {
    method: 'POST',
    body: JSON.stringify({ name, ttl_hours: ttlHours }),
  });
}

export function revokeAgentToken(id: string): Promise<AgentToken> {
  return request<AgentToken>(`/api/agent-tokens/${id}`, {
    method: 'DELETE',
  });
}
```

- [ ] **Step 2: 类型检查**

Run:

```sh
cd web && npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

Run:

```sh
git add web/src/api.ts
git commit -m "feat: add agent token web api"
```

## Task 4: 前端 Token 管理组件

**Files:**
- Create: `web/src/components/AgentTokenManager.tsx`

- [ ] **Step 1: 创建组件**

创建 `web/src/components/AgentTokenManager.tsx`：

```tsx
import { Check, Clipboard, KeyRound, ShieldX, Trash2 } from 'lucide-react';
import { FormEvent, useEffect, useMemo, useState } from 'react';
import type { AgentToken, CreatedAgentToken } from '../api';

type TokenStatus = 'available' | 'used' | 'expired' | 'revoked';

function getTokenStatus(token: AgentToken, now = new Date()): TokenStatus {
  if (token.revoked_at) return 'revoked';
  if (token.used_at) return 'used';
  if (new Date(token.expires_at).getTime() <= now.getTime()) return 'expired';
  return 'available';
}

function formatDate(value?: string) {
  if (!value) return '-';
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

export function AgentTokenManager({
  tokens,
  loading,
  error,
  createdToken,
  onCreate,
  onRevoke,
  onRefresh,
}: {
  tokens: AgentToken[];
  loading: boolean;
  error: string | null;
  createdToken: CreatedAgentToken | null;
  onCreate: (name: string, ttlHours: number) => Promise<void>;
  onRevoke: (id: string) => Promise<void>;
  onRefresh: () => Promise<void>;
}) {
  const [name, setName] = useState('agent');
  const [ttlHours, setTtlHours] = useState(24);
  const [submitting, setSubmitting] = useState(false);
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const [pendingRevokeId, setPendingRevokeId] = useState<string | null>(null);
  const sortedTokens = useMemo(
    () => [...tokens].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()),
    [tokens]
  );

  useEffect(() => {
    onRefresh();
  }, [onRefresh]);

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    try {
      await onCreate(name.trim() || 'agent', ttlHours);
      setName('agent');
      setTtlHours(24);
      setCopyState('idle');
    } finally {
      setSubmitting(false);
    }
  }

  async function copyToken() {
    if (!createdToken) return;
    try {
      await navigator.clipboard.writeText(createdToken.token);
      setCopyState('copied');
    } catch {
      setCopyState('failed');
    }
  }

  return (
    <main className="tokenPage">
      <header className="tokenHeader">
        <div>
          <h1>Agent Tokens</h1>
          <p>Manage one-time registration tokens for new agents.</p>
        </div>
        <button type="button" className="secondaryButton" onClick={onRefresh} disabled={loading}>
          Refresh
        </button>
      </header>

      <section className="tokenPanel" aria-labelledby="create-token-title">
        <h2 id="create-token-title">Create token</h2>
        <form className="tokenForm" onSubmit={handleCreate}>
          <label>
            Name
            <input value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label>
            TTL hours
            <input
              type="number"
              min={1}
              step={1}
              value={ttlHours}
              onChange={(event) => setTtlHours(Math.max(1, Number(event.target.value) || 1))}
            />
          </label>
          <button type="submit" disabled={submitting}>
            <KeyRound size={16} aria-hidden="true" />
            Create
          </button>
        </form>
        {createdToken && (
          <div className="newToken" role="status">
            <span>New token</span>
            <code>{createdToken.token}</code>
            <button type="button" className="iconTextButton" onClick={copyToken}>
              {copyState === 'copied' ? <Check size={16} aria-hidden="true" /> : <Clipboard size={16} aria-hidden="true" />}
              {copyState === 'copied' ? 'Copied' : 'Copy'}
            </button>
            {copyState === 'failed' && <span className="error">Copy failed</span>}
          </div>
        )}
      </section>

      <section className="tokenPanel" aria-labelledby="token-list-title">
        <div className="panelTitleRow">
          <h2 id="token-list-title">Tokens</h2>
          {loading && <span className="muted">Loading...</span>}
        </div>
        {error && <p className="error">{error}</p>}
        {!loading && sortedTokens.length === 0 ? (
          <p className="emptyState">No agent tokens yet.</p>
        ) : (
          <div className="tokenTableWrap">
            <table className="tokenTable">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th>Expires</th>
                  <th>Used</th>
                  <th>Revoked</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {sortedTokens.map((token) => {
                  const status = getTokenStatus(token);
                  const confirming = pendingRevokeId === token.id;
                  return (
                    <tr key={token.id}>
                      <td>
                        <strong>{token.name}</strong>
                        <span className="tokenId">{token.id.slice(0, 8)}</span>
                      </td>
                      <td>
                        <span className={`tokenStatus tokenStatus-${status}`}>{status}</span>
                      </td>
                      <td>{formatDate(token.created_at)}</td>
                      <td>{formatDate(token.expires_at)}</td>
                      <td>{formatDate(token.used_at)}</td>
                      <td>{formatDate(token.revoked_at)}</td>
                      <td>
                        {status === 'revoked' ? (
                          <span className="muted">Revoked</span>
                        ) : confirming ? (
                          <button type="button" className="dangerButton" onClick={() => onRevoke(token.id)}>
                            <ShieldX size={16} aria-hidden="true" />
                            Confirm
                          </button>
                        ) : (
                          <button type="button" className="iconTextButton" onClick={() => setPendingRevokeId(token.id)}>
                            <Trash2 size={16} aria-hidden="true" />
                            Revoke
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </main>
  );
}
```

- [ ] **Step 2: 类型检查确认组件独立通过**

Run:

```sh
cd web && npm run build
```

Expected: 由于组件尚未接入，build 仍应 PASS；如果 `noUnusedLocals` 开启导致未引用文件不参与检查，继续 Task 5。

- [ ] **Step 3: 提交**

Run:

```sh
git add web/src/components/AgentTokenManager.tsx
git commit -m "feat: add agent token manager component"
```

## Task 5: 前端接入导航和数据流

**Files:**
- Modify: `web/src/App.tsx`

- [ ] **Step 1: 接入 API 和视图状态**

在 `web/src/App.tsx` 中更新 import：

```tsx
import { KeyRound, Monitor } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { AgentToken, CreatedAgentToken, Device, Session, User } from './api';
import * as api from './api';
import { AgentTokenManager } from './components/AgentTokenManager';
import { DeviceList } from './components/DeviceList';
import { LoginView } from './components/LoginView';
import { TerminalTabs } from './components/TerminalTabs';
```

新增类型：

```ts
type ViewMode = 'terminals' | 'agentTokens';
```

在 `App` 中新增状态：

```tsx
const [agentTokens, setAgentTokens] = useState<AgentToken[]>([]);
const [createdAgentToken, setCreatedAgentToken] = useState<CreatedAgentToken | null>(null);
const [tokenLoading, setTokenLoading] = useState(false);
const [tokenError, setTokenError] = useState<string | null>(null);
```

在 `!user` 分支的 effect 中同时清理 token 状态：

```tsx
setAgentTokens([]);
setCreatedAgentToken(null);
setTokenError(null);
```

新增 handler：

```tsx
const loadAgentTokens = useCallback(async () => {
  setTokenLoading(true);
  setTokenError(null);
  try {
    setAgentTokens(await api.listAgentTokens());
  } catch {
    setTokenError('Failed to load agent tokens.');
  } finally {
    setTokenLoading(false);
  }
}, []);

async function handleCreateAgentToken(name: string, ttlHours: number) {
  setTokenError(null);
  const created = await api.createAgentToken(name, ttlHours);
  setCreatedAgentToken(created);
  setAgentTokens((current) => [created, ...current.filter((token) => token.id !== created.id)]);
}

async function handleRevokeAgentToken(id: string) {
  setTokenError(null);
  const revoked = await api.revokeAgentToken(id);
  setAgentTokens((current) => current.map((token) => (token.id === id ? revoked : token)));
}
```

给 `AppView` 传入新 props：

```tsx
agentTokens={agentTokens}
createdAgentToken={createdAgentToken}
tokenLoading={tokenLoading}
tokenError={tokenError}
onCreateAgentToken={handleCreateAgentToken}
onRevokeAgentToken={handleRevokeAgentToken}
onRefreshAgentTokens={loadAgentTokens}
```

在 `AppView` props 中加入对应字段，并在函数内新增：

```tsx
const [viewMode, setViewMode] = useState<ViewMode>('terminals');
```

登录后 return 改为：

```tsx
return (
  <div className="shell">
    <aside className="devices">
      <nav className="sideNav" aria-label="Primary">
        <button className={viewMode === 'terminals' ? 'active' : ''} onClick={() => setViewMode('terminals')}>
          <Monitor size={16} aria-hidden="true" />
          Terminals
        </button>
        <button className={viewMode === 'agentTokens' ? 'active' : ''} onClick={() => setViewMode('agentTokens')}>
          <KeyRound size={16} aria-hidden="true" />
          Agent Tokens
        </button>
      </nav>
      <DeviceList devices={devices} onCreateSession={createAndAppend} compact />
    </aside>
    {viewMode === 'terminals' ? (
      <TerminalTabs
        sessions={localSessions}
        onSessionsChange={setLocalSessions}
        onCloseSession={onCloseSession}
        onRenameSession={onRenameSession}
      />
    ) : (
      <AgentTokenManager
        tokens={agentTokens}
        loading={tokenLoading}
        error={tokenError}
        createdToken={createdAgentToken}
        onCreate={onCreateAgentToken}
        onRevoke={onRevokeAgentToken}
        onRefresh={onRefreshAgentTokens}
      />
    )}
  </div>
);
```

同时修改 `DeviceList` 支持在外层 aside 内渲染，避免嵌套 aside。将组件根元素改为 `section`，新增可选 `compact` prop：

```tsx
import { Terminal } from 'lucide-react';
import type { Device } from '../api';

export function DeviceList({
  devices,
  onCreateSession,
  compact = false,
}: {
  devices: Device[];
  onCreateSession: (deviceId: string) => Promise<void>;
  compact?: boolean;
}) {
  return (
    <section className={compact ? 'devicesPanel compact' : 'devices'}>
      <h2>Devices</h2>
      {devices.map((device) => (
        <section key={device.id} className="deviceRow">
          <div>
            <strong>{device.name}</strong>
            <span>{device.platform}</span>
          </div>
          <span className={device.online ? 'online' : 'offline'}>{device.online ? 'online' : 'offline'}</span>
          <button disabled={!device.online} onClick={() => onCreateSession(device.id)}>
            <Terminal size={16} aria-hidden="true" />
            New terminal
          </button>
        </section>
      ))}
    </section>
  );
}
```

- [ ] **Step 2: 类型检查**

Run:

```sh
cd web && npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

Run:

```sh
git add web/src/App.tsx web/src/components/DeviceList.tsx
git commit -m "feat: wire agent token management view"
```

## Task 6: 前端样式

**Files:**
- Modify: `web/src/styles.css`

- [ ] **Step 1: 更新侧栏和按钮选择器**

把旧选择器中的 `.deviceRow button` 保留，同时加入 token 页面按钮：

```css
.loginForm button,
.deviceRow button,
.tabs button,
.sideNav button,
.tokenForm button,
.secondaryButton,
.iconTextButton,
.dangerButton {
  min-height: 34px;
  border-radius: 6px;
  border: 1px solid #46515f;
  background: #253244;
  color: #f4f5f7;
  padding: 0 12px;
}
```

把 `.devices` 定义扩展为侧栏容器：

```css
.devices {
  border-right: 1px solid #282d35;
  padding: 16px;
  background: #15181d;
  overflow-y: auto;
}

.devicesPanel {
  display: grid;
  gap: 0;
}

.devicesPanel.compact {
  margin-top: 18px;
}
```

新增样式：

```css
.sideNav {
  display: grid;
  gap: 8px;
  padding-bottom: 16px;
  border-bottom: 1px solid #282d35;
}

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

.sideNav button {
  justify-content: flex-start;
}

.sideNav button.active {
  background: #37516f;
  border-color: #5b7696;
}

.tokenPage {
  min-width: 0;
  display: grid;
  align-content: start;
  gap: 16px;
  padding: 20px;
  overflow: auto;
}

.tokenHeader,
.panelTitleRow {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: space-between;
}

.tokenHeader h1,
.tokenPanel h2 {
  margin: 0;
}

.tokenHeader p,
.muted,
.emptyState {
  color: #a8b0bd;
}

.tokenPanel {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid #30343a;
  border-radius: 8px;
  background: #181b20;
}

.tokenForm {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(120px, 180px) auto;
  gap: 12px;
  align-items: end;
}

.tokenForm label {
  display: grid;
  gap: 6px;
  color: #b9c0cc;
}

.tokenForm input {
  min-height: 38px;
  border-radius: 6px;
  border: 1px solid #3d444f;
  background: #0f1115;
  color: #f4f5f7;
  padding: 0 10px;
}

.newToken {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  gap: 10px;
  align-items: center;
  padding: 12px;
  border: 1px solid #2f9d64;
  border-radius: 8px;
  background: rgba(47, 157, 100, 0.12);
}

.newToken code {
  min-width: 0;
  overflow-wrap: anywhere;
  color: #d6ffe5;
}

.tokenTableWrap {
  overflow-x: auto;
}

.tokenTable {
  width: 100%;
  border-collapse: collapse;
  min-width: 760px;
}

.tokenTable th,
.tokenTable td {
  padding: 10px;
  border-bottom: 1px solid #282d35;
  text-align: left;
  vertical-align: middle;
}

.tokenTable th {
  color: #b9c0cc;
  font-weight: 600;
}

.tokenTable td:first-child {
  display: grid;
  gap: 3px;
}

.tokenId {
  color: #7d8794;
  font-size: 12px;
}

.tokenStatus {
  display: inline-flex;
  align-items: center;
  min-height: 22px;
  border-radius: 999px;
  padding: 0 8px;
  border: 1px solid transparent;
  line-height: 1;
}

.tokenStatus-available {
  border-color: #2f9d64;
  background: rgba(47, 157, 100, 0.14);
  color: #67d391;
}

.tokenStatus-used {
  border-color: #3d78bd;
  background: rgba(61, 120, 189, 0.16);
  color: #74b4ff;
}

.tokenStatus-expired {
  border-color: #697484;
  background: rgba(105, 116, 132, 0.18);
  color: #b7beca;
}

.tokenStatus-revoked,
.dangerButton {
  border-color: #c95b44;
  background: rgba(201, 91, 68, 0.17);
  color: #ff9b73;
}

@media (max-width: 820px) {
  .shell {
    grid-template-columns: 1fr;
  }

  .devices {
    border-right: 0;
    border-bottom: 1px solid #282d35;
  }

  .tokenForm,
  .newToken {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 2: 构建验证 CSS 无语法问题**

Run:

```sh
cd web && npm run build
```

Expected: PASS。

- [ ] **Step 3: 提交**

Run:

```sh
git add web/src/styles.css
git commit -m "style: add agent token management layout"
```

## Task 7: 前端交互测试

**Files:**
- Modify: `web/src/test/App.test.tsx`

- [ ] **Step 1: 扩展 mock**

在 `web/src/test/App.test.tsx` 的 API mock 返回对象中加入：

```ts
createAgentToken: vi.fn(),
listAgentTokens: vi.fn(),
revokeAgentToken: vi.fn(),
```

- [ ] **Step 2: 更新现有 `AppView` 渲染调用**

文件内所有现有 `AppView` 测试渲染都需要补充这些 props：

```tsx
agentTokens={[]}
createdAgentToken={null}
tokenLoading={false}
tokenError={null}
onCreateAgentToken={vi.fn()}
onRevokeAgentToken={vi.fn()}
onRefreshAgentTokens={vi.fn()}
```

如果测试中需要异步刷新行为，`onRefreshAgentTokens` 使用 `vi.fn().mockResolvedValue(undefined)`。

- [ ] **Step 3: 写视图切换和列表测试**

在 `describe('AppView')` 测试组内追加：

```tsx
it('shows agent tokens after switching views', async () => {
  const refresh = vi.fn().mockResolvedValue(undefined);
  render(
    <AppView
      user={{ id: 'user-1', username: 'admin' }}
      devices={[]}
      sessions={{}}
      agentTokens={[
        {
          id: 'tok-available-123',
          name: 'laptop',
          created_at: new Date().toISOString(),
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        },
      ]}
      createdAgentToken={null}
      tokenLoading={false}
      tokenError={null}
      onLogin={vi.fn()}
      onCloseSession={vi.fn()}
      onCreateSession={vi.fn()}
      onRenameSession={vi.fn()}
      onCreateAgentToken={vi.fn()}
      onRevokeAgentToken={vi.fn()}
      onRefreshAgentTokens={refresh}
    />
  );

  await userEvent.click(screen.getByRole('button', { name: /agent tokens/i }));

  expect(screen.getByRole('heading', { name: /agent tokens/i })).toBeInTheDocument();
  expect(screen.getByText('laptop')).toBeInTheDocument();
  expect(screen.getByText('available')).toBeInTheDocument();
  expect(refresh).toHaveBeenCalled();
});
```

- [ ] **Step 4: 写创建和吊销测试**

继续追加：

```tsx
it('creates and revokes agent tokens from the management view', async () => {
  const createToken = vi.fn().mockResolvedValue(undefined);
  const revokeToken = vi.fn().mockResolvedValue(undefined);
  render(
    <AppView
      user={{ id: 'user-1', username: 'admin' }}
      devices={[]}
      sessions={{}}
      agentTokens={[
        {
          id: 'tok-1',
          name: 'desk',
          created_at: new Date().toISOString(),
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        },
      ]}
      createdAgentToken={{
        id: 'tok-new',
        name: 'desk',
        token: 'raw-token-once',
        created_at: new Date().toISOString(),
        expires_at: new Date(Date.now() + 60_000).toISOString(),
      }}
      tokenLoading={false}
      tokenError={null}
      onLogin={vi.fn()}
      onCloseSession={vi.fn()}
      onCreateSession={vi.fn()}
      onRenameSession={vi.fn()}
      onCreateAgentToken={createToken}
      onRevokeAgentToken={revokeToken}
      onRefreshAgentTokens={vi.fn().mockResolvedValue(undefined)}
    />
  );

  await userEvent.click(screen.getByRole('button', { name: /agent tokens/i }));
  await userEvent.clear(screen.getByLabelText(/name/i));
  await userEvent.type(screen.getByLabelText(/name/i), 'rack');
  await userEvent.clear(screen.getByLabelText(/ttl hours/i));
  await userEvent.type(screen.getByLabelText(/ttl hours/i), '12');
  await userEvent.click(screen.getByRole('button', { name: /create/i }));

  expect(createToken).toHaveBeenCalledWith('rack', 12);
  expect(screen.getByText('raw-token-once')).toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: /revoke/i }));
  expect(revokeToken).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: /confirm/i }));
  expect(revokeToken).toHaveBeenCalledWith('tok-1');
});
```

- [ ] **Step 5: 运行测试确认通过**

Run:

```sh
cd web && npm test -- --run src/test/App.test.tsx
```

Expected: PASS。

- [ ] **Step 6: 提交**

Run:

```sh
git add web/src/test/App.test.tsx
git commit -m "test: cover agent token management ui"
```

## Task 8: 全量验证

**Files:**
- No code changes expected.

- [ ] **Step 1: 运行后端测试**

Run:

```sh
go test ./...
```

Expected: PASS。

- [ ] **Step 2: 运行前端测试**

Run:

```sh
cd web && npm test -- --run
```

Expected: PASS。

- [ ] **Step 3: 运行前端构建**

Run:

```sh
cd web && npm run build
```

Expected: PASS。

- [ ] **Step 4: 检查工作区**

Run:

```sh
git status --short
```

Expected: 没有未提交的实现改动。
