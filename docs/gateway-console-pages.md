# AIYolo Gateway Console 页面设计

本文档把 AIYolo Console 的页面拆到 Go handler、template、HTMX partial、关键查询和表单字段级别，作为后续实现新增页面与扩展现有页面的落地规范。

相关文档：

- [docs/api-gateway-console-design.md](./api-gateway-console-design.md)
- [docs/openrouter-feature-inventory.md](./openrouter-feature-inventory.md)
- [docs/gateway-routing-design.md](./gateway-routing-design.md)
- [docs/gateway-billing-ledger-design.md](./gateway-billing-ledger-design.md)

当前代码锚点：

- 路由入口与 page data 装配在 [internal/console/handlers.go](../internal/console/handlers.go)
- 导航、语言切换与页头文案在 [internal/console/page.go](../internal/console/page.go)
- 基础布局在 [internal/console/templates/layout.html](../internal/console/templates/layout.html)

## 1. 设计目标

- 保持纯 Go 服务端渲染和 HTMX 增量交互，不引入前端构建链。
- 新页面默认复用现有 rail layout、hero header、flash notice 和 fragment 刷新模式。
- 页面设计直接映射现有数据抽象，优先落在 `storage.Store` 的查询与命令上。
- 把“当前已实现”和“目标新增页面”拆开描述，避免文档假设与代码现实脱节。

## 2. 页面设计约定

### 2.1 路由约定

- 页面级 GET 路由统一挂在 `/console/*`。
- 修改动作优先使用同页 POST 路由，不额外拆前端 JSON API。
- 历史兼容页可以暂时保留 redirect，例如当前 `billing` 和 `users`。

### 2.2 Template 约定

每个页面默认采用双模板结构：

- 整页模板：`{{define "pageName"}}`
- 局部刷新模板：`{{define "pageName-content"}}`

例如：

- `providers` + `providers-content`
- `models` + `models-content`
- `apiKeys` + `apiKeys-content`

要求：

- 全页模板只包裹 `layout-start`、content partial、`layout-end`。
- fragment 模板必须有稳定 DOM id，供 `hx-target` 和 `hx-swap="outerHTML"` 使用。

### 2.3 HTMX 约定

现有页面已经形成统一模式，新页面应复用：

- 表单同时保留 `method="post" action="..."` 与 `hx-post="..."`。
- `hx-target` 指向本页 content 容器，例如 `#providers-content`。
- `hx-disabled-elt="find button"` 用于防止重复提交。
- GET 型联动表单使用 `hx-get` 重新渲染当前 content partial。

### 2.4 数据装配约定

page data 装配由 handler 侧统一完成，避免模板内做复杂逻辑。

现有模式：

- `apiKeysPageData`
- `providersPageData`
- `modelsViewData`
- `proxiesViewData`

建议新增页面也遵循：

- `XxxPageData(ctx)` 负责取数。
- `XxxViewData(ctx, r)` 负责把查询结果转成视图层需要的衍生字段。

### 2.5 语言与导航约定

来自 [internal/console/page.go](../internal/console/page.go) 的现有约束：

- 页面标题、eyebrow、description 要支持中英双语。
- 侧栏导航由 `consoleNavItems` 输出。
- 当前控制台固定使用中文，不再维护中英切换。

新增页面时，必须同步补齐：

- `consoleNavItems`
- `pageTitleLocalized`
- `pageEyebrowLocalized`
- `pageDescriptionLocalized`

## 3. 信息架构

### 3.1 当前页面状态

