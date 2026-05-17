# AIYolo Gateway OpenAPI 兼容设计

本文档整理 AIYolo 对 OpenAI、Anthropic、OpenRouter 兼容接口的请求字段、响应格式、流式事件和错误结构处理方式。它不是逐字抄写第三方文档，而是把 AIYolo 需要接受、透传、归一化和明确拒绝的兼容面列成实现清单。

相关文档：

- [docs/openrouter-feature-inventory.md](./openrouter-feature-inventory.md)
- [docs/gateway-routing-design.md](./gateway-routing-design.md)
- [docs/gateway-billing-ledger-design.md](./gateway-billing-ledger-design.md)

当前代码锚点：

- 兼容入口在 [internal/gateway/handlers.go](../internal/gateway/handlers.go)
- OpenRouter provider 同步逻辑在 [internal/console/openrouter.go](../internal/console/openrouter.go)
- 现有兼容测试在 [internal/gateway/handlers_test.go](../internal/gateway/handlers_test.go)

## 1. 兼容目标

- 对客户端暴露稳定的 `/v1/*` 兼容入口，优先覆盖 IDE、Agent 和 SDK 常用面。
- 内部把不同上游协议归一为同一套 routing、quota、usage 和 audit 流程。
- 允许 OpenRouter 作为“多协议上游”，而不是把它当成新的客户端协议种类。
- 对不支持或暂未支持的字段明确给出兼容策略，而不是静默丢弃。

## 2. URL 与路径兼容面

### 2.1 当前已实现路径

| 外部路径 | 语义 | 当前状态 |
| --- | --- | --- |
| `GET /v1/models` | 模型目录 | 已实现 |
| `POST /v1/chat/completions` | OpenAI chat completions | 已实现 |
| `POST /v1/completions` | OpenAI legacy completions / FIM | 已实现 |
| `POST /v1/embeddings` | OpenAI embeddings | 已实现 |
| `POST /v1/responses` | OpenAI Responses | 已实现入口，字段支持待扩展 |
| `POST /v1/messages` | Anthropic messages | 已实现 |
| `POST /v1/messages/count_tokens` | Anthropic token counting | 已实现 |

### 2.2 建议新增路径别名

| 别名路径 | 说明 | 优先级 |
| --- | --- | --- |
| `/api/v1/chat/completions` | OpenRouter 风格别名 | P1 |
| `/api/v1/responses` | OpenRouter 风格别名 | P2 |
| `/api/v1/messages` | OpenRouter Anthropic 别名 | P1 |
| `/api/v1/models` | OpenRouter models 别名 | P1 |
| `/api/v1/key` | key info | P1 |
| `/api/v1/generation` | generation 回查 | P1 |

规则：

- 外部契约以 `/v1/*` 为主。
- `/api/v1/*` 作为兼容别名，不改变内部 handler 结构。
- 所有别名都应汇聚到同一套 transformer，而不是复制 handler。

## 3. 认证与请求头兼容

### 3.1 客户端到 AIYolo

| 协议面 | 接受的认证头 | 当前状态 |
| --- | --- | --- |
| OpenAI-compatible | `Authorization: Bearer aiyolo_xxx` | 已实现 |
| Anthropic-compatible | `x-api-key: aiyolo_xxx` | 已实现 |
| Anthropic-compatible | `Authorization: Bearer aiyolo_xxx` | 已实现 |

### 3.2 透传与注入头

当前允许透传的头：

- `Accept`
- `Content-Type`
- `User-Agent`
- `OpenAI-Organization`
- `OpenAI-Project`
- `Anthropic-Version`
- `Anthropic-Beta`
- `X-OpenRouter-Experimental-Metadata`

当前会由网关注入的头：

- 非 OpenRouter OpenAI 上游：`Authorization: Bearer <provider master key>`
- Anthropic 上游：`x-api-key: <provider master key>`
- OpenRouter 上游：
  - `Authorization: Bearer <provider master key>`
  - `HTTP-Referer: https://github.com/zltl/aiyolo`
  - `X-Title: aiyolo`

约束：

- 用户 API Key 永远不透传给上游。
- 未在白名单中的自定义头默认不透传，后续需要按协议面扩容。

## 4. 归一化模型

兼容层进入业务链路之前，应提炼出内部 `CanonicalRequest`。建议字段：

