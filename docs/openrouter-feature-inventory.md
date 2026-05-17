# OpenRouter 功能复刻清单

本文档基于 OpenRouter 官方文档和产品页面整理，用于指导 AIYolo 的 AI API Gateway Console 继续补齐多模型、多供应商、路由、计费、审计、观测和企业管控能力。它不是逐字复刻页面，而是把可借鉴的产品能力拆成可实现的工程需求。

调研来源包括：

- `https://openrouter.ai/docs/quickstart`
- `https://openrouter.ai/docs/llms-full.txt`
- `https://openrouter.ai/models`
- `https://openrouter.ai/providers`
- `https://openrouter.ai/rankings`
- `https://openrouter.ai/apps`
- `https://openrouter.ai/request-builder`
- `https://openrouter.ai/playground`
- `https://openrouter.ai/pricing`
- `https://openrouter.ai/enterprise`

## 1. 产品定位

OpenRouter 的核心不是单纯的 API 转发，而是一个多供应商 AI API 控制平面：

- 一个 OpenAI-compatible API 入口，访问大量模型和供应商。
- 自动选择价格、吞吐、延迟、可用性更合适的供应商端点。
- 提供统一账单、API Key、额度、日志、导出、团队和隐私策略。
- 把模型目录、供应商目录、应用排行、真实用量统计和调试工具做成产品的一部分。
- 为企业提供 BYOK、ZDR、区域路由、广播到观测平台、SSO/SAML、SLA 等能力。

AIYolo 如果要复刻 OpenRouter 的关键价值，优先要复刻的是“可自部署的控制平面 + 协议兼容网关 + 路由/计费/审计闭环”，而不是公开社区市场。

## 2. 功能优先级标记

| 标记 | 含义 |
| --- | --- |
| P0 | MVP 必须具备，否则无法成为可用 AI Gateway。 |
| P1 | 生产可用所需的可靠性、成本控制和团队能力。 |
| P2 | 差异化能力，适合在核心稳定后补齐。 |
| P3 | 平台化、市场化或企业高级能力。 |

## 3. 公共 API 能力

| 能力 | OpenRouter 表现 | AIYolo 建议 | 优先级 |
| --- | --- | --- | --- |
| Chat Completions | `POST /api/v1/chat/completions`，OpenAI-compatible，支持流式、工具、结构化输出、多模态内容。 | 对外保留 `/v1/chat/completions`，内部可同时兼容 `/api/v1/chat/completions` 别名。 | P0 |
| Responses API | `POST /api/v1/responses`，Beta，stateless，兼容 OpenAI Responses。 | 初期可先代理或转换常用字段，后续完整支持事件流。 | P2 |
| Anthropic Messages | `POST /api/v1/messages`，支持 Anthropic 风格请求和 `service_tier`。 | AIYolo 已规划 Anthropic-compatible，应作为 Claude Code 兼容核心。 | P0 |
| Legacy Completions | `/api/v1/completions`，用于非 chat 或部分补全场景。 | 为 IDE FIM 和老 SDK 保留。 | P1 |
| Embeddings | `/api/v1/embeddings`，支持批量文本和部分多模态 embedding。 | 支持文本 embedding 是 P1，多模态 embedding 可后置。 | P1 |
| Rerank | 独立 rerank API 和模型分类。 | 对 RAG/搜索有价值，可作为独立 adapter。 | P2 |
| Text-to-Speech | `/api/v1/audio/speech`，OpenAI-compatible，输出音频流。 | 多模态产品后置。 | P2 |
| Speech-to-Text | `/api/v1/audio/transcriptions`，返回结构化转写结果和 usage。 | 多模态产品后置。 | P2 |
| Video generation | `/api/v1/videos`，异步提交、返回 job id、轮询结果。 | 需要 job 表和对象存储，适合后置。 | P3 |
| Models API | `/api/v1/models`，可按输出模态、支持参数过滤。 | 必须提供给 SDK、控制台和客户端自动发现。 | P0 |
| Key info | `/api/v1/key`，查询额度、剩余额度、BYOK 用量、周期用量。 | API Key 自助和客户端诊断需要。 | P1 |
| Generation stats | `/api/v1/generation?id=...`，按 generation id 回查用量和成本。 | 审计、调试和异步计费结算需要。 | P1 |
| Management API | `/api/v1/keys` 等管理 API，使用 management key 做 CRUD。 | 控制台之外的自动化管理能力。 | P1 |

## 4. 模型目录

OpenRouter 的模型目录是核心产品面，不只是静态列表。

### 4.1 页面能力

