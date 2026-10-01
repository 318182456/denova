# Docker 部署

镜像：`ghcr.io/318182456/denova`，仅提供 Linux amd64。标签与上游 Release 一致（如 `v0.5.0`），`latest` 指向上游最新 Release。

## 启动

将仓库根目录的 `compose.yaml` 和 `docker/compose.env.example` 下载到同一目录，将后者重命名为 `.env`。修改 `.env` 的 `DENOVA_PASSWORD`（至少 12 个字符），然后运行：

```sh
docker compose up -d
```

访问 `http://服务器IP:8082`，默认用户名 `admin`，密码为 `.env` 中设置的值。登录后在设置中填写自己的模型服务地址、模型和 API Key。

首次启动会使用环境变量建立登录配置，只存储密码哈希。后续启动保留用户配置；更改 `.env` 不会重置已有密码，请在应用设置中修改。导入已有配置时请先启用局域网访问并设置登录凭据，否则容器外无法访问。

## Claude Code 管理页

镜像内置 Claude Code CLI，并在同一端口提供管理页 `http://服务器IP:8082/claude/`，用于查看状态、用认证码登录、退出登录和更新 Claude Code。管理页沿用 Denova 的登录状态，未登录时会跳转到 Denova 登录页，登录后重新打开该地址即可。该页面只由容器提供，不修改应用代码。

登录：选择「Claude 订阅」或「Anthropic Console」，点击「开始登录」，在浏览器中打开给出的链接并授权，将授权后显示的认证码粘贴回页面提交。凭据保存在持久卷的 `/data/.claude`，重启和更新镜像后保留。

更新：点击「检查并更新」安装最新版，新版本保存在持久卷的 `/data/.local`，并优先于镜像自带版本使用。镜像更新后若自带版本更高，启动时会自动删除卷中的旧版本并改用镜像版本。

登录或更新后，在应用设置的「运行时」中选择 Claude Code，点击「检查连接」。若为 Claude 指定了 Denova 模型档案（API 路由），则不需要登录。也可以在命令行完成登录：`docker compose exec -it denova claude auth login`。

### 长期令牌（推荐）

普通登录的访问令牌几小时后过期，需要在线刷新；网络或代理异常、容器重启或多个会话同时刷新时，可能出现 `Failed to refresh OAuth token`。长期部署建议改用长期令牌（需要 Claude 订阅，有效期约一年，无需刷新）：

1. 生成令牌：`docker compose exec -it denova claude setup-token`，或在任意装有 Claude Code 的电脑上执行 `claude setup-token`。按提示在浏览器授权并粘贴认证码，终端会输出以 `sk-ant-oat01-` 开头的令牌。该命令需要交互式终端，无法在管理页中执行。
2. 在管理页「长期令牌与额度」中填写名称和令牌并添加。第一个添加的令牌自动启用；可添加多个，点击「切换到此令牌」切换，或「删除」移除。
3. 在「运行时」中点击「检查连接」。

管理页列出每个令牌的 5 小时额度、每周额度和按模型计算的每周额度，以及各自的重置时间，结果缓存约 1 分钟，可点击「刷新额度」更新。额度来自 Claude 账号用量接口；令牌无权读取时，改为发送一次 1 个输出 token 的最小请求，从响应头读取额度。读取失败时显示 Anthropic 返回的状态和原因：401 表示令牌无效或已过期，403 `Request not allowed` 通常表示所在地区需要代理。

### 网络代理

访问 Anthropic 需要代理时，在管理页「网络代理」中填写地址（如 `http://192.168.1.10:7890`，支持 `http`、`https`、`socks5`）。地址写入 Claude 的 `/data/.claude/settings.json` 的 `env`，Claude CLI 和额度查询共用，对之后启动的任务生效；首次修改前会把原文件备份为 `settings.json.denova-backup`，其他设置保持不变。未填写时使用容器的 `HTTPS_PROXY` 等环境变量。代理在宿主机上时不要填 `127.0.0.1`，应使用宿主机地址或 `host.docker.internal`，并开启代理软件的局域网连接。

