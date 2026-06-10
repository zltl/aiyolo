package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatEndpoint                  = "/console/chat"
	consoleChatFormMaxMemory             = 1 << 20
	consoleChatDefaultCompletionTokens   = 768
	consoleChatReasoningCompletionTokens = 4096
	consoleChatEmptyOutput               = "No text returned."
)

func stripConsoleChatFailurePrefix(locale string, detail string) string {
	detail = strings.TrimSpace(detail)
	prefixes := []string{
		consoleText(locale, "对话失败：", "Chat failed: "),
		"对话失败：",
		"Chat failed: ",
	}
	for {
		changed := false
		for _, prefix := range prefixes {
			if prefix == "" || !strings.HasPrefix(detail, prefix) {
				continue
			}
			detail = strings.TrimSpace(strings.TrimPrefix(detail, prefix))
			changed = true
		}
		if !changed {
			return detail
		}
	}
}

func consoleChatFormatFailure(locale string, detail string) string {
	detail = sanitizeConsoleChatFailureDetail(locale, stripConsoleChatFailurePrefix(locale, detail))
	if detail == "" {
		return consoleText(locale, "对话失败。", "Chat failed.")
	}
	return fmt.Sprintf(consoleText(locale, "对话失败：%s", "Chat failed: %s"), detail)
}

func sanitizeConsoleChatFailureDetail(locale string, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return detail
	}
	lowerDetail := strings.ToLower(detail)
	if strings.Contains(lowerDetail, "closed pipe") || strings.Contains(lowerDetail, "broken pipe") {
		return consoleText(locale,
			"Claude Code 运行时连接中断。请刷新页面等待容器自动升级后重试，或新建对话。",
			"The Claude Code runtime connection was interrupted. Refresh the page to auto-upgrade the container and retry, or start a new conversation.",
		)
	}
	if strings.Contains(lowerDetail, "aiyolo-ass stream transport interrupted") {
		return consoleText(locale,
			"Claude Code 运行时连接中断。请刷新页面等待容器自动升级后重试，或新建对话。",
			"The Claude Code runtime connection was interrupted. Refresh the page to auto-upgrade the container and retry, or start a new conversation.",
		)
	}
	return detail
}

func consoleCloudAgentASSJobResumeDetail(locale string, err error) string {
	switch {
	case workerops.CloudAgentASSJobsEndpointUnavailable(err):
		return consoleText(locale,
			"Claude Code 的 aiyolo-ass 版本过旧，缺少后台任务接口。请刷新页面等待自动升级后重试，或新建对话。",
			"The Claude Code aiyolo-ass is outdated and missing background job APIs. Refresh to auto-upgrade and retry, or start a new conversation.",
		)
	case workerops.CloudAgentASSJobNotFound(err):
		return consoleText(locale,
			"Claude Code 后台任务已结束或丢失，无法继续重连。",
			"The Claude Code background task ended or was lost; unable to reconnect.",
		)
	default:
		return stripConsoleChatFailurePrefix(locale, err.Error())
	}
}

type consoleChatRouteView struct {
	PublicName       string
	ProviderID       string
	ProviderName     string
	UpstreamModel    string
	Protocol         string
	ReasoningEfforts []string
	ImageGeneration  bool
}

type consoleChatFormView struct {
	ClientSessionID              string                      `json:"clientSessionId"`
	PublicName                   string                      `json:"publicName"`
	Environment                  string                      `json:"environment,omitempty"`
	ReasoningEffort              string                      `json:"reasoningEffort,omitempty"`
	SystemPrompt                 string                      `json:"systemPrompt"`
	Draft                        string                      `json:"draft"`
	Attachments                  []consoleChatAttachmentView `json:"attachments,omitempty"`
	ShellActiveTerminalID        string                      `json:"shellActiveTerminalId,omitempty"`
	ShellCurrentWorkingDirectory string                      `json:"shellCurrentWorkingDirectory,omitempty"`
}

type consoleChatAttachmentView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ObjectKey  string `json:"objectKey"`
	URL        string `json:"url"`
	BrowserURL string `json:"browserUrl,omitempty"`
	MediaType  string `json:"mediaType"`
	SizeBytes  int64  `json:"sizeBytes"`
}

type consoleChatMessageView struct {
	ID          string                      `json:"id"`
	Role        string                      `json:"role"`
	Label       string                      `json:"label"`
	Content     string                      `json:"content"`
	Reasoning   string                      `json:"reasoning,omitempty"`
	Attachments []consoleChatAttachmentView `json:"attachments,omitempty"`
}

