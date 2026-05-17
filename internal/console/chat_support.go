package console

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

const (
	consoleChatEndpoint            = "/console/chat"
	consoleChatMaxCompletionTokens = 768
)

type consoleChatRouteView struct {
	PublicName    string
	ProviderID    string
	ProviderName  string
	UpstreamModel string
	Protocol      string
}

type consoleChatFormView struct {
	PublicName   string
	SystemPrompt string
	Draft        string
}

type consoleChatMessageView struct {
	Role    string
	Label   string
	Content string
}

type consoleChatPromptView struct {
	Label  string
	Prompt string
}

type consoleChatResultView struct {
	PublicName    string
	ProviderID    string
	ProviderName  string
	UpstreamModel string
	Output        string
	ResponseID    string
	FinishReason  string
	DurationMS    int64
	TotalTokens   int
}

type consoleChatExecution struct {
	Result     consoleChatResultView
	Usage      domain.UsageRecord
	StatusCode int
}

type consoleChatPageState struct {
	Form          consoleChatFormView
	Routes        []consoleChatRouteView
	Messages      []consoleChatMessageView
	Presets       []consoleChatPromptView
	SelectedRoute consoleChatRouteView
	Result        *consoleChatResultView
	Error         string
}

func (state consoleChatPageState) data() map[string]any {
	return map[string]any{
		"Title":             "Chat",
		"ChatForm":          state.Form,
		"ChatRoutes":        state.Routes,
		"ChatMessages":      state.Messages,
		"ChatPresets":       state.Presets,
		"SelectedChatRoute": state.SelectedRoute,
		"ChatResult":        state.Result,
		"ChatError":         state.Error,
	}
}

func defaultConsoleChatSystemPrompt(locale string) string {
	return consoleText(locale,
		"你正在 AIYolo 控制台里协助运维验证模型路由。回答保持简洁、具体，并尽量贴近当前选中的模型与路由上下文。",
		"You are assisting an operator inside the AIYolo console. Keep answers concise, concrete, and grounded in the selected model route.")
}

func defaultConsoleChatPrompts(locale string) []consoleChatPromptView {
	return []consoleChatPromptView{
		{
			Label:  consoleText(locale, "路由排查", "Route check"),
			Prompt: consoleText(locale, "帮我总结当前 public model 对应的上游路由和潜在故障点。", "Summarize the current public model route and the likely failure points."),
		},
		{
			Label:  consoleText(locale, "代理验证", "Proxy check"),
			Prompt: consoleText(locale, "如果代理不可用，你会如何描述这条链路可能出现的问题？", "If the proxy becomes unavailable, how would you describe the likely failure on this path?"),
		},
		{
			Label:  consoleText(locale, "运营摘要", "Ops summary"),
			Prompt: consoleText(locale, "请用三句话总结这个模型适合什么场景，以及我要关注什么成本信号。", "Summarize in three sentences what this model is good for and which cost signals to watch."),
		},
	}
}

func consoleChatRoleLabel(locale, role string) string {
	switch normalizeConsoleChatRole(role) {
	case openai.ChatMessageRoleUser:
		return consoleText(locale, "你", "You")
	case openai.ChatMessageRoleAssistant:
		return "AIYolo"
	case openai.ChatMessageRoleSystem:
		return consoleText(locale, "系统", "System")
	default:
		return consoleText(locale, "消息", "Message")
	}
}

func normalizeConsoleChatRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case openai.ChatMessageRoleAssistant:
		return openai.ChatMessageRoleAssistant
	case openai.ChatMessageRoleSystem:
		return openai.ChatMessageRoleSystem
	case openai.ChatMessageRoleUser:
		return openai.ChatMessageRoleUser
	default:
		return ""
	}
}

func buildConsoleChatMessage(locale, role, content string) consoleChatMessageView {
	role = normalizeConsoleChatRole(role)
	return consoleChatMessageView{Role: role, Label: consoleChatRoleLabel(locale, role), Content: strings.TrimSpace(content)}
}

func parseConsoleChatMessages(r *http.Request, locale string) []consoleChatMessageView {
	roles := r.Form["chat_message_role"]
	contents := r.Form["chat_message_content"]
	limit := len(roles)
	if len(contents) < limit {
		limit = len(contents)
	}
	messages := make([]consoleChatMessageView, 0, limit)
	for idx := 0; idx < limit; idx++ {
		message := buildConsoleChatMessage(locale, roles[idx], contents[idx])
		if message.Role == "" || message.Content == "" {
			continue
		}
		messages = append(messages, message)
	}
	return messages
}