| 页面 | 路由 | 状态 | 说明 |
| --- | --- | --- | --- |
| Login | `/console/login` | 已实现 | 本地密码登录 + OAuth 入口。 |
| Dashboard | `/console/` | 已实现 | 24h 请求、错误、成本与最近流量。 |
| Usage | `/console/usage` | 已实现 | Usage 与 Billing 合并页。 |
| Audit | `/console/audit` | 已实现 | 请求级审计时间线。 |
| API Keys | `/console/api-keys` | 已实现 | API Key 创建与列表。 |
| Providers | `/console/providers` | 已实现 | Provider 创建、OpenRouter 导入。 |
| Models | `/console/models` | 已实现 | 模型别名、模型测试。 |
| Proxies | `/console/proxies` | 已实现 | Proxy Profile 新增/编辑。 |
| Settings | `/console/settings` | 已实现 | 登录方式、OAuth、Users 合并页。 |
| Billing | `/console/billing` | Redirect | 当前跳转到 Usage 的 spend 区块。 |
| Users | `/console/users` | Redirect | 当前跳转到 Settings 的 identity 区块。 |

### 3.2 目标新增页面

| 页面 | 建议路由 | 优先级 | 说明 |
| --- | --- | --- | --- |
| Logs | `/console/logs` | P1 | 全文 prompt/completion 与保留期控制。 |
| Workspaces | `/console/workspaces` | P1 | 环境隔离、成员与默认路由。 |
| Routing | `/console/routing` | P1 | 默认策略、provider preferences、fallback。 |
| BYOK | `/console/byok` | P1 | Provider 凭据、优先/备用顺序与过滤条件。 |
| Guardrails | `/console/guardrails` | P1 | budget、allowlist、regex 和 ZDR。 |
| Observability | `/console/observability` | P2 | logging、broadcast、sampling、privacy mode。 |
| Request Builder | `/console/request-builder` | P1/P2 | 组装请求并生成 cURL/Python/TS 代码。 |
| Playground | `/console/playground` | P2 | 内部调试台，验证 streaming、tools、schema。 |

## 4. 当前已实现页面拆解

### 4.1 Login

| 维度 | 设计 |
| --- | --- |
| Handler | `loginPage`、`login`、`oauthLogin`、`oauthCallback` |
| Template | [internal/console/templates/login.html](../internal/console/templates/login.html) |
| Layout | 独立登录布局，不走侧栏模板。 |
| 查询 | `consoleAuthSettings`、可用 OAuth providers。 |
| 表单 | 本地密码登录表单。 |

表单字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `email` | 是 | 管理员邮箱，默认填配置值。 |
| `password` | 是 | 本地管理员密码。 |

扩展建议：

- 登录页应增加当前可用登录方式摘要。
- 当 OAuth 已启用但未配置 allowlist 时，应显示高风险提示。

### 4.2 Dashboard

| 维度 | 设计 |
| --- | --- |
| Handler | `dashboard` |
| Template | [internal/console/templates/dashboard.html](../internal/console/templates/dashboard.html) |
| Partial | 无单独 partial，整页渲染即可。 |
| 查询 | `store.Dashboard()` + `store.ListUsage(200)` |
| 核心区块 | 24h 指标卡、模型成本表、最近请求、最近审计。 |

后续扩展：

- 增加 cache hit rate、fallback rate、BYOK rate、guardrail block rate。
- Provider 健康状态卡可作为右侧摘要区。

### 4.3 Usage

| 维度 | 设计 |
| --- | --- |
| Handler | `usage` |
| Template | [internal/console/templates/usage.html](../internal/console/templates/usage.html) |
| 查询 | `store.ListUsage(300)`、`store.BillingOverview()` |
| 现状 | Usage 与 Billing 已合并。 |

主要区块：

- 30d spend/request/active identities 指标。
- Model spend 表。
- Recent billed requests 列表。
- Provider/API Key/User 三种 cost center 摘要。
- Usage ledger 明细表。

新增字段需求：

- `cache_status`
- `is_byok`
- `adjusted_cost_micro_cents`
- `reasoning_tokens`
- `web_search_requests`

这些字段落地后，Usage 页面应新增：

- 缓存命中率卡片
- BYOK 占比卡片
- adjustment 明细展开
- filter bar：按 model、provider、api_key、workspace、status、stream、estimated 过滤

### 4.4 Audit

