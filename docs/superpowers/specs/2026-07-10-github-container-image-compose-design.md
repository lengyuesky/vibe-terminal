# Docker Compose 使用 GitHub 构建镜像设计

## 目标

将默认 Docker Compose 部署方式从本地构建镜像改为直接拉取 GitHub Actions 发布到 GitHub Container Registry 的镜像，使部署机器无需安装项目构建工具，也无需下载完整源码后执行镜像构建。

## 方案

`docker-compose.yml` 的 `server` 服务固定使用以下镜像：

```text
ghcr.io/lengyuesky/vibe-terminal:latest
```

删除现有 `build` 配置，不提供环境变量覆盖镜像标签，也不保留本地构建作为 Compose 的后备路径。`server/Dockerfile` 继续保留，供 GitHub Actions 构建并发布镜像。

现有端口、环境变量、配置文件挂载、数据卷和重启策略保持不变。

## 部署流程

README 中的 Docker Compose 部署命令调整为：

```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml，至少替换 session_secret 和 admin_password
docker compose pull
docker compose up -d
```

`docker compose pull` 明确获取最新镜像，`docker compose up -d` 使用已拉取的 `latest` 镜像启动或更新服务，不再使用 `--build`。

## 错误处理

镜像仓库不可达、镜像不存在或无权访问时，由 `docker compose pull` 直接返回错误并停止部署。配置文件缺失时，现有只读绑定挂载及 `create_host_path: false` 设置继续阻止容器以错误配置启动。

## 验证

运行 `docker compose config`，确认 Compose 文件语法有效，并确认渲染结果中的服务镜像为 `ghcr.io/lengyuesky/vibe-terminal:latest`，且不再包含本地构建配置。

检查 README，确认部署说明不再要求执行 `docker compose up -d --build`。

## 非目标

- 不增加可配置镜像标签的环境变量。
- 不新增生产环境专用 Compose 文件。
- 不修改 GitHub Actions 镜像构建与发布流程。
- 不删除 `server/Dockerfile`。