- 模型卡片和表格视图。
- 按输入模态过滤：text、image、file、audio、video。
- 按输出模态过滤：text、image、audio、embeddings。
- 按上下文长度、价格、系列、类别、供应商、作者过滤。
- 按支持参数过滤：tools、tool_choice、reasoning、structured_outputs、response_format、seed 等。
- 按隐私和合规标签过滤：ZDR、In-Region Routing、Distillable、Inactive。
- 排序项包括 newest、popular、weekly、price、context、throughput、latency。

### 4.2 API 字段

Models API 至少应提供：

| 字段 | 用途 |
| --- | --- |
| `id` | API 请求使用的模型 slug，例如 `openai/gpt-5.2`。 |
| `canonical_slug` | 稳定模型标识，避免展示名变化影响引用。 |
| `name`、`description` | 控制台展示和搜索。 |
| `created`、`expiration_date` | 上新、废弃、迁移提示。 |
| `context_length` | 请求校验、自动截断、路由选择。 |
| `architecture.input_modalities` | 判断 image、file、audio、video 输入是否可用。 |
| `architecture.output_modalities` | 判断 text、image、audio、embeddings 输出是否可用。 |
| `architecture.tokenizer` | 成本估算和 token 统计。 |
| `pricing.prompt`、`pricing.completion` | 基础 token 计费。 |
| `pricing.request`、`pricing.image`、`pricing.web_search` | 请求级、图片级、工具级计费。 |
| `pricing.internal_reasoning` | reasoning token 单独计费。 |
| `pricing.input_cache_read`、`pricing.input_cache_write` | prompt cache 成本核算。 |
| `top_provider.context_length` | 供应商实际限制。 |
| `top_provider.max_completion_tokens` | 输出长度限制。 |
| `top_provider.is_moderated` | 内容审核提示和路由过滤。 |
| `per_request_limits` | 单请求或模型限额。 |
| `supported_parameters` | 自动表单、请求校验、`require_parameters` 路由依据。 |
| `default_parameters` | 控制台默认值和 preset 初始值。 |

AIYolo 建议：把模型目录做成数据库实体，而不是硬编码配置。Provider、真实模型、别名、价格、能力标签、上下文和废弃状态都应该可在控制台维护。

## 5. 供应商目录

OpenRouter 的供应商页展示每个 provider 的：

- 地区或区域标签，例如 US、EU、SG、CN。
- 模型数量、免费模型数量。
- 日 token、月 token 等真实流量指标。
- 数据政策标签：No training、ZDR、BYOK、Moderation required。
- 每个供应商可进一步下钻到支持模型、端点变体、服务条款。

AIYolo 建议最小字段：

| 字段 | 用途 |
| --- | --- |
| `slug` | 路由字段使用的稳定标识。 |
| `display_name` | 控制台展示。 |
| `region` | 数据驻留和延迟选择。 |
| `protocol` | OpenAI、Anthropic、OpenRouter、DashScope、本地等。 |
| `supports_byok` | 是否允许用户自带 key。 |
| `supports_zdr` | 是否可用于 ZDR 路由。 |
| `data_collection_policy` | allow/deny 路由依据。 |
| `moderation_required` | 内容策略和错误解释。 |
| `health_status` | 自动路由和 fallback 输入。 |
| `latency_p50/p90/p99` | 延迟排序。 |
| `throughput_p50/p90/p99` | 吞吐排序。 |
| `terms_url` | 第三方条款追溯。 |

## 6. 路由能力

### 6.1 默认路由

OpenRouter 默认策略是价格优先的负载均衡，同时考虑近期可用性：

- 优先排除最近短时间内有明显故障的端点。
- 在稳定端点中按低价优先，并用价格倒数平方进行加权。
- 失败时按候选顺序 fallback。
- 如果用户指定 `provider.sort` 或 `provider.order`，显式排序会取代默认负载均衡。

AIYolo MVP 可先实现：健康端点过滤、低价优先、失败 fallback、失败原因记录。后续再实现加权随机和滚动窗口质量分。

### 6.2 请求级 fallback

OpenRouter 通过 `models` 数组支持跨模型 fallback：

```json
{
  "models": ["anthropic/claude-sonnet", "openai/gpt-4o-mini"],
  "messages": []
}
```

触发条件包括上下文校验失败、内容审核、rate limit、provider downtime。最终使用哪个模型，响应体 `model` 字段会反映真实服务模型。

AIYolo 建议：将单模型 provider fallback 和多模型 fallback 分开建模，审计日志记录每次 attempt 的 provider、model、status、latency、cost estimate。