| 字段 | 说明 |
| --- | --- |
| `protocol_surface` | openai、anthropic |
| `endpoint_kind` | chat、responses、completions、embeddings、messages、count_tokens |
| `public_model` | 对外模型名 |
| `fallback_models` | 来自 OpenRouter `models[]` 或内部策略 |
| `stream` | 是否 SSE |
| `messages` | 统一消息数组 |
| `system_prompt` | system 文本或块 |
| `tools` | 工具定义 |
| `tool_choice` | 工具选择策略 |
| `response_format` | JSON mode 或 schema mode |
| `sampling` | temperature、top_p、top_k、seed 等 |
| `limits` | max_tokens、max_completion_tokens、stop |
| `provider_preferences` | OpenRouter `provider` 对象 |
| `debug_flags` | metadata、echo upstream body 等 |

## 5. 接口矩阵

### 5.1 Models API

| 字段 | OpenAI | OpenRouter | AIYolo 处理 |
| --- | --- | --- | --- |
| `object` | `list` | `list` | 已实现 |
| `data[].id` | 必需 | 必需 | 已实现，映射 `model_routes.public_name` |
| `data[].object` | `model` | `model` | 已实现 |
| `data[].created` | 可选 | 可选 | 已实现，当前取当前时间 |
| `data[].owned_by` | 可选 | 可选 | 已实现，当前填 `provider_id` |
| `context_length` | OpenAI 无统一字段 | OpenRouter 常见 | P1 |
| `pricing` | 无统一字段 | OpenRouter 有 | P1 |
| `supported_parameters` | 无统一字段 | OpenRouter 有 | P1 |

### 5.2 Chat Completions

#### 当前 P0 必须接受

| 字段 | 来源 | AIYolo 处理 |
| --- | --- | --- |
| `model` | OpenAI/OpenRouter | 必填；映射 public model |
| `messages` | OpenAI/OpenRouter | 必填；透传并归一化 |
| `stream` | OpenAI/OpenRouter | 已实现 |
| `temperature` | OpenAI/OpenRouter | 透传 |
| `top_p` | OpenAI/OpenRouter | 透传 |
| `max_tokens` | OpenAI/OpenRouter | 透传；参与预估 |
| `max_completion_tokens` | OpenAI/OpenRouter | 透传；参与预估 |
| `stop` | OpenAI/OpenRouter | 透传 |
| `tools` | OpenAI/OpenRouter | 透传 |
| `tool_choice` | OpenAI/OpenRouter | 透传 |
| `parallel_tool_calls` | OpenAI/OpenRouter | 透传 |
| `response_format` | OpenAI/OpenRouter | P1 归一化，P0 可透传 |
| `seed` | OpenAI/OpenRouter | 透传 |
| `user` | OpenAI/OpenRouter | 透传并写审计 metadata |
| `service_tier` | OpenAI/OpenRouter | P1 |

#### OpenRouter 扩展字段

| 字段 | 说明 | AIYolo 处理 |
| --- | --- | --- |
| `models` | 跨模型 fallback 列表 | P1 归一化到 `fallback_models` |
| `provider` | provider preferences | P1 归一化 |
| `route` | 虚拟 router profile | P2 |
| `transforms` / `plugins` | 插件管线 | P2 |
| `debug.echo_upstream_body` | 调试选项 | P2，需严格权限控制 |

### 5.3 Legacy Completions

| 字段 | 说明 | AIYolo 处理 |
| --- | --- | --- |
| `model` | public model | 已实现 |
| `prompt` | 文本或前缀 | 已实现透传 |
| `suffix` | FIM 补全后缀 | 已实现透传 |
| `max_tokens` | 输出上限 | 已实现 |
| `stream` | SSE | 已实现 |
| `temperature` | 采样 | 透传 |
| `stop` | 停止词 | 透传 |

### 5.4 Responses API

Responses 当前已暴露入口，但字段支持应明确分层：

| 字段 | AIYolo 处理 |
| --- | --- |
| `model` | 已实现 |
| `input` | P1 归一化为统一输入块 |
| `instructions` | P1 |
| `tools` | P1 |
| `response_format` | P1 |
| `stream` | P1 事件流 |
| `metadata` | P1 审计扩展 |

P0 允许透传到 OpenAI/OpenRouter-compatible 上游，但不保证所有事件子类型都被 AIYolo 完整理解。

### 5.5 Embeddings