type consoleChatPromptView struct {
	Label  string
	Prompt string
}

var consoleChatDeepSeekReasoningEfforts = []string{"high", "max"}

func consoleChatCompletionTokens(route domain.ModelRoute) int {
	modelID := strings.ToLower(strings.TrimSpace(firstNonEmpty(route.UpstreamModel, route.PublicName)))
	if strings.Contains(modelID, "deepseek-v4-pro") {
		return consoleChatReasoningCompletionTokens
	}
	return consoleChatDefaultCompletionTokens
}

func consoleChatRouteReasoningEfforts(route domain.ModelRoute, provider domain.Provider) []string {
	if !domain.IsDeepSeekProvider(provider) {
		return nil
	}
	modelID := strings.ToLower(strings.TrimSpace(firstNonEmpty(route.UpstreamModel, route.PublicName)))
	if !strings.Contains(modelID, "deepseek-v4-pro") {
		return nil
	}
	efforts := make([]string, 0, len(consoleChatDeepSeekReasoningEfforts))
	efforts = append(efforts, consoleChatDeepSeekReasoningEfforts...)
	return efforts
}

func consoleChatNormalizeReasoningEffort(efforts []string, raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	for _, effort := range efforts {
		if raw == effort {
			return raw
		}
	}
	return ""
}

func consoleChatAppliedReasoningEffort(route domain.ModelRoute, provider domain.Provider, raw string) string {
	return consoleChatNormalizeReasoningEffort(consoleChatRouteReasoningEfforts(route, provider), raw)
}

func consoleChatReasoningEffortLabel(locale, effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high":
		return "high"
	case "max":
		return "max"
	default:
		return strings.TrimSpace(effort)
	}
}

type consoleChatResultView struct {
	PublicName    string
	ProviderID    string
	ProviderName  string
	UpstreamModel string
	Output        string
	Reasoning     string `json:"reasoning,omitempty"`
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
	Form                    consoleChatFormView
	EnvironmentOptions      []consoleChatEnvironmentOption
	Routes                  []consoleChatRouteView
	Messages                []consoleChatMessageView
	LastResponseID          string
	Presets                 []consoleChatPromptView
	SelectedRoute           consoleChatRouteView
	Result                  *consoleChatResultView
	Error                   string
	AttachmentTreeEnabled   bool
	AttachmentUploadURL     string
	AttachmentUploadEnabled bool
	AttachmentMaxBytes      int64
	SessionStore            consoleChatSessionStoreView
}

func (state consoleChatPageState) data() map[string]any {
	return map[string]any{
		"Title":                       "Chat",
		"ChatForm":                    state.Form,
		"ChatEnvironmentOptions":      state.EnvironmentOptions,
		"ChatRoutes":                  state.Routes,
		"ChatShellPageURL":            consoleChatShellPagePath,
		"ChatShellSocketURL":          consoleChatShellSocketPath,
		"ChatShellStateURL":           consoleChatShellStatePath,
		"ChatAttachmentTreeURL":       consoleChatAttachmentTreePath,
		"ChatAttachmentTreeEnabled":   state.AttachmentTreeEnabled,
		"ChatWorkspaceTreeURL":        consoleChatWorkspaceTreePath,
		"ChatWorkspaceListDirURL":     consoleChatWorkspaceListDirPath,
		"ChatWorkspaceFileURL":        consoleChatWorkspaceFilePath,
		"ChatWorkspaceDownloadURL":    consoleChatWorkspaceDownloadPath,
		"ChatWorkspaceUploadURL":      consoleChatWorkspaceUploadPath,
		"ChatWorkspaceUploadMaxBytes": consoleChatWorkspaceMaxUploadBytes,
		"ChatWorkspaceDirectoryURL":   consoleChatWorkspaceDirectoryPath,
		"ChatWorkspaceCopyURL":        consoleChatWorkspaceCopyPath,
		"ChatWorkspaceRenameURL":      consoleChatWorkspaceRenamePath,
		"ChatWorkspaceDeleteURL":      consoleChatWorkspaceDeletePath,
		"ChatMessages":                state.Messages,
		"ChatPresets":                 state.Presets,
		"SelectedChatRoute":           state.SelectedRoute,
		"ChatResult":                  state.Result,
		"ChatError":                   state.Error,
		"ChatHistoryJSON":             consoleChatJSON(state.Messages),
		"ChatDraftAttachmentsJSON":    consoleChatJSON(state.Form.Attachments),
		"ChatAttachmentUploadURL":     state.AttachmentUploadURL,
		"ChatAttachmentUploadEnabled": state.AttachmentUploadEnabled,
		"ChatAttachmentMaxBytes":      state.AttachmentMaxBytes,
		"ChatSessionStoreJSON":        consoleChatJSON(state.SessionStore),
	}
}

func consoleChatJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func parseConsoleChatForm(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && mediaType == "multipart/form-data" {
		return r.ParseMultipartForm(consoleChatFormMaxMemory)
	}
	return r.ParseForm()
}

func consoleChatRequestHasSubmittedState(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.TrimSpace(r.FormValue("chat_history_json")) != "" {
		return true
	}
	if len(r.Form["chat_message_role"]) > 0 {
		return true
	}
	if strings.TrimSpace(r.FormValue("chat_draft")) != "" {
		return true
	}
	if strings.TrimSpace(r.FormValue("chat_draft_attachments_json")) != "" {
		return true
	}
	return strings.TrimSpace(r.FormValue("chat_client_session_id")) != ""
}

func consoleChatRequestedSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(r.FormValue("chat_client_session_id")), strings.TrimSpace(r.URL.Query().Get("session")))
}

func consoleChatCanonicalSessionURL(r *http.Request, activeSessionID string) string {
	if r == nil {
		return ""
	}
	activeSessionID = strings.TrimSpace(activeSessionID)
	if activeSessionID == "" {
		return ""
	}
	requestedSessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if requestedSessionID == activeSessionID {
		return ""
	}
	nextURL := *r.URL
	query := nextURL.Query()
	query.Set("session", activeSessionID)
	nextURL.RawQuery = query.Encode()
	return nextURL.String()
}

func defaultConsoleChatSystemPrompt(locale string) string {
	return consoleText(locale,
		"你正在 AIYolo 提供的 AI 助手，回复要自然、流畅、专业，像人类助理一样倾听并回应。鼓励使用格式化工具（如粗体、无序列表、表格）保持内容条理清晰，避免大段密集的文字。在处理情感问题时要求保持同理心，但同时也要基于事实与现实。",
		"You are an AI assistant provided by AIYolo. Your responses should be natural, fluent, and professional, listening and responding like a human assistant. Use formatting tools such as bold text, bullet lists, and tables to keep the content clear and well organized, and avoid large dense blocks of text. When handling emotional issues, remain empathetic while staying grounded in facts and reality.")
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
	return buildConsoleChatMessageWithAttachments(locale, role, content, nil)
}

func buildConsoleChatMessageWithReasoning(locale, role, content, reasoning string) consoleChatMessageView {
	message := buildConsoleChatMessage(locale, role, content)
	message.Reasoning = strings.TrimSpace(reasoning)
	return message
}

func buildConsoleChatMessageWithAttachments(locale, role, content string, attachments []consoleChatAttachmentView) consoleChatMessageView {
	role = normalizeConsoleChatRole(role)
	return consoleChatMessageView{ID: newConsoleID("msg"), Role: role, Label: consoleChatRoleLabel(locale, role), Content: strings.TrimSpace(content), Attachments: cloneConsoleChatAttachments(attachments)}
}

func consoleChatDisplayOutput(locale string, result consoleChatResultView) string {
	output := strings.TrimSpace(result.Output)
	if output != "" && output != consoleChatEmptyOutput {
		return output
	}
	if strings.TrimSpace(result.Reasoning) != "" {
		return consoleText(locale,
			"模型只返回了思考过程，没有返回最终答复。",
			"The model returned reasoning but no final answer text.")
	}
	if output != "" {
		return output
	}
	return consoleChatEmptyOutput
}

func cloneConsoleChatAttachments(attachments []consoleChatAttachmentView) []consoleChatAttachmentView {
	if len(attachments) == 0 {
		return nil
	}
	cloned := make([]consoleChatAttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		cloned = append(cloned, attachment)
	}
	return cloned
}

func normalizeConsoleChatAttachment(cfg artifacts.Config, attachment consoleChatAttachmentView) (consoleChatAttachmentView, bool) {
	attachment.ID = strings.TrimSpace(attachment.ID)
	if attachment.ID == "" {
		attachment.ID = newConsoleID("att")
	}
	attachment.ObjectKey = artifacts.NormalizeObjectKey(attachment.ObjectKey)
	if attachment.ObjectKey == "" {
		return consoleChatAttachmentView{}, false
	}
	attachment.Name = strings.TrimSpace(attachment.Name)
	if attachment.Name == "" {
		attachment.Name = path.Base(attachment.ObjectKey)
	}
	attachment.MediaType = strings.ToLower(strings.TrimSpace(attachment.MediaType))
	if attachment.MediaType == "" {
		attachment.MediaType = "application/octet-stream"
	}
	if attachment.SizeBytes < 0 {
		attachment.SizeBytes = 0
	}
	attachment.URL = cfg.PublicObjectURL(attachment.ObjectKey)
	attachment.BrowserURL = consoleChatAttachmentBrowserURL(cfg, attachment.ObjectKey)
	if strings.TrimSpace(attachment.URL) == "" {
		return consoleChatAttachmentView{}, false
	}
	return attachment, true
}