### 6.3 Provider preferences

OpenRouter 的 `provider` 对象是复刻重点：

| 字段 | 作用 | 优先级 |
| --- | --- | --- |
| `order` | 指定供应商尝试顺序。 | P1 |
| `allow_fallbacks` | 是否允许候选之外 fallback。 | P1 |
| `require_parameters` | 只选择支持全部请求参数的端点。 | P1 |
| `data_collection` | `allow` 或 `deny`，过滤会收集数据的供应商。 | P1 |
| `zdr` | 只路由到 Zero Data Retention 端点。 | P1 |
| `enforce_distillable_text` | 只路由到允许文本蒸馏的模型。 | P3 |
| `only` | 本请求只允许这些 provider。 | P1 |
| `ignore` | 本请求跳过这些 provider。 | P1 |
| `quantizations` | 过滤 int4、int8、fp8、fp16、bf16 等量化端点。 | P2 |
| `sort` | 按 price、throughput、latency 排序。 | P1 |
| `sort.partition` | 多模型 fallback 时按 model 分组或全局排序。 | P2 |
| `preferred_min_throughput` | 以 p50/p75/p90/p99 偏好最低吞吐。 | P2 |
| `preferred_max_latency` | 以 p50/p75/p90/p99 偏好最高延迟。 | P2 |
| `max_price` | 限制 prompt、completion、request、image 等价格。 | P1 |

### 6.4 模型后缀和虚拟变体

OpenRouter 用模型 slug 后缀表达路由偏好：

| 后缀 | 含义 | AIYolo 建议 |
| --- | --- | --- |
| `:free` | 使用免费变体，通常有更低限额或可用性。 | P2，可映射到免费池。 |
| `:extended` | 使用更长上下文变体。 | P2，可映射到 context tier。 |
| `:thinking` | 使用扩展 reasoning 变体。 | P2，可映射 reasoning 参数。 |
| `:online` | 旧 web search 变体，已建议迁移到 server tool。 | 不建议新增，只做兼容。 |
| `:nitro` | 等价于 `provider.sort = "throughput"`。 | P2。 |
| `:floor` | 等价于 `provider.sort = "price"`。 | P2。 |
| `:exacto` | 质量优先，偏好工具调用可靠性更强的 provider。 | P3，需质量评分数据。 |

### 6.5 高级路由

OpenRouter 还暴露 auto router、free router、latest resolution、pareto router、body builder 等虚拟路由。AIYolo 可以把它们抽象为“router profile”：

- `direct`：指定模型直连。
- `fallback`：模型数组 fallback。
- `cost_first`：价格优先。
- `latency_first`：延迟优先。
- `throughput_first`：吞吐优先。
- `quality_first`：工具调用成功率或人工评分优先。
- `free_only`：只走免费模型池。
- `latest`：别名解析到最新稳定模型。

## 7. 请求参数和响应规范

### 7.1 常规参数

OpenRouter 接收 OpenAI 风格参数，并尽量透传或转换到供应商格式：

- 采样：`temperature`、`top_p`、`top_k`、`min_p`、`top_a`、`seed`。
- 惩罚：`frequency_penalty`、`presence_penalty`、`repetition_penalty`。
- 长度：`max_tokens`、`max_completion_tokens`、`stop`。
- 日志概率：`logit_bias`、`logprobs`、`top_logprobs`。
- 输出格式：`response_format`、`structured_outputs`。
- 工具：`tools`、`tool_choice`、`parallel_tool_calls`。
- 路由：`models`、`route`、`provider`。
- 成本/性能：`service_tier`。
- 业务跟踪：`user`、`session_id`、`trace`。
- 调试：`debug.echo_upstream_body`。

AIYolo 建议：参数解析时保留原始请求 JSON，归一化为内部 `CanonicalRequest`，并将未知参数放入 provider-specific map，避免破坏新模型特性。

### 7.2 Streaming

OpenRouter 对所有模型提供 SSE streaming：

- 请求体 `stream: true`。
- 流中可能出现 SSE comment，例如处理中的 keepalive，需要客户端忽略。
- 最终 chunk 包含 usage。
- 所有响应返回 `X-Generation-Id`，方便回查。
- 支持客户端断开后取消上游流，部分 provider 可立即停止计费。
- pre-stream 错误返回标准 HTTP 错误，mid-stream 错误以 SSE data 事件返回，并使用 `finish_reason: "error"` 终止。

AIYolo 已有设计强调 SSE 是一等能力，应继续保持：流式响应边转发、边累计 token、边记录最终状态。

### 7.3 Usage accounting

