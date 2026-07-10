# Web 登录可选 TOTP 2FA 设计

**日期：** 2026-07-10

## 目标

为现有单管理员网页登录增加可选的 TOTP 两步验证（2FA）。2FA 默认关闭，管理员可以在已登录状态下自行启用或关闭。启用后，新的 Web 登录必须依次通过密码验证和 TOTP/恢复码验证，第二步成功前不得签发正式会话 Cookie。

## 已确认的产品范围

- 使用 RFC 6238 TOTP，兼容 Google Authenticator、Microsoft Authenticator、1Password 等验证器。
- 2FA 是可选功能，升级后不会强制已有管理员配置。
- 每次正式会话失效或退出登录后，重新登录都必须再次完成第二步验证。
- 不提供“信任此设备”或长期免输验证码功能。
- 启用 2FA 时生成 10 个一次性恢复码，恢复码仅明文展示一次。
- 管理员可以重新生成恢复码；重新生成后旧恢复码立即全部作废。
- 不增加邮件、短信、WebAuthn 或第三方身份提供商。
- 继续使用现有 24 小时签名 Cookie 会话机制。

## 当前系统背景

当前服务端使用 Go、SQLite 和 `bcrypt`：`POST /api/login` 校验用户名、密码后立即设置 `vibe_session` Cookie。Cookie 使用 HMAC 签名，设置了 `HttpOnly`、`Secure` 和 `SameSite=Lax`。React 前端只有单步用户名/密码登录页，管理员登录后可访问终端和 agent token 管理页面。

本设计保留未启用 2FA 时的现有登录行为，避免破坏已有部署和数据。

## 方案选择

采用“短期预认证挑战令牌”方案：

1. 密码验证成功且用户已启用 2FA 时，服务端返回一个 5 分钟有效的签名挑战令牌，但不设置正式会话 Cookie。
2. 前端使用挑战令牌提交 TOTP 或恢复码。
3. 第二步验证成功后，服务端才签发正式 `vibe_session` Cookie。

未采用以下方案：

- 受限待验证 Cookie：需要维护两类 Cookie 和额外的权限状态，复杂度更高。
- 第二步重复提交用户名和密码：需要再次传输密码，接口职责也不清晰。

## 架构与组件边界

### 服务端认证组件

在 `server/internal/auth` 中增加职责单一的组件：

- TOTP 服务：生成随机密钥、生成 `otpauth://` URI、验证 6 位验证码并返回匹配的时间计数器。
- 2FA 密钥加密器：使用 AES-256-GCM 加密和解密 TOTP 密钥。
- 恢复码服务：生成适合人工保存和输入的高熵恢复码，并计算不可逆校验值。
- 登录挑战管理器：签发和验证短期预认证挑战令牌。
- 登录限流器：分别限制密码步骤和第二因素步骤的连续失败请求。

这些组件不依赖 HTTP，并通过单元测试独立验证。HTTP handler 负责输入输出、权限检查和审计，SQLite store 负责持久化及原子消费。

### HTTP 接口组件

现有 `router.go` 已承担较多职责。2FA 管理接口和相关响应类型放入独立的 `router_2fa.go`，登录入口保留在现有路由中，但调用新的认证组件。这样不进行无关重构，同时避免继续扩大单文件认证逻辑。

### Web 组件

- `LoginView` 支持“密码步骤”和“第二因素步骤”两种状态。
- 新增 `SecurityView`，负责显示状态、扫码设置、恢复码展示、恢复码重置和关闭 2FA。
- `App` 侧边栏新增 `Security` 入口，并协调登录 API 的联合响应。

## 登录流程与 API

### 第一步：用户名和密码

`POST /api/login`

请求保持不变：

```json
{
  "username": "admin",
  "password": "example-password"
}
```

当 2FA 未启用时：

- 返回 `200 OK` 和现有用户 JSON。
- 按现有行为设置正式 `vibe_session` Cookie。
- 记录成功登录审计事件。

当 2FA 已启用时：

- 返回 `202 Accepted`。
- 不设置任何会话 Cookie。
- 响应中包含 5 分钟有效的挑战令牌：

```json
{
  "two_factor_required": true,
  "challenge_token": "signed-token",
  "expires_in": 300
}
```

用户名不存在和密码错误统一返回 `401 invalid_credentials`，响应不能泄露用户名是否存在或该用户是否启用了 2FA。

### 第二步：TOTP 或恢复码

`POST /api/login/2fa`

```json
{
  "challenge_token": "signed-token",
  "code": "123456"
}
```

`code` 可以是 6 位 TOTP，也可以是带或不带分隔符的恢复码。服务端根据规范化后的格式选择验证方式。