| 维度 | 设计 |
| --- | --- |
| Handler | `audit` |
| Template | [internal/console/templates/audit.html](../internal/console/templates/audit.html) |
| 查询 | `store.ListAudit(100)` |
| 现状 | request 级列表，字段较薄。 |

当前列：

- 时间
- 事件类型
- request id
- model/provider
- status
- error code
- proxy profile
- latency

目标扩展：

- generation 级和 attempt 级双视图。
- filter：`event_type`、`provider_id`、`failure_class`、`stream`、`workspace_id`。
- detail drawer：显示 filters、sorting、attempt timeline、router metadata。

### 4.5 API Keys

| 维度 | 设计 |
| --- | --- |
| Handler | `apiKeys`、`createAPIKey` |
| Template | [internal/console/templates/api_keys.html](../internal/console/templates/api_keys.html) |
| Partial | `apiKeys-content` |
| 查询 | `ListAPIKeys`、`ListModelRoutes`、`ListProviders` |
| 行为 | 成功创建后明文 key 只显示一次。 |

当前表单字段：

| 字段 | 说明 |
| --- | --- |
| `name` | 调用方名称 |
| `kind` | `live` 或 `test` |
| `allowed_protocols` | 逗号分隔协议白名单 |
| `allowed_models` | 逗号分隔 public model 白名单 |
| `rpm_limit` | RPM 限流 |
| `tpm_limit` | TPM 限流 |
| `concurrent_limit` | 并发上限 |
| `daily_budget_cents` | 日预算 |
| `monthly_budget_cents` | 月预算 |

P1 需要补齐：

- `disabled`
- `expires_at`
- `include_byok_in_limit`
- 周/月 reset 策略
- key rotation 与 revoke 流程
- management keys

建议新增动作：

- `POST /console/api-keys/{id}/disable`
- `POST /console/api-keys/{id}/rotate`
- `POST /console/api-keys/{id}/reveal-prefix-audit`

### 4.6 Providers

| 维度 | 设计 |
| --- | --- |
| Handler | `providers`、`createProvider`、`createOpenRouter`、`syncProviderModels` |
| Template | [internal/console/templates/providers.html](../internal/console/templates/providers.html) |
| Partial | `providers-content` |
| 查询 | `ListProviders`、`ListProxyProfiles` |
| 现状 | Provider 级管理，尚未拆 endpoint。 |

当前表单字段：

| 字段 | 说明 |
| --- | --- |
| `id` | Provider ID |
| `name` | 展示名称 |
| `base_url` | 上游基础地址 |
| `protocol` | `openai` 或 `anthropic` |
| `default_proxy_id` | 默认代理 |
| `master_key` | 上游系统密钥 |
| `timeout_seconds` | 超时 |
| `status` | 启用或停用 |

建议扩展为两层：

- Provider 卡片：品牌、协议族、共享能力、凭据边界。
- Endpoint 子表：区域、状态、健康窗口、性能指标、数据策略。

P1 新增字段：

- `supported_protocols`
- `data_collection_policy`
- `supports_zdr`
- `supports_byok`
- `rate_limit_hint`
- `region`
- `health_status`

### 4.7 Models

| 维度 | 设计 |
| --- | --- |
| Handler | `models`、`createModel`、`testModel` |
| Template | [internal/console/templates/models.html](../internal/console/templates/models.html) |
| Partial | `models-content`、`model-test-result` |
| 查询 | `ListModelRoutes`、`ListProviders`、`ListProxyProfiles`、`ListPricingRules` |
| 现状 | public model 到单路由映射 + 测试框。 |

当前表单字段：

| 字段 | 说明 |
| --- | --- |
| `public_name` | 对外模型名 |
| `provider_id` | 绑定 Provider |
| `upstream_model` | 实际模型名 |
| `protocol` | 主协议 |
| `proxy_profile_id` | 覆盖代理 |
| `context_tokens` | 上下文长度 |
| `priority` | 优先级 |
| `weight` | 权重 |

测试框字段：