| 字段 | 说明 | AIYolo 处理 |
| --- | --- | --- |
| `model` | public model | 已实现 |
| `input` | 字符串或数组 | 透传 |
| `encoding_format` | float/base64 | 透传 |
| `dimensions` | 目标维度 | P1 |
| `user` | 业务跟踪 | 透传并记审计 |

### 5.6 Anthropic Messages

#### 当前 P0 必须接受

| 字段 | AIYolo 处理 |
| --- | --- |
| `model` | 必填；映射 public model |
| `max_tokens` | 已实现 |
| `messages` | 已实现 |
| `system` | 已实现；支持字符串与数组块 |
| `stream` | 已实现 |
| `tools` | 已实现透传 |
| `tool_choice` | 已实现透传 |
| `metadata` | P1 |
| `stop_sequences` | 透传 |
| `temperature` | 透传 |
| `top_p` | 透传 |
| `top_k` | 透传 |

#### Header 兼容

| 头 | AIYolo 处理 |
| --- | --- |
| `Anthropic-Version` | 已保留与透传 |
| `Anthropic-Beta` | 已保留与透传 |

### 5.7 Anthropic Count Tokens

| 字段 | AIYolo 处理 |
| --- | --- |
| `model` | 已实现 |
| `messages` | 已实现 |
| `system` | 已实现 |
| `tools` | P1 |

这个接口不应写入 billable ledger，但可以写 audit 与轻量 usage 观测。

## 6. Provider Preferences 兼容面

OpenRouter 的 `provider` 对象是 AIYolo P1 的关键兼容面。建议字段映射如下：

| 字段 | 含义 | AIYolo 处理 |
| --- | --- | --- |
| `order` | 供应商尝试顺序 | P1 |
| `only` | 只允许这些 provider | P1 |
| `ignore` | 跳过这些 provider | P1 |
| `allow_fallbacks` | 是否允许扩展候选 | P1 |
| `require_parameters` | 只选支持全部参数的 endpoint | P1 |
| `data_collection` | `allow` 或 `deny` | P1 |
| `zdr` | 强制零数据保留 | P1 |
| `sort` | `price`、`latency`、`throughput` | P1 |
| `max_price` | prompt/completion/request/image 上限 | P1 |
| `preferred_min_throughput` | 吞吐偏好 | P2 |
| `preferred_max_latency` | 延迟偏好 | P2 |
| `quantizations` | 量化筛选 | P2 |

兼容策略：

- 未支持字段不可静默忽略时，应返回 `400 invalid_request_error` 或兼容错误体。
- 已支持但当前上游不适用时，可在 metadata 标明“已接收但未生效”。

## 7. 多模态与 content part 兼容

### 7.1 OpenAI 风格 content part

P1 应支持：

- `text`
- `image_url`
- `input_audio`
- `file`

### 7.2 Anthropic 风格 content block

P1 应支持：

- `text`
- `image`
- `tool_use`
- `tool_result`
- `thinking`
- `redacted_thinking`

归一化原则：

- 内部统一成 content parts，不在业务层保留供应商私有结构。
- 审计主表不直接存大对象，只存对象引用或脱敏摘要。

## 8. 流式响应兼容

### 8.1 OpenAI / OpenRouter SSE

当前已支持：

- `Content-Type: text/event-stream`
- `data: {...}`
- `data: [DONE]`

P1 需要补齐：

- 最终 chunk usage
- tool call delta
- router metadata terminal chunk
- mid-stream error 兼容输出

### 8.2 Anthropic SSE

P0 至少要正确通过并解析：

- `message_start`
- `content_block_start`
- `content_block_delta`
- `message_delta`
- `message_stop`

P1 要对以下事件保留语义：

- `content_block_stop`
- `ping`
- `error`

### 8.3 中途失败

规则：

- pre-stream 错误返回标准 JSON 错误体。
- 已输出流后发生的错误，按对应协议的 SSE 错误事件返回。
- 一旦已输出有效 chunk，不再切换到新 Provider 重试。

## 9. Usage 字段兼容

### 9.1 OpenAI / OpenRouter

