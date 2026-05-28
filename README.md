# AIYolo

AIYolo 是一个“云原生 Agentic IDE + 混合计算架构”工作空间。目标是让用户在浏览器中完成 AI 开发、实验、数据分析和代码协作，同时把云端隔离算力、本地资源、安全授权、Agent 自动化和实时可观测性组织成一个可实现、可扩展的系统。

## 本机 Worker Bootstrap

仓库现在自带一个把当前 Ubuntu 主机直接初始化为本地 worker 的入口：

```bash
sudo -E make bootstrap-local-worker
```

它会在本机完成 Docker、`aiyolo-workerd` systemd 服务和 `/readyz` 健康检查的拉起，并复用仓库里的 worker runtime 模板文件。默认数据根目录是 `/var/lib/aiyolo-agent`，Docker 数据目录会落到 `/var/lib/aiyolo-agent/docker`。也可以通过环境变量覆盖，例如 `AIYOLO_LOCAL_DATA_ROOT=/srv/aiyolo-agent sudo -E make bootstrap-local-worker`。如果当前 shell 挂了本地代理，想让 bootstrap 直接走直连，可以加上 `AIYOLO_LOCAL_SKIP_PROXY_ENV=1`。

如果要把本机数据盘直接挂进 worker data root，可以这样跑：

```bash
sudo -E AIYOLO_LOCAL_SKIP_PROXY_ENV=1 AIYOLO_LOCAL_DATA_DEVICE=/dev/vdb make bootstrap-local-worker
```

## 本机 Cloud Agent Image

仓库现在也自带了一个**本地构建 Ubuntu 24.04 cloud-agent 镜像**的入口，不依赖 Docker Hub 基础镜像：

```bash
sudo -E make build-cloud-agent-image
sudo -E CLOUD_AGENT_USER='i@quant67.com' make run-cloud-agent-local
```

默认镜像标签是 `aiyolo/local-cloud-agent:ubuntu-24.04-v2`。镜像里预装了 `git`、`python3`、`jq`、`xvfb`、`fluxbox`、`x11vnc`、`dbus-x11` 和一组常见浏览器运行时依赖，作为后续 `computer-use` / `browser-use` 的基础层；Claude Code 会以容器内的非 root `aiyolo` 用户运行，这样 cloud-agent chat 可以直接启用完整的工具调用。

现在的构建方式已经改成真正的 `docker build`：先从阿里云 `ubuntu-base` 目录下载 Ubuntu 24.04 rootfs，再在构建过程中把 apt 源切到阿里云镜像 `https://mirrors.aliyun.com/ubuntu`，并把以下能力一起装进基础镜像：

- 轻量桌面：`Xvfb` + `fluxbox` + `x11vnc`
- 浏览器：`google-chrome-stable`
- Docker-in-Docker：`docker.io` + `dockerd`

Cloud Agent 容器里的 `aiyolo-ass` 现在走编译后的 Linux amd64 二进制，不再依赖仓库里的内嵌 Python 兜底脚本。`make build-cloud-agent-image` 默认直接 `go build ./cmd/aiyolo-ass` 后复制进镜像；如果要验证已发布的 release 产物，也可以在构建时传 `AIYOLO_ASS_DOWNLOAD_URL` 和 `AIYOLO_ASS_SHA256_URL`。

`aiyolo-ass` 现在有单独的发布入口：`make ass-release VERSION=vX.Y.Z`。它会把 `bin/aiyolo-ass-linux-amd64` 上传到 `artifacts.s3.bucket`（例如 `aiyolo-releases`），并一次性写出三组对象：

- stable alias：`linux-amd64/aiyolo-ass`
- latest alias：`linux-amd64/latest/aiyolo-ass`
- versioned object：`linux-amd64/vX.Y.Z/aiyolo-ass`

每个对象都会同步生成对应的 `.sha256`。如果你要连同 gateway 和 Windows wrapper 一起发版，仍然用 `make publish-release-artifacts VERSION=vX.Y.Z`。

当前 release/build/upload/download/update 流程固定如下：

1. `make build-ass-release` 只构建 Linux amd64 的 `aiyolo-ass` 二进制到 `bin/`。
2. `make ass-release VERSION=vX.Y.Z` 把 stable/latest/versioned + `.sha256` 上传到配置好的 OSS/S3 bucket。
3. Gateway 对外统一暴露 `/artifacts/...` 下载路径；Cloud Agent ensure 总是读取 `/artifacts/linux-amd64/aiyolo-ass.sha256` 和 `/artifacts/linux-amd64/aiyolo-ass`。
4. `artifacts.public_base_url` 应该配置成 AIYolo 网关根地址，例如 `https://aiyolo.quant67.com`，同时开启 `artifacts.public_via_proxy: true`；这样 catalog、发布日志和其他对外下载入口都会统一生成成 `/artifacts/...` relay URL，而不是暴露 OSS 直链。
5. `/artifacts` 代理不会再去拼公开下载域名，而是直接通过 `artifacts.s3.endpoint` 或 `artifacts.s3.internal_endpoint` 读取对象存储并把内容中转给客户端；因此 Cloud Agent 更新链路不再依赖 OSS CNAME 证书状态。
6. Worker 在远端构建 cloud-agent 镜像时把 stable alias 的最新 SHA256 写进镜像 label；只要 checksum 变化，下一次 Cloud Agent ensure 或 shell 打开就会自动重建镜像并替换容器，完成 `aiyolo-ass` 更新。

本地运行容器时，`make run-cloud-agent-local` 会默认：

- 以 `--privileged` 启动容器，拉起内层 `dockerd`
- 暴露 VNC 端口到宿主机 `127.0.0.1:15900`
- 自动启动 Chrome，并暴露 DevTools 端口到 `127.0.0.1:19222`

如果你在阿里云上还想给 DinD 配 Docker Hub 加速器，可以额外传：

```bash
sudo -E AIYOLO_CLOUD_AGENT_DOCKER_REGISTRY_MIRROR='https://<你的阿里云镜像地址>' make run-cloud-agent-local
```

如果你有可访问的 Chrome 安装包镜像，也可以在构建时覆盖：

```bash
sudo -E AIYOLO_CLOUD_AGENT_CHROME_DEB_URL='https://<你的镜像>/google-chrome-stable_current_amd64.deb' make build-cloud-agent-image
```

## License

本项目采用 GNU Affero General Public License v3.0 许可协议发布，详见 LICENSE。

## Console Chat

控制台左侧导航提供独立的 Chat 入口，会在新标签页打开 `/console/chat`。当前实现只保留控制台原生 chat 工作区，也不再包含 monorepo 内的前端前置子项目。

- 入口路径是 `/console/chat`。
- 使用控制台内置的服务端渲染 chat 页面和流式接口。
- 复用现有 provider、模型路由、代理和 usage 链路。
- 选择 Cloud Agent 环境后，chat 消息会直接接到对应 worker 容器里的 Claude Code；Claude Code 再通过 AIYolo gateway 的 Anthropic 兼容接口调用当前选中的 public model（例如 `deepseek-v4-pro`）。
- 本地会话历史保存在浏览器里，支持重命名、删除和多标签页同步。
- 支持附件上传；多模态请求会按当前路由协议自动组装 content parts。
- 当前保留的对话接口是 `POST /console/chat` 和 `POST /console/chat/stream`。
- 附件上传接口是 `POST /console/chat/attachments`，对象存储走独立的 `chat.attachments.*` 配置。

Chat 页面现在已经切成独立的 Gemini 风格工作区，并限制到当前允许的模型集合。下一步会继续补会话搜索、附件清理和更细的多模态能力边界。