func normalizeConsoleChatMessage(locale string, message consoleChatMessageView, cfg artifacts.Config) (consoleChatMessageView, bool) {
	message.Role = normalizeConsoleChatRole(message.Role)
	message.Content = strings.TrimSpace(message.Content)
	message.Reasoning = strings.TrimSpace(message.Reasoning)
	message.ID = strings.TrimSpace(message.ID)
	if message.ID == "" {
		message.ID = newConsoleID("msg")
	}
	attachments := make([]consoleChatAttachmentView, 0, len(message.Attachments))
	for _, attachment := range message.Attachments {
		normalized, ok := normalizeConsoleChatAttachment(cfg, attachment)
		if !ok {
			continue
		}
		attachments = append(attachments, normalized)
	}
	message.Attachments = attachments
	if message.Content != "" {
		message.Content = rewriteChatAssetMarkdownURLs(cfg, message.Content)
	}
	if message.Role == "" || (message.Content == "" && message.Reasoning == "" && len(message.Attachments) == 0) {
		return consoleChatMessageView{}, false
	}
	message.Label = consoleChatRoleLabel(locale, message.Role)
	return message, true
}

func parseConsoleChatDraftAttachments(r *http.Request, cfg artifacts.Config) []consoleChatAttachmentView {
	raw := strings.TrimSpace(r.FormValue("chat_draft_attachments_json"))
	if raw == "" {
		return nil
	}
	var attachments []consoleChatAttachmentView
	if err := json.Unmarshal([]byte(raw), &attachments); err != nil {
		return nil
	}
	views := make([]consoleChatAttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		normalized, ok := normalizeConsoleChatAttachment(cfg, attachment)
		if !ok {
			continue
		}
		views = append(views, normalized)
	}
	return views
}

func consoleChatAllowedModelSlot(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return "", false
	}
	if normalized == "gpt-image-2" || strings.HasSuffix(normalized, "/gpt-image-2") {
		return "gpt-image-2", true
	}
	if normalized == "chatgpt-image-2" || strings.HasSuffix(normalized, "/chatgpt-image-2") {
		return "gpt-image-2", true
	}
	if strings.Contains(normalized, "flux") {
		return "flux-image", true
	}
	return "", false
}

func parseConsoleChatMessages(r *http.Request, locale string, cfg artifacts.Config) []consoleChatMessageView {
	raw := strings.TrimSpace(r.FormValue("chat_history_json"))
	if raw != "" {
		var decoded []consoleChatMessageView
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			messages := make([]consoleChatMessageView, 0, len(decoded))
			for _, message := range decoded {
				normalized, ok := normalizeConsoleChatMessage(locale, message, cfg)
				if !ok {
					continue
				}
				messages = append(messages, normalized)
			}
			return messages
		}
	}
	roles := r.Form["chat_message_role"]
	contents := r.Form["chat_message_content"]
	limit := len(roles)
	if len(contents) < limit {
		limit = len(contents)
	}
	messages := make([]consoleChatMessageView, 0, limit)
	for idx := 0; idx < limit; idx++ {
		message, ok := normalizeConsoleChatMessage(locale, consoleChatMessageView{Role: roles[idx], Content: contents[idx]}, cfg)
		if !ok {
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
			PublicName:       route.PublicName,
			ProviderID:       provider.ID,
			ProviderName:     firstNonEmpty(strings.TrimSpace(provider.Name), provider.ID),
			UpstreamModel:    firstNonEmpty(route.UpstreamModel, route.PublicName),
			Protocol:         protocol,
			ReasoningEfforts: consoleChatRouteReasoningEfforts(route, provider),
			ImageGeneration:  consoleChatIsImageGenerationModel(route),
		})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].PublicName != views[j].PublicName {
			return views[i].PublicName < views[j].PublicName
		}
		if views[i].ProviderID != views[j].ProviderID {
			return views[i].ProviderID < views[j].ProviderID
		}
		return views[i].UpstreamModel < views[j].UpstreamModel
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
	requestedSlot, ok := consoleChatAllowedModelSlot(publicName)
	if !ok {
		return consoleChatRouteView{}, false
	}
	for _, route := range routes {
		if routeSlot, routeOK := consoleChatAllowedModelSlot(route.PublicName); routeOK && routeSlot == requestedSlot {
			return route, true
		}
		if routeSlot, routeOK := consoleChatAllowedModelSlot(route.UpstreamModel); routeOK && routeSlot == requestedSlot {
			return route, true
		}
	}
	return consoleChatRouteView{}, false
}