| 字段 | 说明 |
| --- | --- |
| `test_public_name` | 选择已保存 public model |
| `test_prompt` | 最小探测 prompt |

P1 需要把 Models 从“单条映射”升级为“路由入口页”：

- 显示候选 endpoint 与 fallback 模型。
- 展示价格快照来源。
- 展示兼容参数能力，例如 tools、structured_outputs、reasoning。
- 引入 `route profile` 编辑器。

### 4.8 Proxies

| 维度 | 设计 |
| --- | --- |
| Handler | `proxies`、`createProxy` |
| Template | [internal/console/templates/proxies.html](../internal/console/templates/proxies.html) |
| Partial | `proxies-content` |
| 查询 | `ListProxyProfiles` + 衍生 view data |
| 现状 | direct/http/socks5 的 Profile 管理。 |

当前表单字段：

| 字段 | 说明 |
| --- | --- |
| `id` | Profile ID |
| `name` | 显示名称 |
| `type` | direct、http、socks5 |
| `region` | 区域标签 |
| `endpoint` | 代理地址 |
| `auth` | 凭据 |
| `health_check_url` | 健康检查 URL |
| `timeout_seconds` | 超时 |
| `status` | 启用或停用 |

扩展建议：

- 在卡片上展示最近 1h 成功率和延迟。
- 支持“试连”动作。
- 当某 Profile 被 Provider、Endpoint、Route 引用时，展示引用计数。

### 4.9 Settings

| 维度 | 设计 |
| --- | --- |
| Handler | `settings`、`saveAuthSettings` |
| Template | [internal/console/templates/settings.html](../internal/console/templates/settings.html) |
| Partial | `settings-content` |
| 查询 | `billingPageData` 不再单独使用；当前主要依赖 `UserDirectory` 和 `ConsoleAuthSettings` |
| 现状 | Settings 与 Users 已合并。 |

当前区块：

- 身份中心概览。
- 观察到的 user_id 目录。
- 最近审计片段。
- allowlist 与本地密码开关。
- OAuth provider 表单矩阵。

当前全局表单字段：

| 字段 | 说明 |
| --- | --- |
| `local_password_enabled` | 是否允许本地密码登录 |
| `allowed_emails` | 一行一个邮箱 |
| `allowed_domains` | 一行一个域名 |

当前 OAuth provider 字段前缀为动态 `FormPrefix`，主要字段包括：

- `enabled`
- `client_id`
- `client_secret`
- `scopes`
- `auth_url`
- `token_url`
- `userinfo_url`
- `issuer_url`
- `kind`
- `token_style`
- `token_response_path`
- `auth_style`
- `userinfo_method`
- `userinfo_token_style`
- `userinfo_subject_path`
- `userinfo_email_path`
- `userinfo_name_path`
- `userinfo_login_path`
- `extra_email_url`
- `auth_params`
- `token_params`
- `userinfo_params`

P1 可继续并入：

- SSO/SAML provider 配置。
- session lifetime。
- admin roles。
- input/output logging access policy。

## 5. Redirect 页面处理

### 5.1 Billing

当前 `billing` handler 直接跳转 `/console/usage#spend-overview`。短期内应保留该行为，因为：

- Usage 页面已经承载账单总览。
- 避免产生第二套重复查询与模板。

只有当以下内容明显超出 Usage 页面承载能力时，再恢复独立 Billing 页：

- adjustment 管理
- invoice/export 历史
- prepay credit / balance 管理
- workspace 级预算规则

### 5.2 Users

当前 `users` handler 直接跳转 `/console/settings#identity-center`。这与现有 Settings 页面定位一致：

- 当前用户目录主要来自 API Key、usage、audit 的被动观察，不是完整 IAM。
- 真正的身份与访问设置与 Users 强耦合。

未来如果引入 Workspace 成员、RBAC、组织邀请，再拆回独立 Users/Access 页面更合适。

## 6. 目标新增页面拆解

### 6.1 Logs

建议：