OpenRouter 在每个响应中自动包含 usage，不再依赖额外参数：

| 字段 | 含义 |
| --- | --- |
| `prompt_tokens` | 输入 token。 |
| `completion_tokens` | 输出 token。 |
| `total_tokens` | 总 token。 |
| `prompt_tokens_details.cached_tokens` | cache read token。 |
| `prompt_tokens_details.cache_write_tokens` | cache write token。 |
| `prompt_tokens_details.audio_tokens`、`video_tokens` | 多模态输入 token。 |
| `completion_tokens_details.reasoning_tokens` | reasoning token。 |
| `completion_tokens_details.audio_tokens`、`image_tokens` | 多模态输出 token。 |
| `cost` | 本次请求计费。 |
| `is_byok` | 是否使用 BYOK。 |
| `cost_details.upstream_inference_cost` | BYOK 上游估算成本。 |
| `server_tool_use.web_search_requests` | server tool 用量。 |

AIYolo 建议：usage ledger 不只存总 token，还要存详细分解，后续才能支持 prompt cache、reasoning、音频、图片、web search 独立计费。

### 7.4 Error shape

OpenRouter 标准错误结构：

```json
{
  "error": {
    "code": 403,
    "message": "Request blocked",
    "metadata": {}
  }
}
```

常见状态码：400、401、402、403、408、429、502、503。429 和 503 可能带 `Retry-After`。Provider 错误和 moderation 错误会在 `metadata` 中附带供应商和原始错误摘要。

AIYolo 建议：错误响应同时满足 OpenAI-compatible、Anthropic-compatible 和内部审计需要，不能只返回上游原始 body。

## 8. 多模态能力

OpenRouter 支持的模态包括：

| 能力 | API 形态 | AIYolo 优先级 |
| --- | --- | --- |
| 图片理解 | `messages[].content[]` 中的 `image_url`，支持 URL 或 base64。 | P1 |
| PDF | `file` content type，支持文本提取和 OCR/原生处理。 | P2 |
| 音频输入 | `input_audio` content type，或 STT endpoint。 | P2 |
| 视频输入 | `video_url` content type，供应商差异较大。 | P3 |
| 图片生成 | 模型输出图片，或 `openrouter:image_generation` server tool。 | P2 |
| 视频生成 | `/api/v1/videos` 异步 job。 | P3 |
| TTS | `/api/v1/audio/speech`。 | P2 |
| STT | `/api/v1/audio/transcriptions`。 | P2 |
| Embeddings | 文本批量 embedding，部分模型支持图片 embedding。 | P1/P2 |

实现建议：先把请求/响应的 content part 抽象做好，文件存储和对象 URL 作为独立服务，不要把 base64 大对象直接塞进审计主表。

## 9. 工具、插件和结构化输出

### 9.1 用户定义工具

OpenRouter 标准化 OpenAI function calling：

- `tools[].type = "function"`。
- 模型返回 `tool_calls`，客户端负责执行工具并把结果作为 `role: "tool"` 消息回传。
- `tool_choice` 支持 `auto`、`none`、`required` 或强制指定某个函数。
- `parallel_tool_calls` 控制是否允许并行调用。
- 流式工具调用需要正确处理 delta 中的 tool call 参数。
- OpenRouter 会跟踪不同 provider 的工具调用可靠性，并用于质量优先路由。

AIYolo P0/P1 应完整透传 OpenAI 工具调用结构，P2 再做工具调用可靠性统计。

### 9.2 Server tools

OpenRouter 的 server tools 是由平台执行的工具，模型可在一次请求中调用 0 到 N 次：

| 工具 | 类型 | 能力 | 优先级 |
| --- | --- | --- | --- |
| Web Search | `openrouter:web_search` | 搜索实时信息，支持 engine、结果数量、域名过滤、地域偏置、引用。 | P2 |
| Web Fetch | `openrouter:web_fetch` | 抓取并提取 URL/PDF 内容，支持 engine、最大次数、token 截断、域名允许/阻止。 | P2 |
| Datetime | `openrouter:datetime` | 返回当前日期时间。 | P2 |
| Image Generation | `openrouter:image_generation` | 根据模型调用生成图片，返回图片 URL。 | P2/P3 |

server tools 与用户定义工具可同时存在。AIYolo 可先把 server tools 作为内部 tool executor，再逐步支持外部搜索引擎 BYOK。

### 9.3 Plugins

OpenRouter plugins 更像一次性请求/响应处理器：