func consoleChatRoutes(routes []domain.ModelRoute, providers []domain.Provider) []consoleChatRouteView {
	providerByID := make(map[string]domain.Provider, len(providers))
	for _, provider := range providers {
		providerByID[provider.ID] = provider
	}

	views := make([]consoleChatRouteView, 0, len(routes))
	for _, route := range routes {
		if strings.TrimSpace(route.PublicName) == "" || !route.Enabled {
			continue
		}
		provider, ok := providerByID[route.ProviderID]
		if !ok {
			continue
		}
		if status := strings.TrimSpace(provider.Status); status != "" && !strings.EqualFold(status, domain.StatusEnabled) {
			continue
		}
		protocol := consoleChatRouteProtocol(route, provider)
		if protocol == "" {
			continue
		}
		views = append(views, consoleChatRouteView{
			PublicName:    route.PublicName,
			ProviderID:    provider.ID,
			ProviderName:  firstNonEmpty(strings.TrimSpace(provider.Name), provider.ID),
			UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
			Protocol:      protocol,
		})
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].PublicName < views[j].PublicName
	})
	return views
}

func findConsoleChatRoute(routes []consoleChatRouteView, publicName string) (consoleChatRouteView, bool) {
	publicName = strings.TrimSpace(publicName)
	for _, route := range routes {
		if route.PublicName == publicName {
			return route, true
		}
	}
	return consoleChatRouteView{}, false
}

func (handler *Handler) chatPageState(ctx context.Context, r *http.Request) (consoleChatPageState, error) {
	routes, err := handler.store.ListModelRoutes(ctx)
	if err != nil {
		return consoleChatPageState{}, err
	}
	providers, err := handler.store.ListProviders(ctx)
	if err != nil {
		return consoleChatPageState{}, err
	}
	locale := resolveConsoleLocale(r)
	state := consoleChatPageState{
		Form: consoleChatFormView{
			PublicName:   strings.TrimSpace(r.FormValue("chat_public_name")),
			SystemPrompt: strings.TrimSpace(r.FormValue("chat_system_prompt")),
			Draft:        strings.TrimSpace(r.FormValue("chat_draft")),
		},
		Routes:   consoleChatRoutes(routes, providers),
		Messages: parseConsoleChatMessages(r, locale),
		Presets:  defaultConsoleChatPrompts(locale),
	}
	if state.Form.PublicName == "" && len(state.Routes) > 0 {
		state.Form.PublicName = state.Routes[0].PublicName
	}
	if state.Form.SystemPrompt == "" {
		state.Form.SystemPrompt = defaultConsoleChatSystemPrompt(locale)
	}
	if selected, ok := findConsoleChatRoute(state.Routes, state.Form.PublicName); ok {
		state.SelectedRoute = selected
	}
	return state, nil
}

func (handler *Handler) renderChat(w http.ResponseWriter, r *http.Request, state consoleChatPageState) {
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "chat-content", state.data())
		return
	}
	handler.render(w, r, "chat", state.data())
}

func (handler *Handler) chat(w http.ResponseWriter, r *http.Request) {
	state, err := handler.chatPageState(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	handler.renderChat(w, r, state)
}

func (handler *Handler) sendChat(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	state, err := handler.chatPageState(r.Context(), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	locale := resolveConsoleLocale(r)
	if state.Form.PublicName == "" {
		state.Error = handler.requestText(r, "先选择一个可用的 public model。", "Select an available public model first.")
		handler.renderChat(w, r, state)
		return
	}
	if strings.TrimSpace(state.Form.Draft) == "" {
		state.Error = handler.requestText(r, "先输入一条消息。", "Enter a message first.")
		handler.renderChat(w, r, state)
		return
	}
	if _, ok := findConsoleChatRoute(state.Routes, state.Form.PublicName); !ok {
		state.Error = handler.requestText(r, "请选择当前可用的 public model。", "Choose a public model that is currently available in this chat page.")
		handler.renderChat(w, r, state)
		return
	}

	target, errorMessage := handler.resolveConsoleChatTarget(r.Context(), r, state.Form.PublicName)
	if errorMessage != "" {
		state.Error = errorMessage
		handler.renderChat(w, r, state)
		return
	}

	requestID := requestID(r)
	consoleUserID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	started := time.Now()
	state.Messages = append(state.Messages, buildConsoleChatMessage(locale, openai.ChatMessageRoleUser, state.Form.Draft))
	execution, err := runConsoleChatTurn(r.Context(), target.Provider, target.Route, target.Profile, state.Form.SystemPrompt, state.Messages[:len(state.Messages)-1], state.Form.Draft)
	persistConsoleChatOutcome(context.WithoutCancel(r.Context()), handler.store, requestID, consoleUserID, clientIP(r), r.UserAgent(), target.Protocol, target.Route, target.Provider, target.Profile, target.PricingRule, started, execution, err)
	state.Form.Draft = ""
	if err != nil {
		state.Error = fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), err.Error())
		handler.renderChat(w, r, state)
		return
	}
	state.Messages = append(state.Messages, buildConsoleChatMessage(locale, openai.ChatMessageRoleAssistant, execution.Result.Output))
	state.Result = &execution.Result
	handler.renderChat(w, r, state)
}