验证成功后：

- 设置正式 `vibe_session` Cookie。
- 返回 `200 OK` 和现有用户 JSON。
- TOTP 登录原子更新最后使用的时间计数器。
- 恢复码登录原子标记对应恢复码已使用。
- 记录登录方式为 TOTP 或恢复码，但不记录验证码内容。

验证失败、挑战过期、挑战被篡改、2FA 已被关闭或重新配置时均不得创建 Cookie。挑战无效或过期时前端返回密码步骤，要求重新开始登录。

## 挑战令牌设计

挑战令牌包含：

- 用户 ID；
- 当前已启用 2FA 配置的随机配置 ID；
- 签发时间；
- 过期时间；
- 令牌版本。

载荷经过 URL-safe Base64 编码，并使用从 `session_secret` 派生的独立 HMAC-SHA256 密钥签名。派生上下文与会话 Cookie、TOTP 密钥加密用途不同，防止不同用途之间复用同一个子密钥。

挑战令牌有效期固定为 5 分钟。它不代表已登录状态，不能访问任何需要管理员会话的接口。第二步验证时必须比较令牌和数据库中的配置 ID；关闭并重新启用 2FA 会生成新的配置 ID，因此旧挑战立即失效。TOTP 时间计数器防重放和恢复码原子消费用于阻止同一个第二因素凭据重复完成挑战。

## TOTP 规则

- 算法：HMAC-SHA1，这是主流验证器支持最广泛的 RFC 6238 默认算法。
- 密钥：使用 `crypto/rand` 生成 20 个随机字节，再编码为 Base32。
- 位数：6 位。
- 周期：30 秒。
- 容忍窗口：当前时间窗口前后各一个窗口。
- 服务端必须使用 UTC 时间计算计数器。
- 每个用户保存最后成功使用的 TOTP 计数器；小于或等于该值的验证码即使数学上有效也拒绝使用。
- 更新最后计数器时使用条件更新或事务，确保并发请求只有一个能够成功。

服务器时钟偏差会导致 TOTP 失败，因此部署文档应明确要求启用 NTP 或其他可靠的系统时间同步。

## 启用、管理与关闭流程

所有管理接口都要求已有的正式管理员会话。

### 查询状态

`GET /api/security/2fa`

返回：

```json
{
  "enabled": true,
  "recovery_codes_remaining": 8
}
```

响应不包含 TOTP 密钥、加密密文或恢复码哈希。

### 开始设置

`POST /api/security/2fa/setup`

```json
{
  "password": "example-password"
}
```

服务端重新验证当前管理员密码，生成随机 TOTP 密钥，以待确认状态加密保存 10 分钟，并返回：

```json
{
  "otpauth_uri": "otpauth://totp/...",
  "manual_key": "BASE32SECRET",
  "expires_at": "2026-07-10T12:10:00Z"
}
```

`otpauth://` URI 使用 `vibe-terminal` 作为 issuer，当前用户名作为 account name。重复开始设置会覆盖尚未确认的旧设置。已经启用 2FA 时不能再次开始设置，必须先关闭。

### 确认启用

`POST /api/security/2fa/enable`

```json
{
  "code": "123456"
}
```

服务端仅使用尚未过期的待确认密钥验证验证码。成功后在同一事务中：

- 标记 2FA 已启用；
- 保存首次使用的 TOTP 计数器，防止设置验证码被立即用于登录；
- 生成并保存 10 个恢复码校验值；
- 清除待确认过期时间。

响应返回 10 个恢复码明文，且此后任何状态查询都不能再次获得这些明文。

### 重新生成恢复码

`POST /api/security/2fa/recovery-codes`

请求同时包含当前密码和当前 TOTP。成功后在一个事务中使所有旧恢复码失效并生成 10 个新恢复码，仅在本次响应中返回明文。恢复码不能用于执行重新生成操作；丢失验证器但仍有恢复码时，管理员可以先用恢复码登录，再通过密码关闭并重新启用 2FA。

### 关闭 2FA

`POST /api/security/2fa/disable`

请求包含当前密码。成功后删除该用户的 TOTP 设置和全部恢复码。关闭操作依赖正式登录会话和密码再认证，并写入审计日志。

## 恢复码设计

- 每次生成 10 个恢复码。
- 每个恢复码使用 10 个随机字节，编码成 16 个 Base32 字符，展示格式为 `XXXX-XXXX-XXXX-XXXX`；该字符集不包含容易与字母混淆的 `0` 和 `1`。
- 输入比较前忽略大小写、空格和连字符。
- 数据库只保存带服务端派生密钥的 HMAC-SHA256 校验值，不保存明文。
- 使用恢复码时通过事务条件更新 `used_at`，保证并发请求不能重复消费同一个恢复码。
- UI 仅在启用或重新生成的成功响应后展示明文，并提供复制和下载功能。