- `web`：旧 web search 插件，已建议迁移到 server tool。
- `file-parser`：文件/PDF 解析。
- `response-healing`：修复 JSON 或 structured output 格式。
- `context-compression`：middle-out 压缩过长上下文。

AIYolo 建议：插件管线和路由管线统一记录到审计中，便于后续输出 router metadata。

### 9.4 Structured outputs

OpenRouter 支持：

- `response_format: { "type": "json_object" }`，基础 JSON mode。
- `response_format: { "type": "json_schema", "json_schema": { ... } }`，严格 schema mode。
- 通过 `require_parameters: true` 确保 provider 支持 structured output。
- streaming 场景下输出可逐步形成合法 JSON。
- response healing 可降低非流式 JSON 失败率。

AIYolo 建议：控制台的 Request Builder 应能生成 JSON Schema，并在模型目录里显示支持状态。

## 10. 缓存、成本和可靠性

### 10.1 Response caching

OpenRouter 的响应缓存是平台层缓存，模型无关：

- 请求头 `X-OpenRouter-Cache: true` 启用。
- `X-OpenRouter-Cache-TTL` 设置 TTL，范围 1 到 86400 秒。
- `X-OpenRouter-Cache-Clear: true` 清理当前 cache key。
- 响应头 `X-OpenRouter-Cache-Status: HIT|MISS`、`X-OpenRouter-Cache-Age`、`X-OpenRouter-Cache-TTL`。
- cache key 包括 API key、model、endpoint type、streaming mode、请求体 hash。
- cache scope 是 API key，不跨 key 共享。
- cache hit 免费，usage 置零，不触达 provider rate limit。
- 支持 chat completions、responses、messages、embeddings。
- account-level ZDR 开启时不可用，因为缓存需要临时保存响应。

AIYolo 建议 P1 实现非流式缓存，P2 支持流式 replay。缓存表或 Redis key 需要记录响应体、headers、TTL、owner key hash、endpoint type。

### 10.2 Prompt caching

OpenRouter 统一处理供应商 prompt cache：

- 自动缓存：OpenAI、DeepSeek、Gemini 等供应商部分模型可自动启用。
- 显式缓存：Anthropic、Alibaba Qwen 等可用 `cache_control` 设置缓存断点。
- Anthropic 支持顶层 `cache_control` 自动缓存，也支持 content block 级断点。
- Anthropic TTL 可为 5 分钟或 1 小时，不同 TTL 写入成本不同。
- OpenRouter 使用 provider sticky routing 增加 cache hit，同账号、同模型、同会话尽量路由到同一 provider endpoint。
- usage 中返回 `cached_tokens` 和 `cache_write_tokens`。

AIYolo 建议：短期先透传 `cache_control` 并解析 usage，后续再实现 sticky routing。

### 10.3 Zero completion insurance

OpenRouter 对空输出或错误 finish reason 的请求提供自动保护：如果没有 completion token 且 finish reason 为空或错误，则不扣费。

AIYolo 建议 P1 加入类似规则，避免用户为上游异常支付网关余额。

### 10.4 Service tiers

OpenRouter 支持 `service_tier` 控制成本和延迟：

- `flex`：更低成本，较高延迟或较低可用性。
- `priority`：更快或更高优先级，成本更高。
- Chat Completions、Responses、Anthropic Messages 均可使用，响应会返回实际使用 tier。

AIYolo 可把它抽象为 provider endpoint 的 capacity tier，并计入路由条件。

## 11. 账号、密钥、Workspace 和 BYOK

### 11.1 API Key

OpenRouter API Key 支持：

- 名称。
- credit limit。
- daily、weekly、monthly reset。
- disabled 状态。
- `include_byok_in_limit`。
- all-time、daily、weekly、monthly usage。
- BYOK usage 分项。
- Management API programmatic CRUD。

AIYolo 已有 API Keys 页面，应补齐周期额度、BYOK 是否计入额度、key hash 查询、禁用和轮换审计。

### 11.2 OAuth PKCE

OpenRouter 支持让第三方应用跳转用户授权，交换得到用户控制的 API Key：

1. 应用把用户送到 `/auth?callback_url=...&code_challenge=...`。
2. 用户授权后带 `code` 回到应用。
3. 应用调用 `/api/v1/auth/keys` 换取 key。

AIYolo 如果要支持外部插件生态，可以 P3 实现。自部署场景下可先用本地账号/API Key 代替。

### 11.3 Workspaces

OpenRouter workspace 将以下设置隔离：

- API Keys。
- Guardrails。
- BYOK provider keys。
- Routing defaults。
- Presets。
- Plugins。
- Observability integrations。
- Members。

