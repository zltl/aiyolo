# AIYolo-Ass 最小接口设计

本文档定义运行在 cloud-agent 容器内部的 aiyolo-ass 最小控制面接口。P0 只覆盖四个能力：文件树、读文件、写文件、shell exec。目标不是一步做成通用 Agent 平台，而是先把当前散落在临时 Python 脚本、`docker exec` 和 shell bridge 里的容器内操作收敛到一个稳定接口上。

相关背景文档：

- [docs/api-gateway-console-design.md](./api-gateway-console-design.md)
- [docs/gateway-routing-design.md](./gateway-routing-design.md)

当前代码基线：

- Console workspace HTTP 接口在 [internal/console/chat_workspace.go](../internal/console/chat_workspace.go)
- Console workspace 前端调用在 [internal/console/static/chat-workspace.js](../internal/console/static/chat-workspace.js)
- Cloud-agent 一次性命令执行在 [internal/workers/cloud_agent_command.go](../internal/workers/cloud_agent_command.go)
- Cloud-agent 交互 shell 在 [internal/workers/cloud_agent_shell.go](../internal/workers/cloud_agent_shell.go)
- Cloud-agent Claude Code 执行在 [internal/workers/cloud_agent_claude.go](../internal/workers/cloud_agent_claude.go)
- Cloud-agent 镜像和启动脚本在 [internal/workers/cloud_agent.go](../internal/workers/cloud_agent.go)

## 1. 目标与非目标

### 1.1 目标

- 在 cloud-agent 容器内提供一个稳定、可版本化的内部接口，替换当前按请求动态拼接的 Python 脚本。
- 保持当前 console 工作区接口和调用语义易于迁移，不要求前端先重写。
- 让文件树、读写文件和一次性 shell exec 共享同一套路径约束、错误模型和运行时上下文。
- 为后续扩展交互终端、搜索、patch、文件监听、Claude Code 编排预留统一入口。

### 1.2 非目标

- P0 不覆盖交互式终端多路复用；现有终端链路可以继续保留，待 P1 再迁移。
- P0 不覆盖二进制文件编辑、文件上传、目录重命名、删除、批量 patch、grep、watch。
- P0 不直接暴露公网或控制台浏览器可直连的接口；它是 cloud-agent 内部私有控制面。
- P0 不引入独立数据库或持久化状态；除 shell exec 会话外，请求默认无状态。

## 2. 进程定位与部署

### 2.1 进程角色

`aiyolo-ass` 是 cloud-agent 容器内部的常驻进程，负责执行所有“必须进入容器内部才能完成”的操作。它不负责：

- 网关鉴权
- 用户会话管理
- 工作器 SSH 建连
- 浏览器直接渲染

这些仍由 gateway / console / worker 控制面处理。`aiyolo-ass` 只处理容器内事实操作。

### 2.2 推荐监听方式

P0 推荐提供两种监听方式，但只要求至少实现一种：

1. 主监听：Unix Domain Socket，路径 `/run/aiyolo/ass.sock`。
2. 调试监听：`127.0.0.1:17812`，默认关闭，仅用于容器内调试。

接口协议统一定义为 HTTP/1.1 + JSON；若后续需要交互 shell，可在同一路径前缀上增加 WebSocket upgrade。

接口契约不依赖调用方如何抵达该监听点。调用方可以通过以下任一方式接入：

- worker 上的 host-only 端口映射加本地反向代理
- gateway 到 worker 的 SSH 隧道
- `docker exec` 中转作为过渡适配层

P0 推荐的迁移路径是：先稳定容器内 API，再替换容器外访问路径；不要把“如何连到容器”与“容器内接口长什么样”耦合在一起。

### 2.3 运行时环境