func runConsoleChatTurn(ctx context.Context, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, history []consoleChatMessageView, userInput string) (consoleChatExecution, error) {
	protocol := consoleChatRouteProtocol(route, provider)
	if protocol == "" {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadRequest}}, &consoleUpstreamError{StatusCode: http.StatusBadRequest, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}
	return runConsoleRawChatTurn(ctx, protocol, provider, route, profile, systemPrompt, history, userInput, false, nil)
}

func buildConsoleChatUsageRecord(requestID, userID, protocol string, route domain.ModelRoute, provider domain.Provider, pricingRule domain.PricingRule, started time.Time, execution consoleChatExecution) domain.UsageRecord {
	usage := execution.Usage
	usage.RequestID = requestID
	usage.UserID = userID
	usage.ProviderID = provider.ID
	usage.ModelAlias = route.PublicName
	usage.UpstreamModel = firstNonEmpty(route.UpstreamModel, route.PublicName)
	usage.Protocol = protocol
	usage.Endpoint = consoleChatEndpoint
	usage.Stream = execution.Usage.Stream
	if usage.StatusCode == 0 {
		usage.StatusCode = execution.StatusCode
	}
	if usage.StatusCode == 0 {
		usage.StatusCode = http.StatusBadGateway
	}
	if usage.LatencyMS <= 0 {
		usage.LatencyMS = time.Since(started).Milliseconds()
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	if usage.Currency == "" {
		usage.Currency = firstNonEmpty(pricingRule.Currency, "USD")
	}
	if usage.CostMicroCents == 0 && usage.StatusCode < 400 {
		usage.CostMicroCents = calculateModelTestUsageCost(pricingRule, usage)
	}
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	return usage
}

func persistConsoleChatOutcome(ctx context.Context, store storage.Store, requestID, userID, clientAddress, userAgent, protocol string, route domain.ModelRoute, provider domain.Provider, profile domain.ProxyProfile, pricingRule domain.PricingRule, started time.Time, execution consoleChatExecution, cause error) {
	usage := buildConsoleChatUsageRecord(requestID, userID, protocol, route, provider, pricingRule, started, execution)
	if err := store.InsertUsage(ctx, usage); err != nil {
		log.Printf("insert console chat usage request_id=%s err=%v", requestID, err)
	}
	event := domain.AuditEvent{
		ID:             newID("audit"),
		RequestID:      requestID,
		UserID:         userID,
		ClientIP:       clientAddress,
		UserAgent:      userAgent,
		Protocol:       protocol,
		Endpoint:       consoleChatEndpoint,
		ModelAlias:     route.PublicName,
		ProviderID:     provider.ID,
		UpstreamModel:  firstNonEmpty(route.UpstreamModel, route.PublicName),
		ProxyProfileID: profile.ID,
		Stream:         usage.Stream,
		StatusCode:     usage.StatusCode,
		ErrorCode:      consoleChatErrorCode(cause),
		LatencyMS:      usage.LatencyMS,
		InputTokens:    usage.InputTokens,
		OutputTokens:   usage.OutputTokens,
		CostMicroCents: usage.CostMicroCents,
		EventType:      "console_chat",
		Message:        strings.TrimSpace(firstNonEmpty(errorString(cause), "Console chat turn completed.")),
		CreatedAt:      usage.CreatedAt,
	}
	if err := store.InsertAudit(ctx, event); err != nil {
		log.Printf("insert console chat audit request_id=%s err=%v", requestID, err)
	}
}