## 密钥保护

TOTP 密钥不能明文写入 SQLite。服务端使用 AES-256-GCM：

- 根材料使用现有强随机 `session_secret`。
- 通过 HKDF-SHA256 和固定用途字符串派生独立的 2FA 加密密钥。
- 每次加密生成新的随机 nonce。
- 数据库存储带版本、nonce 和密文的编码值，以便未来升级格式。
- 解密或认证标签校验失败时按服务端错误处理，绝不能跳过 2FA。

恢复码 HMAC 和登录挑战签名分别使用不同的 HKDF 用途字符串派生子密钥。

由于 2FA 数据依赖 `session_secret`，启用 2FA 的部署必须持久、安全地保存该配置。直接更换或丢失 `session_secret` 会导致已有 TOTP 密钥无法解密；部署文档必须显著说明这一约束，并要求在受控迁移中先关闭或重新配置 2FA。

## 数据模型与迁移

新增 `user_two_factor` 表：

```sql
create table if not exists user_two_factor (
    user_id text primary key references users(id) on delete cascade,
    configuration_id text not null,
    secret_ciphertext text not null,
    setup_expires_at datetime,
    enabled_at datetime,
    last_totp_counter integer,
    created_at datetime not null,
    updated_at datetime not null
)
```

新增 `two_factor_recovery_codes` 表：

```sql
create table if not exists two_factor_recovery_codes (
    id text primary key,
    user_id text not null references users(id) on delete cascade,
    code_hash text not null,
    used_at datetime,
    created_at datetime not null,
    unique(user_id, code_hash)
)
```

现有迁移方式使用幂等的 `create table if not exists`，新增独立表不要求修改已有 `users` 表。旧数据库启动后自动创建新表；没有对应 2FA 记录的用户视为未启用。

Store 层提供以下原子操作：

- 保存或覆盖待确认设置；
- 读取已启用或待确认设置；
- 启用 2FA 并替换恢复码；
- 条件消费 TOTP 计数器；
- 条件消费恢复码；
- 查询剩余恢复码数量；
- 替换全部恢复码；
- 删除 2FA 设置和恢复码。

## 登录限流

当前项目是单进程服务，首版使用进程内限流器，不增加外部缓存依赖：

- 密码步骤按“规范化用户名 + 来源 IP”计数：10 分钟内最多 5 次失败，随后锁定 15 分钟。
- 第二因素步骤按“用户 ID + 来源 IP”计数：5 分钟内最多 5 次失败，随后锁定 15 分钟。
- 成功完成对应步骤后清除该步骤的失败计数。
- 返回 `429 Too Many Requests` 并设置 `Retry-After`。
- 两类限流状态合计最多保存 10000 个键；达到上限时优先清除已过期条目，再淘汰最早访问的条目，避免攻击者通过大量随机用户名造成无界内存增长。

服务重启会清空限流状态。这一限制适合当前单实例 MVP；若未来支持多实例部署，应替换为共享存储限流。

反向代理部署时，只有在显式配置可信代理后才能使用转发头识别来源 IP；默认使用直接连接地址，避免攻击者伪造 `X-Forwarded-For` 绕过限流。

## 错误处理

- 用户名不存在或密码错误：`401 invalid_credentials`。
- TOTP 或恢复码错误：`401 invalid_two_factor_code`，不区分具体类型。
- 挑战过期、被篡改或状态不再匹配：`401 login_restart_required`。
- 设置会话过期：`409 two_factor_setup_expired`。
- 已启用时重复设置或未启用时执行管理操作：`409 two_factor_state_conflict`。
- 限流：`429 too_many_attempts`，包含 `Retry-After`。
- TOTP 密钥无法解密或持久化失败：`500 two_factor_unavailable`，服务端记录详细原因，客户端不获得敏感细节。

所有失败路径都不得创建或保留正式会话 Cookie。

## 审计

新增或细化以下审计事件：

- `two_factor_enabled`；
- `two_factor_disabled`；
- `two_factor_recovery_codes_regenerated`；
- `login`，元数据只记录使用 `password`、`totp` 或 `recovery_code` 完成登录；
- 达到限流阈值时记录安全事件，避免为每次错误输入产生无限审计数据。

审计元数据不得包含密码、挑战令牌、TOTP 密钥、验证码或恢复码。

## Web 界面

### 登录页

密码步骤保持现有视觉风格。收到 `202 Accepted` 后切换到第二因素步骤：