账号级设置包括 Activity/Logs、Credits/Billing、Organization、Management Keys、Privacy、Preferences。

AIYolo 建议把现有组织/项目模型向 workspace 收敛：workspace 是环境隔离边界，适合区分 dev、staging、production、团队或客户。

### 11.4 BYOK

OpenRouter BYOK 能力：

- 用户提供供应商密钥，OpenRouter 加密保存并在路由时使用。
- BYOK key 分 prioritized 和 fallback 两组。
- prioritized key 优先于 OpenRouter shared capacity。
- 可设置 “always use for this provider”，禁止 fallback 到共享 capacity。
- 多个 BYOK key 可按顺序尝试。
- BYOK key 可按模型、OpenRouter API key、workspace member 设置过滤条件。
- 支持 Azure、AWS Bedrock、Google Vertex 等特殊凭据格式。
- Activity 详情可查看 provider responses，便于调试 400、401、403、429、500。

AIYolo 建议 P1 做 BYOK，但只先支持通用 Bearer key；Azure/AWS/GCP 的特殊凭据可 P2/P3。

## 12. Guardrails、隐私和合规

### 12.1 Guardrails

OpenRouter guardrails 可包含：

- Budget limit，按日/周/月重置。
- Model allowlist。
- Provider allowlist。
- Per-model-group ZDR。
- Prompt injection/jailbreak 防护。
- Custom regex content filters，支持 redact 或 block。

Guardrails 可分配给成员或 API key。层级合并规则是更严格者生效：allowlist 取交集，ZDR 使用 OR，预算分别检查，较低限制先拦截。

AIYolo 建议 P1 实现 budget、model/provider allowlist、简单 regex block/redact。P2 加 prompt injection 模式库和 eligibility preview。

### 12.2 Prompt injection detection

OpenRouter 的 prompt injection detection：

- 本地正则扫描用户输入，低延迟。
- 动作包括 flag、redact、block。
- 多个 guardrail 同时生效时，优先级为 block > redact > flag。
- 包含 typoglycemia、base64/hex 解码、字符间隔规避检测。
- 命中时可返回 403，并在 router metadata 的 pipeline 中写明 stage。

AIYolo 可先实现 flag/redact/block 三态和审计事件，再迭代规避检测。

### 12.3 ZDR 和数据政策

OpenRouter 的隐私控制包括：

- Account-level privacy settings。
- Guardrail-level ZDR。
- Per-request `provider.zdr`。
- `provider.data_collection = "deny"`，过滤会收集数据的 provider。
- ZDR 分模型组：Anthropic、OpenAI、Google、non-frontier。
- OpenRouter 自身默认不保留 prompt，除非用户启用输入输出日志。
- ZDR endpoint 列表可通过 `/api/v1/endpoints/zdr` 程序化获取。

AIYolo 建议：把 provider 数据政策作为可维护元数据，路由时强制过滤，审计日志记录过滤原因。

### 12.4 Sovereign AI

OpenRouter 企业版支持 EU in-region routing，使用 `https://eu.openrouter.ai`，保证请求在欧盟区域内处理。配合 ZDR 和 data collection deny 可形成完整数据驻留策略。

AIYolo 自部署天然适合私有区域部署。若未来多 region，需要 base URL、workspace policy、provider region 三者共同决定路由。

## 13. 观测、日志和导出

### 13.1 Activity 和 Usage

OpenRouter Activity 页面包含：

- Spend。
- Tokens。
- Requests。
- 时间范围：1 hour、1 day、1 month、1 year。
- 分组：Model、API Key、Creator。
- 导出：CSV、PDF。
- 详细页可展开某个指标，按 key/model/member 细分。

AIYolo 已规划 Usage 和 Audit 页面，应补充“指标卡下钻”和导出能力。

### 13.2 Input & Output Logging

OpenRouter 可在 Observability 中开启输入输出日志：

- Logs 页面可查看 Prompt 和 Completion。
- 仅启用后产生的 generation 会存全文。
- 组织中只有 admin 可查看内容。
- 与 Broadcast 不同，它存储在 OpenRouter 平台内。
- 与“数据折扣日志”不同，它不会允许平台用数据做产品改进或训练。
- EU routing 当前跳过 input/output logging。

AIYolo 建议：默认只存脱敏摘要，全文日志作为 workspace 级开关，配合保留期和访问审计。

### 13.3 Broadcast

Broadcast 将请求 trace 异步发送到外部观测平台：