- 路由：`GET /console/logs`
- Template：`logs`、`logs-content`
- 查询：`ListGenerations`、`ListGenerationChunks` 或对象存储摘要
- 区块：全文开关状态、保留期、脱敏策略、最近 generation 列表、详情抽屉

关键过滤项：

- workspace
- model
- provider
- api_key
- log retention state
- PII redaction state

### 6.2 Workspaces

建议：

- 路由：`GET /console/workspaces`、`POST /console/workspaces`
- Partial：`workspaces-content`
- 区块：workspace 列表、成员、默认 routing、默认 guardrail、默认 observability

表单字段：

- `id`
- `name`
- `description`
- `default_routing_policy_id`
- `default_guardrail_id`
- `default_region`
- `logging_mode`

### 6.3 Routing

建议：

- 路由：`GET /console/routing`、`POST /console/routing/policies`
- 区块：默认策略、候选过滤、排序、fallback、最近健康窗口

表单字段：

- `strategy`
- `provider_order`
- `allow_fallbacks`
- `require_parameters`
- `data_collection`
- `require_zdr`
- `max_price_prompt`
- `max_price_completion`
- `preferred_region`
- `sort`

### 6.4 BYOK

建议：

- 路由：`GET /console/byok`、`POST /console/byok/credentials`
- 区块：凭据列表、绑定 Provider、优先/备用顺序、过滤条件、最近失败

表单字段：

- `provider_id`
- `credential_name`
- `secret_material`
- `priority_group`
- `always_use`
- `allowed_models`
- `allowed_api_keys`
- `workspace_ids`

### 6.5 Guardrails

建议：

- 路由：`GET /console/guardrails`、`POST /console/guardrails`
- 区块：预算规则、allowlist、regex 过滤器、prompt injection 策略、ZDR

表单字段：

- `name`
- `daily_budget_cents`
- `weekly_budget_cents`
- `monthly_budget_cents`
- `allowed_models`
- `allowed_providers`
- `require_zdr`
- `regex_filters`
- `filter_action`

### 6.6 Observability

建议：

- 路由：`GET /console/observability`、`POST /console/observability/destinations`
- 区块：input/output logging 开关、broadcast destinations、sampling、privacy mode

### 6.7 Request Builder

建议：

- 路由：`GET /console/request-builder`、`POST /console/request-builder/render`
- Partial：`request-builder-preview`
- 区块：协议选择、messages 编辑器、provider preferences、代码片段导出

### 6.8 Playground

建议：

- 路由：`GET /console/playground`、`POST /console/playground/run`
- Partial：`playground-result`
- 区块：对话区、流式输出、工具调用日志、structured output 验证

## 7. 查询与命令接口建议

随着页面增加，`storage.Store` 应逐步从“页面级聚合查询”演进为“领域视图查询 + 页面组装”。建议新增：

- `ListGenerations`
- `GetGeneration`
- `ListRoutingPolicies`
- `ListProviderEndpoints`
- `ListBYOKCredentials`
- `ListGuardrails`
- `ListObservabilityDestinations`
- `ListWorkspaces`

这样可以避免每新增一个页面就继续膨胀 `BillingOverview`、`UserDirectory` 这类宽查询。

## 8. 页面间联动原则

- Dashboard 只展示摘要，不承担重配置。
- Usage 负责最终记账面。
- Audit 负责链路解释面。
- Settings 负责身份入口与后台访问边界。
- Routing、BYOK、Guardrails、Observability 未来构成“运营面”的四个核心页。

## 9. 决策摘要

- 当前 Console 已经有稳定的 SSR + HTMX 结构，后续新增页面不应另起前端模式。
- 每个页面都要明确 handler、template、fragment 和查询来源，避免页面设计只停留在视觉层。
- Billing 与 Users 当前维持 redirect 是合理的；等数据模型长出来后再恢复独立页。
- 新增页面优先级应是 Routing、Workspaces、BYOK、Guardrails，再到 Observability、Request Builder、Playground。