切换只影响之后启动的 Claude 进程，正在运行的任务继续使用原令牌，无需重启容器。使用 Denova 模型档案（API 路由）的运行、`claude auth login/logout` 和 `claude setup-token` 不使用所选令牌。令牌保存在持久卷的 `/data/.config/denova-claude/`（权限 0600），未选择令牌时依次使用 `.env` 的 `CLAUDE_CODE_OAUTH_TOKEN` 和登录的账号。令牌等同于账号凭据，不要分享。

实现方式：容器入口进程在 8080 端口提供 `/claude/`，其余请求反向代理到容器内部 18080 端口上的 Denova，并负责 Denova 的启停。代理会用真实来源地址覆盖客户端传来的 `X-Forwarded-*` 头，因此外部请求无法伪装成本机访问。从旧镜像升级后需要重新登录一次 Denova。

## 数据

持久化卷 `denova_data` 挂载到 `/data`，用户配置、受管项目和会话位于 `/data/.denova`。备份时停止容器并备份整个卷。不要使用 `docker compose down -v`，该命令会删除数据。若改用主机目录挂载，目录需允许 UID/GID 1000 写入；外部项目路径必须另行挂载到容器。

镜像包含主程序、updater、前端、内嵌资源、Skills、ripgrep、Python、Git、Chromium 和 Claude Code CLI（自动发布时固定为构建当时的最新版，关闭后台自动更新，可在管理页手动更新）。容器使用非 root 用户。浏览器工具使用容器中的 Chromium，镜像内的 Chromium 包装器关闭浏览器沙箱，容器本身保留 Docker 默认隔离。

## 更新

通过 `docker compose pull && docker compose up -d` 更新；容器内不要使用应用的桌面安装更新功能。可在 `.env` 中将 `IMAGE_TAG` 设为具体版本（如 `v0.5.0`）以固定版本。

## 自动发布

`.github/workflows/docker-publish.yml` 每小时检查上游仓库（默认 `alfredxw/denova`，可用仓库变量 `UPSTREAM_REPOSITORY` 修改）的最新 Release。发现镜像仓库中还没有的版本时，使用上游该 tag 的源码加上本仓库的 `Dockerfile`、`.dockerignore`、`compose.yaml` 和 `docker/` 构建，因此镜像内容与上游 Release 一致，不包含本仓库的其他改动。Go、Node、pnpm 版本从上游源码读取。

构建在 GitHub 的 amd64 运行器上完成，并验证页面、登录保护、登录、版本号、内置工具、重启后的配置和数据保留，通过后才发布版本标签。

- 手动发布：在 Actions 中运行 `Docker` 工作流，可指定 tag，勾选 `force` 可重建已存在的版本。
- 本仓库源码构建：推送到 `master`（仅改 `docs/` 或 Markdown 除外）会自动用本仓库完整源码构建并发布 `fork` 标签，用于尚未进入上游 Release 的修复；也可手动运行时勾选 `fork`。在 `.env` 中设 `IMAGE_TAG=fork` 后 `docker compose pull && docker compose up -d` 即可使用；改回 `latest` 即恢复上游 Release。`fork` 标签不会移动 `latest`，其版本号为 `<上游版本>-fork.<最后一次应用代码提交>`。
- 推送中包含上述容器文件的修改时，同一次运行会并行重建当前最新版本。两个构建使用各自的层缓存；只改容器文件时复用已缓存的应用编译。
- 只有上游最新 Release 会移动 `latest`；手动重建旧版本不会让已有部署回退。

首次使用需要：

1. 在 fork 的 Actions 页面启用工作流（GitHub 默认不运行 fork 的定时任务；仓库 60 天无活动时定时任务也会被停用，需要重新启用）。
2. 首次发布后，在 GitHub 的 Packages → `denova` → Package settings 中将可见性改为 Public，否则拉取镜像需要登录。
