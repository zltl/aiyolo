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

默认镜像标签是 `aiyolo/local-cloud-agent:ubuntu-24.04`。镜像里预装了 `git`、`python3`、`jq`、`xvfb`、`fluxbox`、`x11vnc`、`dbus-x11` 和一组常见浏览器运行时依赖，作为后续 `computer-use` / `browser-use` 的基础层。

现在的构建方式已经改成真正的 `docker build`：先从阿里云 `ubuntu-base` 目录下载 Ubuntu 24.04 rootfs，再在构建过程中把 apt 源切到阿里云镜像 `https://mirrors.aliyun.com/ubuntu`，并把以下能力一起装进基础镜像：

- 轻量桌面：`Xvfb` + `fluxbox` + `x11vnc`
- 浏览器：`google-chrome-stable`
- Docker-in-Docker：`docker.io` + `dockerd`

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
- 本地会话历史保存在浏览器里，支持重命名、删除和多标签页同步。
- 支持附件上传；多模态请求会按当前路由协议自动组装 content parts。
- 当前保留的对话接口是 `POST /console/chat` 和 `POST /console/chat/stream`。
- 附件上传接口是 `POST /console/chat/attachments`，对象存储走独立的 `chat.attachments.*` 配置。

Chat 页面现在已经切成独立的 Gemini 风格工作区，并限制到当前允许的模型集合。下一步会继续补会话搜索、附件清理和更细的多模态能力边界。