func consoleChatModelBasename(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		return strings.TrimSpace(model[idx+1:])
	}
	return model
}

func consoleChatAllowedModelMatchesCandidate(allowed, candidate string) bool {
	allowed = strings.TrimSpace(allowed)
	candidate = strings.TrimSpace(candidate)
	if allowed == "" || candidate == "" {
		return false
	}
	if allowed == candidate {
		return true
	}
	for _, alias := range consoleChatAllowedModelAliases(allowed) {
		if strings.TrimSpace(alias) == candidate {
			return true
		}
	}
	for _, alias := range consoleChatAllowedModelAliases(candidate) {
		if strings.TrimSpace(alias) == allowed {
			return true
		}
	}
	if consoleChatModelBasename(allowed) == consoleChatModelBasename(candidate) {
		return true
	}
	if allowedSlot, allowedOK := consoleChatAllowedModelSlot(allowed); allowedOK {
		if candidateSlot, candidateOK := consoleChatAllowedModelSlot(candidate); candidateOK && allowedSlot == candidateSlot {
			return true
		}
	}
	return false
}

func consoleChatFilterRoutesByAllowedModels(routes []consoleChatRouteView, allowedModels []string) []consoleChatRouteView {
	if len(allowedModels) == 0 {
		return nil
	}
	expanded := consoleChatExpandUserAllowedModels(allowedModels)
	if len(expanded) == 0 {
		return nil
	}
	filtered := make([]consoleChatRouteView, 0, len(routes))
	for _, route := range routes {
		candidates := []string{strings.TrimSpace(route.PublicName), strings.TrimSpace(route.UpstreamModel)}
		matched := false
		for _, allowed := range expanded {
			for _, candidate := range candidates {
				if consoleChatAllowedModelMatchesCandidate(allowed, candidate) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

type consoleChatAllowedModelsResolution struct {
	Models       []string
	Unrestricted bool
}

func consoleChatIsSeedAPIKey(key domain.APIKey) bool {
	if strings.TrimSpace(key.ID) == "seed" {
		return true
	}
	return strings.TrimSpace(key.Name) == "Seed API Key"
}

func consoleChatIsCloudAgentInfrastructureAPIKey(key domain.APIKey) bool {
	if strings.HasPrefix(strings.TrimSpace(key.Name), "Cloud Agent ") {
		return true
	}
	return consoleChatSameStringSet(key.AllowedProtocols, consoleChatCloudAgentAllowedProtocols())
}

func consoleChatAPIKeyOwnedByUser(key domain.APIKey, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	keyUserID := strings.TrimSpace(key.UserID)
	if keyUserID == userID {
		return true
	}
	// Legacy console-created keys were stored under a shared owner id.
	return keyUserID == "local" || keyUserID == "default"
}

func (handler *Handler) consoleChatAllowedModelsFromUserAPIKeys(ctx context.Context, userID string) (consoleChatAllowedModelsResolution, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return consoleChatAllowedModelsResolution{}, nil
	}
	keys, err := handler.store.ListAPIKeys(ctx)
	if err != nil {
		return consoleChatAllowedModelsResolution{}, err
	}
	now := time.Now().UTC()
	union := make(map[string]struct{})
	hasUnrestrictedUserKey := false
	for _, key := range keys {
		if !consoleChatAPIKeyOwnedByUser(key, userID) {
			continue
		}
		if consoleChatIsCloudAgentInfrastructureAPIKey(key) && len(key.AllowedModels) == 0 {
			continue
		}
		if !auth.APIKeyActive(key, now) {
			continue
		}
		if consoleChatIsSeedAPIKey(key) {
			if len(key.AllowedModels) == 0 {
				continue
			}
			for _, model := range consoleChatExpandUserAllowedModels(key.AllowedModels) {
				trimmed := strings.TrimSpace(model)
				if trimmed == "" {
					continue
				}
				union[trimmed] = struct{}{}
			}
			continue
		}
		if consoleChatIsCloudAgentInfrastructureAPIKey(key) {
			for _, model := range consoleChatExpandUserAllowedModels(key.AllowedModels) {
				trimmed := strings.TrimSpace(model)
				if trimmed == "" {
					continue
				}
				union[trimmed] = struct{}{}
			}
			continue
		}
		if len(key.AllowedModels) == 0 {
			hasUnrestrictedUserKey = true
			continue
		}
		for _, model := range consoleChatExpandUserAllowedModels(key.AllowedModels) {
			trimmed := strings.TrimSpace(model)
			if trimmed == "" {
				continue
			}
			union[trimmed] = struct{}{}
		}
	}
	if len(union) > 0 {
		models := make([]string, 0, len(union))
		for model := range union {
			models = append(models, model)
		}
		sort.Strings(models)
		return consoleChatAllowedModelsResolution{Models: models}, nil
	}
	if hasUnrestrictedUserKey {
		return consoleChatAllowedModelsResolution{Unrestricted: true}, nil
	}
	return consoleChatAllowedModelsResolution{}, nil
}

func (handler *Handler) consoleChatAllowedModelsFromCloudAgentAccounts(ctx context.Context, userID, environment string) (consoleChatAllowedModelsResolution, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return consoleChatAllowedModelsResolution{}, nil
	}
	workerID := consoleChatEnvironmentWorkerID(environment)
	accounts, err := handler.store.ListCloudAgentAccounts(ctx, userID, workerID)
	if err != nil {
		return consoleChatAllowedModelsResolution{}, err
	}
	now := time.Now().UTC()
	for _, account := range accounts {
		credential := strings.TrimSpace(account.Credential)
		if credential == "" {
			continue
		}
		key, err := handler.store.FindAPIKeyByHash(ctx, auth.HashAPIKey(credential))
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return consoleChatAllowedModelsResolution{}, err
		}
		if !auth.APIKeyActive(key, now) {
			continue
		}
		reconciledAllowedModels := consoleChatExpandAllowedModels(key.AllowedModels)
		if !consoleChatSameStringSet(reconciledAllowedModels, key.AllowedModels) {
			key.AllowedModels = reconciledAllowedModels
			if err := handler.store.CreateAPIKey(ctx, key); err != nil {
				return consoleChatAllowedModelsResolution{}, err
			}
		}
		return consoleChatAllowedModelsResolution{Models: reconciledAllowedModels}, nil
	}
	if strings.TrimSpace(workerID) != "" {
		return handler.consoleChatAllowedModelsFromCloudAgentAccounts(ctx, userID, consoleChatEnvironmentLocal)
	}
	return consoleChatAllowedModelsResolution{}, nil
}

func (handler *Handler) resolveConsoleChatAllowedModels(ctx context.Context, userID, environment string) (consoleChatAllowedModelsResolution, error) {
	if resolution, err := handler.consoleChatAllowedModelsFromUserAPIKeys(ctx, userID); err != nil {
		return consoleChatAllowedModelsResolution{}, err
	} else if resolution.Unrestricted || len(resolution.Models) > 0 {
		return resolution, nil
	}
	return consoleChatAllowedModelsResolution{}, nil
}

func (handler *Handler) consoleChatCloudAgentAllowedModels(ctx context.Context, userID string, state *consoleChatPageState) ([]string, error) {
	if state == nil {
		return nil, errors.New("chat state is required")
	}
	resolution, err := handler.resolveConsoleChatAllowedModels(ctx, userID, consoleChatEnvironmentLocal)
	if err != nil {
		return nil, err
	}
	if len(resolution.Models) > 0 {
		return resolution.Models, nil
	}
	selected := strings.TrimSpace(state.Form.PublicName)
	if selected == "" && len(state.Routes) > 0 {
		selected = strings.TrimSpace(state.Routes[0].PublicName)
	}
	if selected == "" {
		return nil, nil
	}
	return consoleChatExpandAllowedModels([]string{selected}), nil
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
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	environmentOptions, err := handler.chatEnvironmentOptions(ctx, r)
	if err != nil {
		return consoleChatPageState{}, err
	}
	state := consoleChatPageState{
		Form: consoleChatFormView{
			ClientSessionID:              consoleChatRequestedSessionID(r),
			PublicName:                   strings.TrimSpace(r.FormValue("chat_public_name")),
			Environment:                  strings.TrimSpace(r.FormValue("chat_environment")),
			ReasoningEffort:              strings.TrimSpace(r.FormValue("chat_reasoning_effort")),
			SystemPrompt:                 strings.TrimSpace(r.FormValue("chat_system_prompt")),
			Draft:                        strings.TrimSpace(r.FormValue("chat_draft")),
			Attachments:                  parseConsoleChatDraftAttachments(r, handler.cfg.ChatAttachments),
			ShellActiveTerminalID:        normalizeConsoleChatOptionalShellTerminalID(r.FormValue("chat_shell_active_terminal_id")),
			ShellCurrentWorkingDirectory: normalizeConsoleChatShellWorkingDirectory(r.FormValue("chat_shell_current_working_directory")),
		},
		EnvironmentOptions:      environmentOptions,
		Routes:                  consoleChatRoutes(routes, providers),
		Messages:                parseConsoleChatMessages(r, locale, handler.cfg.ChatAttachments),
		Presets:                 defaultConsoleChatPrompts(locale),
		AttachmentTreeEnabled:   handler.cfg.ChatAttachments.CanList(),
		AttachmentUploadURL:     consoleChatAttachmentUploadPath,
		AttachmentUploadEnabled: handler.cfg.ChatAttachments.CanUpload(),
		AttachmentMaxBytes:      consoleChatAttachmentMaxBytes,
	}
	sessionStore, activeSession, err := handler.loadConsoleChatSessionStore(ctx, r, state.Form.ClientSessionID)
	if err != nil {
		return consoleChatPageState{}, err
	}
	state.SessionStore = sessionStore
	if activeSession != nil {
		state.LastResponseID = strings.TrimSpace(activeSession.LastResponseID)
	}
	if activeSession != nil && !consoleChatRequestHasSubmittedState(r) {
		state.Form.ClientSessionID = activeSession.ID
		state.Form.PublicName = firstNonEmpty(state.Form.PublicName, activeSession.PublicName)
		state.Form.Environment = firstNonEmpty(state.Form.Environment, handler.restoreConsoleChatEnvironment(ctx, userID, activeSession.ID))
		state.Form.SystemPrompt = firstNonEmpty(state.Form.SystemPrompt, activeSession.SystemPrompt)
		state.Form.Draft = firstNonEmpty(state.Form.Draft, activeSession.Draft)
		state.Form.Attachments = cloneConsoleChatAttachments(activeSession.DraftAttachments)
		state.Messages = cloneConsoleChatMessages(activeSession.Messages)
	}
	state.Form.Environment = normalizeConsoleChatEnvironmentValue(state.Form.Environment, state.EnvironmentOptions)
	if resolution, err := handler.resolveConsoleChatAllowedModels(ctx, userID, state.Form.Environment); err != nil {
		return consoleChatPageState{}, err
	} else if !resolution.Unrestricted {
		state.Routes = consoleChatFilterRoutesByAllowedModels(state.Routes, resolution.Models)
	}
	if state.Form.PublicName == "" && len(state.Routes) > 0 {
		state.Form.PublicName = state.Routes[0].PublicName
	}
	if state.Form.SystemPrompt == "" {
		state.Form.SystemPrompt = defaultConsoleChatSystemPrompt(locale)
	}
	if selected, ok := findConsoleChatRoute(state.Routes, state.Form.PublicName); ok {
		state.SelectedRoute = selected
		state.Form.PublicName = selected.PublicName
		state.Form.ReasoningEffort = consoleChatNormalizeReasoningEffort(selected.ReasoningEfforts, state.Form.ReasoningEffort)
	} else {
		state.Form.ReasoningEffort = ""
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
	if !consoleChatRequestHasSubmittedState(r) && !isHTMXRequest(r) {
		if nextURL := consoleChatCanonicalSessionURL(r, state.Form.ClientSessionID); nextURL != "" {
			http.Redirect(w, r, nextURL, http.StatusSeeOther)
			return
		}
	}
	handler.renderChat(w, r, state)
}

func (handler *Handler) sendChat(w http.ResponseWriter, r *http.Request) {
	if err := parseConsoleChatForm(r); err != nil {
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
	if strings.TrimSpace(state.Form.Draft) == "" && len(state.Form.Attachments) == 0 {
		state.Error = handler.requestText(r, "先输入一条消息。", "Enter a message first.")
		handler.renderChat(w, r, state)
		return
	}
	if _, ok := findConsoleChatRoute(state.Routes, state.Form.PublicName); !ok {
		state.Error = handler.requestText(r, "请选择当前可用的 public model。", "Choose a public model that is currently available in this chat page.")
		handler.renderChat(w, r, state)
		return
	}
	if _, err := handler.ensureConsoleChatEnvironment(r.Context(), r, &state); err != nil {
		state.Error = err.Error()
		handler.renderChat(w, r, state)
		return
	}

	requestID := requestID(r)
	history := state.Messages
	userMessage := buildConsoleChatMessageWithAttachments(locale, openai.ChatMessageRoleUser, state.Form.Draft, state.Form.Attachments)
	var execution consoleChatExecution
	var executionErr error
	if state.Form.Environment != consoleChatEnvironmentLocal {
		worker, key, account, cloudSession, targetErr := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, state.Form.ClientSessionID)
		if targetErr != nil {
			state.Error = targetErr.Error()
			handler.renderChat(w, r, state)
			return
		}
		execution, executionErr = handler.runCloudAgentChat(context.WithoutCancel(r.Context()), worker, key, account, cloudSession, consoleCloudAgentChatRequest{
			SessionID:                    state.Form.ClientSessionID,
			PublicName:                   state.Form.PublicName,
			PreviousResponseID:           state.LastResponseID,
			History:                      history,
			UserInput:                    state.Form.Draft,
			Attachments:                  state.Form.Attachments,
			ShellActiveTerminalID:        state.Form.ShellActiveTerminalID,
			ShellCurrentWorkingDirectory: state.Form.ShellCurrentWorkingDirectory,
		})
	} else {
		target, errorMessage := handler.resolveConsoleChatTarget(r.Context(), r, state.Form.PublicName)
		if errorMessage != "" {
			state.Error = errorMessage
			handler.renderChat(w, r, state)
			return
		}
		consoleUserID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
		started := time.Now()
		executionProtocol := handler.consoleChatExecutionProtocol(target.Route, target.Provider, history, state.Form.Attachments)
		execution, executionErr = handler.runConsoleChatTurn(r.Context(), currentConsoleSessionSubject(r, handler.cfg.SecretKey), target.Provider, target.Route, target.Profile, state.Form.SystemPrompt, state.Form.ReasoningEffort, history, state.Form.Draft, state.Form.Attachments)
		persistConsoleChatOutcome(context.WithoutCancel(r.Context()), handler.store, requestID, consoleUserID, executionProtocol, target.Route, target.Provider, target.PricingRule, started, execution)
	}
	state.Messages = append(history, userMessage)
	if executionErr != nil {
		state.Messages = consoleChatAppendResultMessage(locale, state.Messages, execution.Result)
		handler.syncConsoleChatPageSession(context.WithoutCancel(r.Context()), r, &state, state.Messages, consoleChatSessionStatusForError(execution.Result), requestID, execution.Result.ResponseID, executionErr.Error())
		state.Error = fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), executionErr.Error())
		handler.renderChat(w, r, state)
		return
	}
	state.Form.Draft = ""
	state.Form.Attachments = nil
	execution.Result.Output = consoleChatDisplayOutput(locale, execution.Result)
	state.Messages = append(state.Messages, buildConsoleChatMessageWithReasoning(locale, openai.ChatMessageRoleAssistant, execution.Result.Output, execution.Result.Reasoning))
	handler.syncConsoleChatPageSession(context.WithoutCancel(r.Context()), r, &state, state.Messages, consoleChatSessionStatusCompleted, requestID, execution.Result.ResponseID, "")
	state.Result = &execution.Result
	handler.renderChat(w, r, state)
}