当前已解析：

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`

P1 建议补齐：

- `prompt_tokens_details.cached_tokens`
- `prompt_tokens_details.cache_write_tokens`
- `completion_tokens_details.reasoning_tokens`
- `cost`
- `is_byok`
- `cost_details.upstream_inference_cost`
- `server_tool_use.web_search_requests`

### 9.2 Anthropic

当前已解析：

- `input_tokens`
- `output_tokens`
- `cache_creation_input_tokens`
- `cache_read_input_tokens`

P1 建议补齐：

- thinking token 细项
- tool use token 细项

## 10. 错误格式兼容

### 10.1 当前实现

当前统一错误体：

```json
{
  "error": {
    "code": "invalid_api_key",
    "message": "API key is invalid or disabled"
  }
}
```

优点：

- 对所有协议面一致。
- 简单，便于网关内部产错。

不足：

- `code` 当前是字符串，而不是 OpenRouter 文档里常见的整数 HTTP code。
- 未区分 OpenAI 风格与 Anthropic 风格错误包装。
- 缺少 `metadata`、`type`、`param` 等兼容字段。

### 10.2 目标统一错误模型

内部建议统一为：

| 字段 | 说明 |
| --- | --- |
| `http_status` | HTTP 状态码 |
| `gateway_code` | 网关级错误码 |
| `message` | 人类可读错误 |
| `provider_code` | 上游错误码 |
| `provider_status` | 上游状态码 |
| `metadata` | provider、request_id、retry_after 等 |
| `compat_surface` | openai、anthropic |

### 10.3 对外序列化规则

#### OpenAI-compatible

建议返回：

```json
{
  "error": {
    "message": "API key is invalid or disabled",
    "type": "invalid_request_error",
    "param": null,
    "code": "invalid_api_key"
  }
}
```

#### Anthropic-compatible

建议返回：

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "API key is invalid or disabled"
  }
}
```

#### OpenRouter-style enriched error

建议在 OpenAI-compatible 错误基础上增加：

```json
{
  "error": {
    "code": 429,
    "message": "quota exceeded",
    "metadata": {
      "gateway_code": "quota_exceeded",
      "provider": "openrouter",
      "retry_after": 30
    }
  }
}
```

### 10.4 错误映射表

| 场景 | HTTP | `gateway_code` | OpenAI type | Anthropic type |
| --- | --- | --- | --- | --- |
| 缺少 API Key | 401 | `missing_api_key` | `invalid_request_error` | `authentication_error` |
| API Key 无效 | 401 | `invalid_api_key` | `invalid_request_error` | `authentication_error` |
| 协议或模型不允许 | 403 | `not_allowed` | `permission_error` | `permission_error` |
| 请求 JSON 错误 | 400 | `invalid_json` | `invalid_request_error` | `invalid_request_error` |
| 模型不存在 | 404 | `model_not_found` | `invalid_request_error` | `not_found_error` |
| quota exceeded | 429 | `quota_exceeded` | `rate_limit_error` | `rate_limit_error` |
| 上游连接失败 | 502 | `upstream_error` | `api_error` | `api_error` |
| 协议不兼容 | 400 | `protocol_mismatch` | `invalid_request_error` | `invalid_request_error` |

## 11. 当前已实现与目标状态

### 11.1 已实现

- `/v1/models`
- `/v1/chat/completions`
- `/v1/completions`
- `/v1/embeddings`
- `/v1/responses` 入口
- `/v1/messages`
- `/v1/messages/count_tokens`
- OpenRouter 作为 OpenAI/Anthropic 双协议上游
- `Anthropic-Version`、`Anthropic-Beta` 透传
- SSE usage 基础解析

### 11.2 P1 补齐

- OpenRouter `models[]` fallback
- `provider` 对象
- 更完整的 `response_format` / structured outputs
- router metadata
- enriched usage details
- surface-aware error serialization

### 11.3 P2 补齐

- Responses 完整事件流
- server tools
- multimodal content parts
- debug/diagnostic flags

## 12. 测试与验收建议

新增兼容能力时，至少补以下测试：

- OpenAI-compatible 非流式成功
- OpenAI-compatible 流式成功
- Anthropic-compatible 非流式成功
- Anthropic-compatible SSE 成功
- OpenRouter 作为 OpenAI 上游成功
- OpenRouter 作为 Anthropic 上游成功
- provider preferences 拒绝与降级
- quota exceeded 错误序列化
- mid-stream error 兼容事件

## 13. 决策摘要

- AIYolo 的兼容层不应只做透明转发，而应做“接收、归一化、选路、计费、再序列化”。
- `/v1/*` 是主契约，`/api/v1/*` 作为兼容别名补齐。
- OpenRouter 重点不是新协议，而是 `models[]`、`provider`、metadata 和 usage 细项。
- 错误体必须保留统一内部语义，同时按 OpenAI、Anthropic 两种外部表面分别序列化。