- 支持 Langfuse、LangSmith、Datadog、Braintrust、OpenTelemetry Collector、S3、Snowflake、ClickHouse、PostHog、Sentry、W&B Weave 等。
- 每个 destination 有加密凭据。
- 可按 API key 过滤。
- 支持 sampling rate。
- Privacy Mode 可移除 prompt 和 completion，只发送 token、cost、latency、model、metadata。
- Trace 数据包括 request/response、token、cost、timing、model、provider、tool usage。
- 请求可携带 `user`、`session_id`、`trace` 元数据。

AIYolo P2 可先支持 OpenTelemetry 和 Webhook，P3 再支持具体 SaaS destination。

### 13.4 Router metadata

OpenRouter 的 router metadata 是调试利器：

- 请求头 `X-OpenRouter-Experimental-Metadata: enabled` 启用。
- 支持 chat completions、messages、responses、legacy completions。
- streaming 时在最终 chunk 或 terminal event 返回。
- 成功响应包含 `openrouter_metadata`。
- 错误响应也可包含 routing snapshot，但认证/限流等早期错误不一定有。
- cache hit 不返回 metadata。

建议 AIYolo 的 metadata 至少包含：

| 字段 | 说明 |
| --- | --- |
| `requested` | 客户端请求的模型或 alias。 |
| `strategy` | direct、fallback、cost_first、latency_first 等。 |
| `region` | 网关或 provider region。 |
| `summary` | 人类可读路由摘要。 |
| `attempt` | 成功 attempt 序号。 |
| `is_byok` | 是否 BYOK。 |
| `endpoints.total` | 候选端点数量。 |
| `endpoints.available[]` | 候选 provider/model/selected。 |
| `attempts[]` | 每次 provider 尝试的状态。 |
| `pipeline[]` | guardrail、plugin、server_tools、response_healing、context_compression 等阶段。 |

## 14. 产品页面能力

OpenRouter 不只是 API，还有一组能提高开发者自助效率和信任度的页面。

| 页面 | 能力 | AIYolo 建议 |
| --- | --- | --- |
| Chat/Playground | 新建对话、选择模型、内置示例 prompt、模型分组如 coding/reasoning/roleplay。 | P2，做成内部调试台。 |
| Request Builder | 选择模型、messages、参数、provider preferences，生成 cURL/Python/TypeScript。 | P1/P2，对接控制台非常有用。 |
| Models | 模型目录、筛选、排序、详情。 | P0。 |
| Providers | 供应商目录、地区、能力标签、用量。 | P0/P1。 |
| Rankings | 基于真实用量和 benchmark 的模型排行、市场份额、tool calls、图片和音频量。 | P3，私有部署可做内部排行。 |
| Apps | 通过 attribution headers 聚合应用/Agent 排行和分类。 | P3，公有平台才需要。 |
| Pricing | 免费/付费/企业计划、平台费、BYOK 限额、功能矩阵。 | 私有部署可转为系统额度和成本说明。 |
| Enterprise | SLA、SSO、统一报告、BYOK、ZDR、in-region、broadcast、组织支持。 | 按企业部署需求拆分实现。 |

## 15. Console 页面映射

现有设计文档已有 Dashboard、Usage、Audit、API Keys、Providers、Models、Proxies、Billing、Users、Settings。复刻 OpenRouter 后建议扩展为：

| 页面 | 新增重点 |
| --- | --- |
| Dashboard | 增加 cache hit rate、fallback rate、BYOK rate、guardrail block rate、provider health。 |
| Usage | 支持 spend/tokens/requests 三指标切换，下钻 model/key/user/provider/workspace。 |
| Audit | 展示 router attempts、pipeline stages、cache status、guardrail result、debug metadata。 |
| Logs | 可选全文 prompt/completion，带保留期、脱敏和权限控制。 |
| API Keys | 周期额度、limit reset、include BYOK、management keys、禁用/轮换。 |
| Workspaces | 环境隔离、成员、默认路由、默认 guardrail、observability。 |
| Providers | provider data policy、ZDR、BYOK 支持、健康、延迟、吞吐。 |
| Models | 模型能力、价格、上下文、参数支持、别名、废弃状态。 |
| Routing | 默认策略、provider allow/ignore、sort、fallback、max price、region。 |
| BYOK | provider key、优先/备用、过滤条件、特殊云凭据。 |
| Guardrails | budget、allowlist、ZDR、regex filters、prompt injection。 |
| Presets | system prompt、模型、参数、provider preferences、cache setting、版本历史。 |
| Observability | input/output logging、broadcast destinations、sampling、privacy mode。 |
| Request Builder | 生成可复制请求，展示最终 JSON 和代码片段。 |
| Playground | 内部模型测试、streaming、tools、structured output 验证。 |