`aiyolo-ass` 默认依赖这些环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AIYOLO_ASS_WORKSPACE_ROOT` | `/workspace` | 逻辑工作区根目录。 |
| `AIYOLO_ASS_USER` | `aiyolo` | shell exec 默认用户。 |
| `AIYOLO_ASS_HOME` | `/workspace` | shell exec 默认 `HOME`。 |
| `AIYOLO_ASS_MAX_FILE_BYTES` | `524288` | 文本编辑最大文件大小，先与当前 console 512 KiB 限制对齐。 |
| `AIYOLO_ASS_MAX_TREE_ENTRIES` | `1000` | 单次目录返回的最大条目数。 |
| `AIYOLO_ASS_MAX_EXEC_OUTPUT_BYTES` | `1048576` | shell exec 单次最大 stdout+stderr 返回大小。 |

## 3. 通用约定

### 3.1 路径约束

所有文件系统路径都使用 workspace 相对路径，而不是容器绝对路径：

- 根目录表示为空字符串 `""`
- 分隔符统一为 `/`
- 禁止 `..` 跳出工作区
- 禁止 NUL 字符
- `.`、`/`、空白根路径会被规范化为根目录或按接口语义判错

P0 只处理工作区内路径，不暴露“任意容器路径读写”能力。

### 3.2 编码与文本约束

- 文件读取和写入都只支持 UTF-8 文本。
- 含 NUL 的文件视为二进制文件，拒绝返回编辑内容。
- 超过 `AIYOLO_ASS_MAX_FILE_BYTES` 的文件拒绝读取和写入。

### 3.3 返回包格式

P0 统一使用 envelope，避免不同接口各自发明状态字段：

成功响应：

```json
{
  "status": "ok",
  "data": {},
  "request_id": "req_123"
}
```

错误响应：

```json
{
  "status": "error",
  "error": {
    "code": "path_invalid",
    "message": "workspace path escapes root",
    "retryable": false
  },
  "request_id": "req_123"
}
```

建议错误码：

| `error.code` | 含义 | 典型 HTTP 状态 |
| --- | --- | --- |
| `path_invalid` | 路径为空、非法或越界 | `400` |
| `path_not_found` | 文件或目录不存在 | `404` |
| `path_not_directory` | 期望目录却给了文件 | `400` |
| `path_not_file` | 期望文件却给了目录 | `400` |
| `file_too_large` | 超过可编辑大小 | `413` |
| `file_binary` | 文件含二进制内容 | `415` |
| `encoding_invalid` | 不是 UTF-8 | `415` |
| `conflict` | 乐观锁 revision 不匹配 | `409` |
| `exec_invalid` | exec 请求体不合法 | `400` |
| `exec_timeout` | exec 超时 | `408` |
| `internal_error` | 进程内部错误 | `500` |

### 3.4 Revision 约定

读文件和写文件都返回一个不透明 `revision` 字段，用于乐观并发控制。P0 推荐值：

```text
sha256:<hex>
```

客户端不得依赖其生成算法，只能把它视作内容版本号。

## 4. 接口设计

### 4.1 `GET /v1/healthz`

用途：容器内健康检查、gateway 连接预热、调试。

响应：

```json
{
  "status": "ok",
  "data": {
    "service": "aiyolo-ass",
    "version": "0.1.0",
    "workspace_root": "/workspace"
  }
}
```

说明：

- 只返回当前进程和工作区根目录是否可访问。
- 不做重量级依赖检查。

### 4.2 `GET /v1/fs/tree`

用途：返回某个目录的一层子项，覆盖当前 console “展开目录树”能力。

查询参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `path` | `""` | 目录相对路径，空字符串表示根目录。 |
| `limit` | `200` | 单次返回最大条目数，不能超过 `AIYOLO_ASS_MAX_TREE_ENTRIES`。 |
| `include_hidden` | `false` | 是否返回点文件。P0 默认不返回。 |

响应体：

```json
{
  "status": "ok",
  "data": {
    "path": "internal/console",
    "entries": [
      {
        "name": "static",
        "path": "internal/console/static",
        "type": "directory",
        "size": 4096,
        "modified_at": "2026-05-28T07:14:03Z",
        "has_children": true
      },
      {
        "name": "chat_workspace.go",
        "path": "internal/console/chat_workspace.go",
        "type": "file",
        "size": 18293,
        "modified_at": "2026-05-28T06:22:15Z",
        "has_children": false
      }
    ],
    "truncated": false
  }
}
```

语义约定：

- 只返回直接子项，不递归。
- 排序规则与当前 console 保持一致：目录在前，文件在后，同类按名称不区分大小写排序。
- P0 可以跳过无法安全解析的 symlink、socket、device、FIFO；不要求前端先理解这些特殊类型。
- `has_children` 只对目录有意义，用于前端决定是否渲染可展开状态。

### 4.3 `GET /v1/fs/file`

用途：读取单个文本文件内容，覆盖当前 console “打开文件”能力。

查询参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `path` | 无 | 文件相对路径，必填。 |

响应体：

```json
{
  "status": "ok",
  "data": {
    "path": "README.md",
    "size": 14238,
    "revision": "sha256:8a7f3d...",
    "content": "# AIYolo\n..."
  }
}
```

语义约定：

- 仅允许读取普通文本文件。
- 若文件过大、二进制或非 UTF-8，返回错误而不是部分内容。
- `size` 表示字节数，不是字符数。

### 4.4 `PUT /v1/fs/file`

用途：保存单个文本文件内容，覆盖当前 console “保存文件”能力。

请求体：

```json
{
  "path": "README.md",
  "content": "# AIYolo\nupdated\n",
  "expected_revision": "sha256:8a7f3d...",
  "create": false,
  "mkdir_p": false
}
```

字段说明：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `path` | 无 | 文件相对路径，必填。 |
| `content` | 无 | UTF-8 文本内容。 |
| `expected_revision` | 空 | 可选乐观锁；若提供且不匹配，返回 `409 conflict`。 |
| `create` | `false` | 文件不存在时是否允许创建。 |
| `mkdir_p` | `false` | 创建文件时是否允许自动创建父目录。 |

成功响应：

```json
{
  "status": "ok",
  "data": {
    "path": "README.md",
    "bytes": 14302,
    "revision": "sha256:5d6a1e..."
  }
}
```

写入语义：

- 必须采用原子写入：先写临时文件，再 `rename` 覆盖目标文件。
- 默认不创建新文件，也不创建父目录，避免前端误操作扩大能力面。
- 若 `expected_revision` 为空，则按“最后写入者覆盖”处理；console P0 可以先不启用乐观锁，但接口应保留该能力。

### 4.5 `POST /v1/shell/exec`

用途：执行容器内一次性命令，覆盖当前 `runCloudAgentCommand` 这类“短命 shell/script”能力。P0 不替代交互终端。

请求体：

```json
{
  "mode": "bash",
  "script": "git status --short",
  "cwd": "",
  "env": {
    "CI": "1"
  },
  "stdin": "",
  "timeout_ms": 30000,
  "max_output_bytes": 262144
}
```

或安全模式：

```json
{
  "mode": "argv",
  "argv": ["git", "status", "--short"],
  "cwd": "",
  "timeout_ms": 30000
}
```

字段说明：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `mode` | 无 | `bash` 或 `argv`。 |
| `script` | 空 | `mode=bash` 时必填，按 `/bin/bash -lc` 执行。 |
| `argv` | 空 | `mode=argv` 时必填，直接 exec，不经过 shell 解析。 |
| `cwd` | `""` | 工作区相对路径，空字符串表示工作区根。 |
| `env` | `{}` | 附加环境变量；不允许覆盖 `HOME`、`USER`、`PATH`、`SHELL`。 |
| `stdin` | 空 | 进程标准输入，P0 仅支持一次性字符串输入。 |
| `timeout_ms` | `30000` | 超时毫秒数。 |
| `max_output_bytes` | `262144` | 单次返回最大输出，不能超过进程上限。 |

响应体：

```json
{
  "status": "ok",
  "data": {
    "mode": "bash",
    "cwd": "",
    "exit_code": 0,
    "timed_out": false,
    "stdout": " M README.md\n",
    "stderr": "",
    "truncated": false,
    "duration_ms": 18
  }
}
```

执行语义：

- `cwd` 必须落在工作区内，不能切到任意容器路径。
- 默认执行用户为 `AIYOLO_ASS_USER`，默认 `HOME` 为 `AIYOLO_ASS_HOME`。
- 非零退出码不是协议错误，仍返回 `200`，由调用方根据 `exit_code` 决定是否视为失败。
- 超时会杀掉整个进程组，并返回 `timed_out=true`；P0 可以同时把 `error.code` 设为 `exec_timeout`，但推荐仍用 `200` 包装执行结果，便于上层保留部分 stdout/stderr。
- P0 不分配 PTY，不保证交互程序可用；这是与未来 `/v1/shell/sessions` 的边界。

## 5. 与当前代码的映射

### 5.1 Workspace 文件树

当前 console：

- `GET /console/chat/workspace/tree`
- 后端在 [internal/console/chat_workspace.go](../internal/console/chat_workspace.go)

迁移后：

- gateway 继续保留 `GET /console/chat/workspace/tree`
- handler 内部不再拼 Python 脚本，而是把请求转换为 `GET /v1/fs/tree`

这样前端 [internal/console/static/chat-workspace.js](../internal/console/static/chat-workspace.js) 无需先改。

### 5.2 Workspace 读文件

当前 console：

- `GET /console/chat/workspace/file`

迁移后：

- gateway 转发为 `GET /v1/fs/file`

### 5.3 Workspace 写文件

当前 console：

- `POST /console/chat/workspace/file`

迁移后：

- gateway 对浏览器保持 `POST` 兼容
- gateway 内部把请求翻译成 `PUT /v1/fs/file`

### 5.4 一次性命令执行

当前执行面：

- `RunCloudAgentCommand(...)` 在 [internal/workers/cloud_agent_command.go](../internal/workers/cloud_agent_command.go)

迁移后：

- 对一次性脚本执行，gateway 不再传整段脚本给 `docker exec bash -s --`
- 改为调用 `POST /v1/shell/exec`
- 当前 `buildConsoleChatWorkspaceTreeScript`、`buildConsoleChatWorkspaceReadScript`、`buildConsoleChatWorkspaceWriteScript` 这类临时脚本可以删除或收缩为兼容层

### 5.5 暂不迁移的能力

这些先保持原样，不强行并入 P0：

- 交互 shell websocket
- Claude Code 长流式执行
- 浏览器内嵌 terminal 多 tab 管理

P1 可以在 aiyolo-ass 下追加：

- `POST /v1/shell/sessions`
- `WS /v1/shell/sessions/{id}`
- `POST /v1/shell/sessions/{id}/resize`

## 6. 最小实现建议

P0 最小实现建议保持朴素，不提前抽象过度：

1. 用 Go 实现 `aiyolo-ass`，直接复用标准库 `net/http`。
2. 先实现 `/v1/healthz`、`/v1/fs/tree`、`/v1/fs/file`、`/v1/shell/exec` 四个接口。
3. 文件路径校验与当前 [internal/console/chat_workspace.go](../internal/console/chat_workspace.go) 保持同等严格，避免迁移后语义漂移。
4. 写文件使用原子替换，并补上 revision 能力；这比当前逻辑更稳。
5. shell exec 先只做 non-PTY 一次性执行，明确不承诺交互行为。

## 7. 分阶段落地

### 7.1 P0

- cloud-agent 镜像内置 `aiyolo-ass`
- 启动脚本拉起该进程
- gateway 新增调用适配层
- console 文件树、读文件、写文件切到 aiyolo-ass
- `runCloudAgentCommand` 的短命脚本迁移到 `/v1/shell/exec`

### 7.2 P1

- 追加交互 shell session API
- 统一底部 terminal 和命令执行链路
- 为 Claude Code / 其他容器内任务提供统一作业编排入口

### 7.3 P2

- 增加 grep/search、apply patch、mkdir/rm/rename、watch、git 状态等能力
- 与 worker 侧 `aiyolo-workerd` 形成稳定的 host/container 双层控制面

## 8. 结论

`aiyolo-ass` 的 P0 不需要一开始就接管 cloud-agent 里的所有事情。把接口面先收敛到四个能力已经足够替换掉当前最分散、最频繁、最适合稳定化的容器内操作：文件树、读文件、写文件、一次性 shell exec。只要这四个接口先稳定下来，后续交互终端、Claude Code、搜索和 patch 才有清晰的挂载点。