# Docker 部署

镜像：`ghcr.io/318182456/denova`，仅提供 Linux amd64。标签与上游 Release 一致（如 `v0.5.0`），`latest` 指向上游最新 Release。

## 启动

将仓库根目录的 `compose.yaml` 和 `docker/compose.env.example` 下载到同一目录，将后者重命名为 `.env`。修改 `.env` 的 `DENOVA_PASSWORD`（至少 12 个字符），然后运行：

```sh
docker compose up -d
```

访问 `http://服务器IP:8082`，默认用户名 `admin`，密码为 `.env` 中设置的值。登录后在设置中填写自己的模型服务地址、模型和 API Key。

首次启动会使用环境变量建立登录配置，只存储密码哈希。后续启动保留用户配置；更改 `.env` 不会重置已有密码，请在应用设置中修改。导入已有配置时请先启用局域网访问并设置登录凭据，否则容器外无法访问。

## Claude 运行时登录

镜像内置 Claude Code CLI。使用 Claude 订阅账号时，在容器内用认证码登录：

```sh
docker compose exec -it denova claude auth login
```

按提示在任意浏览器中打开输出的链接并授权，将页面显示的认证码粘贴回终端。凭据保存在持久卷的 `/data/.claude`，重启和更新镜像后保留。登录后在应用设置的「运行时」中选择 Claude Code，点击「检查连接」。用 `docker compose exec denova claude auth status` 查看登录状态，`claude auth logout` 退出登录。

若在设置中为 Claude 指定了模型服务配置（API 路由），则不需要登录。

## 数据

持久化卷 `denova_data` 挂载到 `/data`，用户配置、受管项目和会话位于 `/data/.denova`。备份时停止容器并备份整个卷。不要使用 `docker compose down -v`，该命令会删除数据。若改用主机目录挂载，目录需允许 UID/GID 1000 写入；外部项目路径必须另行挂载到容器。

镜像包含主程序、updater、前端、内嵌资源、Skills、ripgrep、Python、Git、Chromium 和 Claude Code CLI（自动发布时固定为构建当时的最新版，关闭自动更新，随镜像更新）。容器使用非 root 用户。浏览器工具使用容器中的 Chromium，镜像内的 Chromium 包装器关闭浏览器沙箱，容器本身保留 Docker 默认隔离。

## 更新

通过 `docker compose pull && docker compose up -d` 更新；容器内不要使用应用的桌面安装更新功能。可在 `.env` 中将 `IMAGE_TAG` 设为具体版本（如 `v0.5.0`）以固定版本。

## 自动发布

`.github/workflows/docker-publish.yml` 每小时检查上游仓库（默认 `alfredxw/denova`，可用仓库变量 `UPSTREAM_REPOSITORY` 修改）的最新 Release。发现镜像仓库中还没有的版本时，使用上游该 tag 的源码加上本仓库的 `Dockerfile`、`.dockerignore`、`compose.yaml` 和 `docker/` 构建，因此镜像内容与上游 Release 一致，不包含本仓库的其他改动。Go、Node、pnpm 版本从上游源码读取。

构建在 GitHub 的 amd64 运行器上完成，并验证页面、登录保护、登录、版本号、内置工具、重启后的配置和数据保留，通过后才发布版本标签。

- 手动发布：在 Actions 中运行 `Docker` 工作流，可指定 tag，勾选 `force` 可重建已存在的版本。
- 修改上述容器文件并推送到 `master` 后，会自动重建当前最新版本。
- 只有上游最新 Release 会移动 `latest`；手动重建旧版本不会让已有部署回退。

首次使用需要：

1. 在 fork 的 Actions 页面启用工作流（GitHub 默认不运行 fork 的定时任务；仓库 60 天无活动时定时任务也会被停用，需要重新启用）。
2. 首次发布后，在 GitHub 的 Packages → `denova` → Package settings 中将可见性改为 Public，否则拉取镜像需要登录。