## 16. 数据模型影响

建议补齐或规划以下表/实体：

- `workspaces`
- `workspace_members`
- `api_keys`
- `management_api_keys`
- `providers`
- `provider_endpoints`
- `provider_credentials`
- `models`
- `model_aliases`
- `model_prices`
- `model_capabilities`
- `routing_policies`
- `routing_attempts`
- `presets`
- `preset_versions`
- `guardrails`
- `guardrail_assignments`
- `guardrail_filters`
- `usage_ledger`
- `generations`
- `generation_chunks` 或对象存储引用
- `audit_events`
- `response_cache_entries`
- `prompt_cache_observations`
- `observability_destinations`
- `broadcast_deliveries`
- `activity_exports`
- `provider_health_windows`
- `provider_performance_windows`

## 17. 实施路线建议

### P0: 可用网关闭环

- OpenAI-compatible Chat Completions。
- Anthropic Messages 兼容，满足 Claude Code 基础使用。
- API Key 认证、额度、禁用。
- Models 和 Providers 数据库化。
- 基础 provider routing：direct、lowest price、fallback。
- SSE streaming、usage 解析、generation id。
- Usage ledger、Audit events。
- Dashboard、Usage、Audit、API Keys、Providers、Models 页面。

### P1: 生产可用

- `provider.order/only/ignore/allow_fallbacks/require_parameters/max_price/data_collection/zdr`。
- 跨模型 `models` fallback。
- 周期额度、management API keys、key rotation。
- Workspaces。
- BYOK 通用实现。
- Guardrails 基础版：budget、model/provider allowlist、regex block/redact。
- Response cache 非流式版。
- Prompt cache 透传和 usage 分解。
- Input/output logging 可选开关。
- Activity 导出 CSV。
- Request Builder 基础版。

### P2: 高阶能力

- Router metadata。
- Server tools：web_search、web_fetch、datetime。
- Structured outputs 和 response healing。
- Context compression。
- Multimodal：image input、PDF、TTS、STT、embeddings。
- Broadcast：OpenTelemetry、Webhook、S3。
- Presets 和版本历史。
- Provider performance rolling windows，latency/throughput sort。
- Playground。

### P3: 平台化和企业高级能力

- Apps/Rankings 内部版或公有平台版。
- OAuth PKCE。
- SSO/SAML。
- EU/in-region 多区域路由。
- 高级 BYOK：Azure、Bedrock、Vertex 特殊凭据。
- Quality-first routing、Exacto-like tool calling quality score。
- Video generation 异步 job。
- PDF/PDF OCR 深度处理。
- SLA、企业报表、PDF export。

## 18. 与 AIYolo 现有设计的关系

现有 `docs/api-gateway-console-design.md` 的方向是正确的：Go 服务端渲染、HTMX、PostgreSQL、API Gateway、Billing、Audit、Proxy Profiles。OpenRouter 调研建议在这个基础上补三类内容：

1. 模型和供应商的能力元数据要更丰富，支持参数、模态、价格、数据政策、性能窗口都要进入路由决策。
2. 控制台不只是 CRUD，还要提供 Request Builder、Playground、Logs、Routing、Guardrails、BYOK、Observability 这些“运营面”。
3. 审计日志要升级为 generation 级事实表，记录请求、attempt、usage、cache、guardrail、pipeline 和最终成本。

## 19. 暂不建议复刻的内容

- 公有 Apps 排行和公开流量榜，除非 AIYolo 未来做开放平台。
- 完整 Agent SDK，除非要从 Gateway 扩展到应用开发框架。
- 复杂供应商 TOS 聚合页面，初期保留链接即可。
- 所有 Broadcast SaaS destination，先支持标准 OpenTelemetry/Webhook。
- 视频生成和大型多模态存储，等文本/图片/embedding 稳定后再做。

## 20. 配套文档

- [docs/gateway-routing-design.md](./gateway-routing-design.md)：详细设计路由策略、provider endpoint、health window 和 fallback 状态机。
- [docs/gateway-billing-ledger-design.md](./gateway-billing-ledger-design.md)：详细设计 usage ledger、价格快照、cache 成本、BYOK 成本和退款规则。
- [docs/gateway-console-pages.md](./gateway-console-pages.md)：把新增页面拆成 Go handler、template、HTMX partial 和表单字段。
- [docs/gateway-openapi-compat.md](./gateway-openapi-compat.md)：列出 AIYolo 要兼容的 OpenAI、Anthropic、OpenRouter API 字段和错误格式。