- 默认显示 6 位数字验证码输入框；
- 提供“使用恢复码”切换入口；
- 支持提交、加载状态和统一错误提示；
- 提供“返回重新登录”，清除内存中的挑战令牌和验证码；
- 挑战过期或服务端要求重新登录时自动回到密码步骤。

挑战令牌只保存在 React 内存状态，不写入 Local Storage 或 Session Storage。

### Security 页面

侧边栏新增 `Security` 页面：

- 未启用：显示功能说明和启用按钮；启用流程要求输入密码、展示二维码和手动密钥、输入首个验证码。
- 启用成功：展示 10 个恢复码，提供复制和下载，并明确提示只显示一次。
- 已启用：显示启用状态和剩余恢复码数量，提供重新生成和关闭操作。
- 重新生成：要求输入密码和当前 TOTP，成功后展示新恢复码。
- 关闭：要求重新输入密码并进行明确确认。

二维码由前端使用 `qrcode.react` 根据服务端返回的 `otpauth_uri` 生成；手动密钥始终作为无二维码环境的备用方式。

## 安全注意事项

- 2FA 不能替代 HTTPS；Cookie 和认证请求仍必须只通过 HTTPS 传输。
- 2FA 不能替代强密码、网络访问控制和终端执行隔离。
- 正式 Cookie 只能在完成全部所需因素后签发。
- TOTP 密钥、恢复码明文和挑战令牌不得写入日志。
- 设置和关闭操作需要密码再认证，降低已登录浏览器被短暂接管后的风险。
- 使用 JSON 请求、现有 `SameSite=Lax` Cookie 以及同源部署模型；2FA 管理 handler 同时拒绝非 JSON 请求。
- 所有随机密钥、nonce、挑战签名材料和恢复码均使用 `crypto/rand`。

## 测试策略

### Go 单元测试

- TOTP 生成与验证、前后一个时间窗口、错误格式和错误验证码。
- 已使用时间计数器不能重放，并发消费只有一个成功。
- AES-GCM 往返、随机 nonce、密文篡改和错误密钥失败。
- 恢复码生成、规范化、校验和单次消费。
- 挑战令牌往返、过期、篡改和错误版本。
- 密码步骤与第二因素步骤的限流、成功清除和条目过期。

### Store 测试

- 旧数据库迁移后创建两个新表。
- 待确认设置覆盖与过期行为。
- 启用事务同时写入恢复码和首次 TOTP 计数器。
- TOTP 计数器条件更新防重放。
- 恢复码并发消费、剩余数量和整体替换。
- 关闭 2FA 后设置与恢复码全部删除。

### HTTP 集成测试

- 未启用 2FA 时保留现有登录、`/api/me` 和 Cookie 行为。
- 已启用时密码正确只返回挑战，不返回 Cookie。
- 有效 TOTP 完成登录；错误、过期、篡改挑战均失败。
- 同一 TOTP 不能重复完成登录。
- 恢复码可以完成一次登录，第二次使用失败。
- 设置、确认、状态查询、重新生成和关闭完整流程。
- 密码错误、第二因素错误和限流响应不泄露敏感状态。

### React 测试

- 登录页从密码步骤切换到 TOTP 步骤。
- TOTP 和恢复码两种提交模式。
- 挑战过期后返回密码步骤。
- Security 页面二维码设置、手动密钥、首个验证码确认。
- 恢复码仅在启用或重新生成响应后展示。
- 状态和剩余恢复码数量正确显示。
- 关闭 2FA 的密码确认与错误处理。

### 完整验证

实现完成后运行：

```bash
make test
```

该命令必须覆盖 Go 测试、Rust agent 回归测试、React/Vitest、TypeScript/Vite 构建和 Docker Compose 配置检查。

## 文档更新

README 和配置说明需要补充：

- 如何从 Security 页面启用 2FA；
- 推荐的验证器应用；
- 恢复码必须离线安全保存；
- 服务器必须保持准确时间；
- `session_secret` 必须使用强随机值并稳定持久化；
- 丢失验证器时使用恢复码登录，再关闭并重新配置 2FA；
- 2FA 不替代 HTTPS 和其他部署安全措施。

## 成功标准

- 默认升级路径不改变未启用 2FA 用户的登录行为。
- 启用 2FA 后，仅密码不能获取正式会话 Cookie。
- 有效 TOTP 或未使用的恢复码可以完成登录。
- TOTP 时间计数器和恢复码均不能重放。
- TOTP 密钥和恢复码明文不落库、不写日志。
- 管理员能够在 Web UI 中完成启用、查看状态、重新生成恢复码和关闭操作。
- 登录失败具有限流和不泄露账户状态的统一错误处理。
- 全量测试和构建通过。
