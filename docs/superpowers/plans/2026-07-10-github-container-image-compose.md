# GitHub 构建镜像 Compose 部署 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Docker Compose 默认部署方式改为固定拉取 `ghcr.io/lengyuesky/vibe-terminal:latest`，并同步更新部署文档。

**Architecture:** `docker-compose.yml` 只负责声明已发布的 GHCR 镜像和运行时配置，不再包含本地构建指令。GitHub Actions 继续使用 `server/Dockerfile` 构建镜像，部署者先拉取 `latest`，再启动 Compose 服务。

**Tech Stack:** Docker Compose、GitHub Container Registry、Markdown

---

## 文件职责

- 修改 `docker-compose.yml`：声明服务使用固定的 GHCR `latest` 镜像，保留现有运行时配置。
- 修改 `README.md`：记录拉取镜像并启动服务的部署命令。
- 不修改 `server/Dockerfile` 和 `.github/workflows/release.yml`：现有 GitHub 镜像构建发布流程保持不变。

### Task 1: 将 Compose 服务切换到 GHCR 镜像

**Files:**
- Modify: `docker-compose.yml:2-6`

- [ ] **Step 1: 运行目标结构检查并确认当前配置不满足要求**

Run:

```bash
rendered="$(docker compose config)" && \
printf '%s\n' "$rendered" | rg -q '^    image: ghcr\.io/lengyuesky/vibe-terminal:latest$' && \
! printf '%s\n' "$rendered" | rg -q '^    build:'
```

Expected: 命令返回非零状态，因为当前渲染结果包含 `build`，且没有目标 `image`。

- [ ] **Step 2: 用固定镜像替换本地构建配置**

将 `docker-compose.yml` 开头改为：

```yaml
services:
  server:
    image: ghcr.io/lengyuesky/vibe-terminal:latest
    environment:
      VIBE_CONFIG: "/app/config.yaml"
```

保持 `volumes`、`ports`、`restart` 和顶层卷声明不变。

- [ ] **Step 3: 验证 Compose 语法和渲染结构**

Run:

```bash
make docker-config
rendered="$(docker compose config)" && \
printf '%s\n' "$rendered" | rg -q '^    image: ghcr\.io/lengyuesky/vibe-terminal:latest$' && \
! printf '%s\n' "$rendered" | rg -q '^    build:'
```

Expected: `make docker-config` 成功，后续结构检查返回零状态。

- [ ] **Step 4: 提交 Compose 修改**

```bash
git add docker-compose.yml
git commit -m "deploy: use ghcr image in compose"
```

### Task 2: 更新 Docker Compose 部署文档

**Files:**
- Modify: `README.md:210-216`

- [ ] **Step 1: 运行部署命令检查并确认当前文档不满足要求**

Run:

```bash
rg -q '^docker compose pull$' README.md && \
rg -q '^docker compose up -d$' README.md && \
! rg -q '^docker compose up -d --build$' README.md
```

Expected: 命令返回非零状态，因为当前 README 没有 `docker compose pull`，并仍使用 `--build`。

- [ ] **Step 2: 更新部署命令**

将 Docker Compose 部署代码块改为：

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml，至少替换 session_secret 和 admin_password
docker compose pull
docker compose up -d
```

- [ ] **Step 3: 验证 README 中的部署命令**

Run:

```bash
rg -q '^docker compose pull$' README.md && \
rg -q '^docker compose up -d$' README.md && \
! rg -q '^docker compose up -d --build$' README.md
```

Expected: 命令返回零状态。

- [ ] **Step 4: 提交文档修改**

```bash
git add README.md
git commit -m "docs: deploy compose from ghcr"
```

### Task 3: 完成整体校验

**Files:**
- Verify: `docker-compose.yml`
- Verify: `README.md`

- [ ] **Step 1: 运行全部相关验证**

Run:

```bash
make docker-config
rendered="$(docker compose config)" && \
printf '%s\n' "$rendered" | rg -q '^    image: ghcr\.io/lengyuesky/vibe-terminal:latest$' && \
! printf '%s\n' "$rendered" | rg -q '^    build:' && \
rg -q '^docker compose pull$' README.md && \
rg -q '^docker compose up -d$' README.md && \
! rg -q '^docker compose up -d --build$' README.md
git diff --check
git status --short
```

Expected: Compose 与 README 检查全部成功，`git diff --check` 没有输出，`git status --short` 没有未提交修改。