func runConsoleChatTurn(ctx context.Context, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, reasoningEffort string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView) (consoleChatExecution, error) {
	protocol := consoleChatRouteProtocol(route, provider)
	if protocol == "" {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadRequest}}, &consoleUpstreamError{StatusCode: http.StatusBadRequest, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}
	return runConsoleChatTurnWithContinuation(ctx, protocol, provider, route, profile, systemPrompt, reasoningEffort, history, userInput, attachments, false, nil, nil)
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
		usage.Currency = firstNonEmpty(pricingRule.Currency, domain.DefaultBillingCurrency)
	}
	if usage.CostMicroCents == 0 && usage.StatusCode < 400 {
		usage.CostMicroCents = calculateModelTestUsageCost(pricingRule, usage)
	}
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	return usage
}

func persistConsoleChatOutcome(ctx context.Context, store storage.Store, requestID, userID, protocol string, route domain.ModelRoute, provider domain.Provider, pricingRule domain.PricingRule, started time.Time, execution consoleChatExecution) {
	usage := buildConsoleChatUsageRecord(requestID, userID, protocol, route, provider, pricingRule, started, execution)
	if err := store.InsertUsage(ctx, usage); err != nil {
		log.Printf("insert console chat usage request_id=%s err=%v", requestID, err)
	}
}
