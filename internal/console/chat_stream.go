package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
	proxytransport "github.com/zltl/aiyolo/internal/proxy"
	"github.com/zltl/aiyolo/internal/storage"
)

const (
	consoleChatStreamPath      = "/console/chat/stream"
	consoleAnthropicVersion    = "2023-06-01"
	consoleChatStreamMediaType = "application/x-ndjson; charset=utf-8"
)

type consoleChatTarget struct {
	Route       domain.ModelRoute
	Provider    domain.Provider
	Profile     domain.ProxyProfile
	PricingRule domain.PricingRule
	Protocol    string
}

type consoleChatStreamEvent struct {
	Type    string                 `json:"type"`
	Delta   string                 `json:"delta,omitempty"`
	Reasoning string               `json:"reasoning,omitempty"`
	HTML    string                 `json:"html,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Message *consoleChatAPIMessage `json:"message,omitempty"`
	Result  *consoleChatAPIResult  `json:"result,omitempty"`
}

type consoleChatAPIMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
}

type consoleChatAPIResult struct {
	PublicName    string `json:"publicName"`
	ProviderName  string `json:"providerName"`
	UpstreamModel string `json:"upstreamModel"`
	Output        string `json:"output"`
	Reasoning     string `json:"reasoning,omitempty"`
	ResponseID    string `json:"responseId"`
	FinishReason  string `json:"finishReason"`
	DurationMS    int64  `json:"durationMs"`
	TotalTokens   int    `json:"totalTokens"`
}

func consoleChatAPIResultView(result consoleChatResultView) consoleChatAPIResult {
	return consoleChatAPIResult{
		PublicName:    result.PublicName,
		ProviderName:  result.ProviderName,
		UpstreamModel: result.UpstreamModel,
		Output:        result.Output,
		Reasoning:     result.Reasoning,
		ResponseID:    result.ResponseID,
		FinishReason:  result.FinishReason,
		DurationMS:    result.DurationMS,
		TotalTokens:   result.TotalTokens,
	}
}

type consoleChatStreamChunk struct {
	Delta        string
	Reasoning    string
	ResponseID   string
	FinishReason string
	Usage        domain.UsageRecord
	Err          error
}

type consoleChatOpenAIMessagePayload struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type consoleChatOpenAIContentPayload struct {
	Type     string                            `json:"type"`
	Text     string                            `json:"text,omitempty"`
	ImageURL *consoleChatOpenAIImageURLPayload `json:"image_url,omitempty"`
}

type consoleChatOpenAIImageURLPayload struct {
	URL string `json:"url"`
}

type consoleChatAnthropicMessagePayload struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type consoleChatAnthropicContentPayload struct {
	Type   string                             `json:"type"`
	Text   string                             `json:"text,omitempty"`
	Title  string                             `json:"title,omitempty"`
	Source *consoleChatAnthropicSourcePayload `json:"source,omitempty"`
}

type consoleChatAnthropicSourcePayload struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type consoleUpstreamError struct {
	StatusCode int
	Code       string
	Message    string
}

func (err *consoleUpstreamError) Error() string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Message) != "" {
		return err.Message
	}
	if err.StatusCode > 0 {
		return http.StatusText(err.StatusCode)
	}
	return "upstream request failed"
}

func consoleChatCompatibleProtocols(route domain.ModelRoute, provider domain.Provider) []string {
	providerProtocols := domain.ProviderSupportedProtocols(provider)
	routeProtocols := domain.RouteAllowedProtocols(route, provider)
	compatible := make([]string, 0, 2)
	for _, protocol := range []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic} {
		if domain.SupportsProtocol(providerProtocols, protocol) && domain.SupportsProtocol(routeProtocols, protocol) {
			compatible = append(compatible, protocol)
		}
	}
	return compatible
}

func consoleChatRouteProtocol(route domain.ModelRoute, provider domain.Provider) string {
	compatible := consoleChatCompatibleProtocols(route, provider)
	if len(compatible) == 0 {
		return ""
	}
	primary := domain.RoutePrimaryProtocol(route, provider)
	if domain.SupportsProtocol(compatible, primary) {
		return primary
	}
	return compatible[0]
}

func (handler *Handler) resolveConsoleChatTarget(ctx context.Context, r *http.Request, publicName string) (consoleChatTarget, string) {
	var target consoleChatTarget
	route, err := handler.store.GetModelRoute(ctx, publicName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return target, handler.requestText(r, "找不到这个模型路由，请先在 Models 页面保存并启用。", "That model route was not found. Save and enable it on the Models page first.")
		}
		return target, err.Error()
	}
	provider, err := handler.store.GetProvider(ctx, route.ProviderID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return target, handler.requestText(r, "路由引用的 Provider 不存在。", "The provider referenced by this route does not exist.")
		}
		return target, err.Error()
	}
	protocol := consoleChatRouteProtocol(route, provider)
	if protocol == "" {
		return target, handler.requestText(r, "当前聊天页只支持 OpenAI 或 Anthropic 兼容 Provider。", "The chat page currently supports only OpenAI- or Anthropic-compatible providers.")
	}
	if strings.TrimSpace(provider.MasterKey) == "" {
		return target, handler.requestText(r, "当前 Provider 还没有保存可用的 master key。", "The selected provider does not have a usable master key saved yet.")
	}
	pricingRule, err := resolveModelTestPricingRule(ctx, handler.store, route)
	if err != nil {
		return target, fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), err.Error())
	}
	profile, err := resolveModelTestProxyProfile(ctx, handler.store, provider, route)
	if err != nil {
		return target, fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), err.Error())
	}
	return consoleChatTarget{Route: route, Provider: provider, Profile: profile, PricingRule: pricingRule, Protocol: protocol}, ""
}

func (handler *Handler) streamChat(w http.ResponseWriter, r *http.Request) {
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
		handler.streamChatReplace(w, r, state)
		return
	}
	if strings.TrimSpace(state.Form.Draft) == "" && len(state.Form.Attachments) == 0 {
		state.Error = handler.requestText(r, "先输入一条消息。", "Enter a message first.")
		handler.streamChatReplace(w, r, state)
		return
	}
	if _, ok := findConsoleChatRoute(state.Routes, state.Form.PublicName); !ok {
		state.Error = handler.requestText(r, "请选择当前可用的 public model。", "Choose a public model that is currently available in this chat page.")
		handler.streamChatReplace(w, r, state)
		return
	}

	target, errorMessage := handler.resolveConsoleChatTarget(r.Context(), r, state.Form.PublicName)
	if errorMessage != "" {
		state.Error = errorMessage
		handler.streamChatReplace(w, r, state)
		return
	}

	handler.startChatEventStream(w)
	requestID := requestID(r)
	consoleUserID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	started := time.Now()
	state.Messages = append(state.Messages, buildConsoleChatMessageWithAttachments(locale, "user", state.Form.Draft, state.Form.Attachments))
	execution, err := runConsoleRawChatTurn(r.Context(), target.Protocol, target.Provider, target.Route, target.Profile, state.Form.SystemPrompt, state.Messages[:len(state.Messages)-1], state.Form.Draft, state.Form.Attachments, true, func(delta string) error {
		return handler.writeChatStreamEvent(w, consoleChatStreamEvent{Type: "delta", Delta: delta})
	}, func(reasoning string) error {
		return handler.writeChatStreamEvent(w, consoleChatStreamEvent{Type: "reasoning", Reasoning: reasoning})
	})
	persistConsoleChatOutcome(context.WithoutCancel(r.Context()), handler.store, requestID, consoleUserID, clientIP(r), r.UserAgent(), target.Protocol, target.Route, target.Provider, target.Profile, target.PricingRule, started, execution, err)
	if err != nil {
		state.Error = fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), err.Error())
		_ = handler.streamChatReplace(w, r, state)
		return
	}
	state.Form.Draft = ""
	state.Form.Attachments = nil
	execution.Result.Output = consoleChatDisplayOutput(locale, execution.Result)
	state.Messages = append(state.Messages, buildConsoleChatMessageWithReasoning(locale, "assistant", execution.Result.Output, execution.Result.Reasoning))
	state.Result = &execution.Result
	_ = handler.streamChatReplace(w, r, state)
}

func (handler *Handler) startChatEventStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", consoleChatStreamMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
}

func (handler *Handler) writeChatStreamEvent(w http.ResponseWriter, event consoleChatStreamEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(payload, '\n')); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (handler *Handler) renderChatFragmentHTML(r *http.Request, state consoleChatPageState) (string, error) {
	var buffer bytes.Buffer
	if err := handler.tmpl.ExecuteTemplate(&buffer, "chat-content", handler.decoratePageData(r, state.data())); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func (handler *Handler) streamChatReplace(w http.ResponseWriter, r *http.Request, state consoleChatPageState) error {
	handler.startChatEventStream(w)
	html, err := handler.renderChatFragmentHTML(r, state)
	if err != nil {
		return err
	}
	return handler.writeChatStreamEvent(w, consoleChatStreamEvent{Type: "replace", HTML: html})
}

func runConsoleRawChatTurn(ctx context.Context, protocol string, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView, stream bool, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	prompt := strings.TrimSpace(userInput)
	if prompt == "" && len(attachments) == 0 {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadRequest, Stream: stream}}, errors.New("message is empty")
	}
	if protocol != domain.ProtocolOpenAI && protocol != domain.ProtocolAnthropic {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadRequest, Stream: stream}}, &consoleUpstreamError{StatusCode: http.StatusBadRequest, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}

	timeoutSeconds := provider.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 90
	}

	chatCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	httpClient, err := proxytransport.NewTransportFactory().HTTPClient(chatCtx, provider, profile)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadGateway, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadGateway, Stream: stream}}, err
	}
	body, err := buildConsoleChatRequestBody(protocol, route, systemPrompt, history, prompt, attachments, stream)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadRequest, Stream: stream}}, err
	}
	request, err := buildConsoleChatUpstreamRequest(chatCtx, provider, protocol, body, stream)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusInternalServerError, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusInternalServerError, Stream: stream}}, err
	}

	started := time.Now()
	response, err := httpClient.Do(request)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadGateway, Usage: domain.UsageRecord{Currency: "USD", StatusCode: http.StatusBadGateway, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(response.Body)
		upstreamErr := parseConsoleUpstreamError(response.StatusCode, responseBody)
		return consoleChatExecution{StatusCode: response.StatusCode, Usage: domain.UsageRecord{Currency: "USD", StatusCode: response.StatusCode, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, upstreamErr
	}

	if stream {
		contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if contentType == "text/event-stream" {
			return parseConsoleChatStreamResponse(response.Body, protocol, route, provider, started, onDelta, onReasoning)
		}
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return consoleChatExecution{StatusCode: response.StatusCode, Usage: domain.UsageRecord{Currency: "USD", StatusCode: response.StatusCode, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, err
	}
	return parseConsoleChatJSONResponse(responseBody, protocol, route, provider, response.StatusCode, stream, started)
}

func buildConsoleChatRequestBody(protocol string, route domain.ModelRoute, systemPrompt string, history []consoleChatMessageView, prompt string, attachments []consoleChatAttachmentView, stream bool) ([]byte, error) {
	upstreamModel := firstNonEmpty(route.UpstreamModel, route.PublicName)
	switch protocol {
	case domain.ProtocolAnthropic:
		payload := map[string]any{
			"model":      upstreamModel,
			"max_tokens": consoleChatMaxCompletionTokens,
			"messages":   buildConsoleAnthropicMessages(history, prompt, attachments),
			"stream":     stream,
		}
		if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		return json.Marshal(payload)
	default:
		payload := map[string]any{
			"model":      upstreamModel,
			"max_tokens": consoleChatMaxCompletionTokens,
			"messages":   buildConsoleOpenAIMessages(systemPrompt, history, prompt, attachments),
			"stream":     stream,
		}
		if stream {
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
		return json.Marshal(payload)
	}
}

func buildConsoleOpenAIMessages(systemPrompt string, history []consoleChatMessageView, prompt string, attachments []consoleChatAttachmentView) []consoleChatOpenAIMessagePayload {
	messages := make([]consoleChatOpenAIMessagePayload, 0, len(history)+2)
	if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
		messages = append(messages, consoleChatOpenAIMessagePayload{Role: "system", Content: systemPrompt})
	}
	for _, message := range history {
		role := normalizeConsoleChatRole(message.Role)
		content := buildConsoleOpenAIMessageContent(strings.TrimSpace(message.Content), message.Attachments)
		if role == "" || content == nil {
			continue
		}
		messages = append(messages, consoleChatOpenAIMessagePayload{Role: role, Content: content})
	}
	messages = append(messages, consoleChatOpenAIMessagePayload{Role: "user", Content: buildConsoleOpenAIMessageContent(strings.TrimSpace(prompt), attachments)})
	return messages
}

func buildConsoleOpenAIMessageContent(text string, attachments []consoleChatAttachmentView) any {
	parts := make([]consoleChatOpenAIContentPayload, 0, len(attachments)+1)
	if text = strings.TrimSpace(text); text != "" {
		parts = append(parts, consoleChatOpenAIContentPayload{Type: "text", Text: text})
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		if consoleChatAttachmentIsImage(attachment.MediaType) {
			parts = append(parts, consoleChatOpenAIContentPayload{Type: "image_url", ImageURL: &consoleChatOpenAIImageURLPayload{URL: attachment.URL}})
			continue
		}
		parts = append(parts, consoleChatOpenAIContentPayload{Type: "text", Text: consoleChatAttachmentReferenceText(attachment)})
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 && parts[0].Type == "text" {
		return parts[0].Text
	}
	return parts
}

func buildConsoleAnthropicMessages(history []consoleChatMessageView, prompt string, attachments []consoleChatAttachmentView) []consoleChatAnthropicMessagePayload {
	messages := make([]consoleChatAnthropicMessagePayload, 0, len(history)+1)
	for _, message := range history {
		role := normalizeConsoleChatRole(message.Role)
		content := buildConsoleAnthropicMessageContent(strings.TrimSpace(message.Content), message.Attachments)
		if content == nil {
			continue
		}
		if role != "user" && role != "assistant" {
			continue
		}
		messages = append(messages, consoleChatAnthropicMessagePayload{Role: role, Content: content})
	}
	messages = append(messages, consoleChatAnthropicMessagePayload{Role: "user", Content: buildConsoleAnthropicMessageContent(strings.TrimSpace(prompt), attachments)})
	return messages
}

func buildConsoleAnthropicMessageContent(text string, attachments []consoleChatAttachmentView) any {
	parts := make([]consoleChatAnthropicContentPayload, 0, len(attachments)+1)
	if text = strings.TrimSpace(text); text != "" {
		parts = append(parts, consoleChatAnthropicContentPayload{Type: "text", Text: text})
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		switch {
		case consoleChatAttachmentIsImage(attachment.MediaType):
			parts = append(parts, consoleChatAnthropicContentPayload{Type: "image", Source: &consoleChatAnthropicSourcePayload{Type: "url", URL: attachment.URL, MediaType: attachment.MediaType}})
		case consoleChatAttachmentIsDocument(attachment.MediaType):
			parts = append(parts, consoleChatAnthropicContentPayload{Type: "document", Title: attachment.Name, Source: &consoleChatAnthropicSourcePayload{Type: "url", URL: attachment.URL, MediaType: attachment.MediaType}})
		default:
			parts = append(parts, consoleChatAnthropicContentPayload{Type: "text", Text: consoleChatAttachmentReferenceText(attachment)})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 && parts[0].Type == "text" {
		return parts[0].Text
	}
	return parts
}

func consoleChatAttachmentReferenceText(attachment consoleChatAttachmentView) string {
	label := firstNonEmpty(strings.TrimSpace(attachment.Name), path.Base(strings.TrimSpace(attachment.ObjectKey)), "attachment")
	if mediaType := strings.TrimSpace(attachment.MediaType); mediaType != "" {
		return fmt.Sprintf("Attachment: %s (%s)\nURL: %s", label, mediaType, strings.TrimSpace(attachment.URL))
	}
	return fmt.Sprintf("Attachment: %s\nURL: %s", label, strings.TrimSpace(attachment.URL))
}

func consoleChatAttachmentIsImage(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "image/")
}

func consoleChatAttachmentIsDocument(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case mediaType == "application/pdf":
		return true
	case strings.HasPrefix(mediaType, "text/"):
		return true
	case mediaType == "application/json", mediaType == "text/csv", mediaType == "application/xml", mediaType == "text/xml":
		return true
	default:
		return false
	}
}

func buildConsoleChatUpstreamRequest(ctx context.Context, provider domain.Provider, protocol string, body []byte, stream bool) (*http.Request, error) {
	upstreamURL, err := joinConsoleChatUpstreamURL(domain.ProviderBaseURLForProtocol(provider, protocol), consoleChatUpstreamEndpoint(protocol))
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Set("User-Agent", "AIYolo Console Chat")
	if protocol == domain.ProtocolAnthropic {
		request.Header.Set("anthropic-version", consoleAnthropicVersion)
	}
	if domain.IsOpenRouterProvider(provider) {
		request.Header.Set("Authorization", "Bearer "+provider.MasterKey)
		request.Header.Set("HTTP-Referer", "https://github.com/zltl/aiyolo")
		request.Header.Set("X-Title", "aiyolo")
	} else if protocol == domain.ProtocolAnthropic {
		request.Header.Set("x-api-key", provider.MasterKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+provider.MasterKey)
	}
	return request, nil
}

func consoleChatUpstreamEndpoint(protocol string) string {
	if protocol == domain.ProtocolAnthropic {
		return "/v1/messages"
	}
	return "/v1/chat/completions"
}

func joinConsoleChatUpstreamURL(baseURL string, endpoint string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	endpointPath := endpoint
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(endpointPath, "/v1/") {
		endpointPath = strings.TrimPrefix(endpointPath, "/v1")
	}
	parsed.Path = path.Join(basePath, endpointPath)
	return parsed.String(), nil
}

func parseConsoleChatJSONResponse(body []byte, protocol string, route domain.ModelRoute, provider domain.Provider, statusCode int, stream bool, started time.Time) (consoleChatExecution, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return consoleChatExecution{StatusCode: statusCode, Usage: domain.UsageRecord{Currency: "USD", StatusCode: statusCode, Stream: stream, LatencyMS: time.Since(started).Milliseconds()}}, err
	}
	usage := parseConsoleChatUsageFromJSON(body, protocol)
	usage.Currency = firstNonEmpty(usage.Currency, "USD")
	usage.StatusCode = statusCode
	usage.Stream = stream
	usage.LatencyMS = time.Since(started).Milliseconds()
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}

	result := consoleChatResultView{
		PublicName:    route.PublicName,
		ProviderID:    provider.ID,
		ProviderName:  firstNonEmpty(provider.Name, provider.ID),
		UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
		ResponseID:    consoleValueString(payload["id"]),
		DurationMS:    usage.LatencyMS,
		TotalTokens:   usage.TotalTokens,
	}
	switch protocol {
	case domain.ProtocolAnthropic:
		result.Output = consoleTextContent(payload["content"])
		result.Reasoning = consoleReasoningPayloadText(payload["content"])
		result.FinishReason = firstNonEmpty(consoleValueString(payload["stop_reason"]), consoleValueString(payload["stop_reason"]))
	default:
		if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if message, ok := choice["message"].(map[string]any); ok {
					result.Output = consoleTextContent(message["content"])
					result.Reasoning = consoleReasoningPayloadText(message)
				}
				result.FinishReason = consoleValueString(choice["finish_reason"])
			}
		}
	}
	if strings.TrimSpace(result.Output) == "" {
		result.Output = consoleChatEmptyOutput
	}
	return consoleChatExecution{Result: result, Usage: usage, StatusCode: statusCode}, nil
}

func parseConsoleChatStreamResponse(body io.Reader, protocol string, route domain.ModelRoute, provider domain.Provider, started time.Time, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	reader := bufio.NewReader(body)
	usage := domain.UsageRecord{Currency: "USD", StatusCode: http.StatusOK, Stream: true}
	result := consoleChatResultView{
		PublicName:    route.PublicName,
		ProviderID:    provider.ID,
		ProviderName:  firstNonEmpty(provider.Name, provider.ID),
		UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
	}
	var output strings.Builder
	var reasoning strings.Builder
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			chunk, parseErr := parseConsoleChatStreamChunk(line, protocol)
			if parseErr != nil {
				usage.LatencyMS = time.Since(started).Milliseconds()
				result.Output = strings.TrimSpace(output.String())
				result.Reasoning = strings.TrimSpace(reasoning.String())
				result.DurationMS = usage.LatencyMS
				result.TotalTokens = usage.TotalTokens
				return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, parseErr
			}
			mergeConsoleChatUsage(&usage, chunk.Usage)
			if chunk.ResponseID != "" {
				result.ResponseID = chunk.ResponseID
			}
			if chunk.FinishReason != "" {
				result.FinishReason = chunk.FinishReason
			}
			if chunk.Delta != "" {
				output.WriteString(chunk.Delta)
				if onDelta != nil {
					if callbackErr := onDelta(chunk.Delta); callbackErr != nil {
						usage.LatencyMS = time.Since(started).Milliseconds()
						result.Output = strings.TrimSpace(output.String())
						result.Reasoning = strings.TrimSpace(reasoning.String())
						result.DurationMS = usage.LatencyMS
						result.TotalTokens = usage.TotalTokens
						return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, callbackErr
					}
				}
			}
			if chunk.Reasoning != "" {
				reasoning.WriteString(chunk.Reasoning)
				if onReasoning != nil {
					if callbackErr := onReasoning(chunk.Reasoning); callbackErr != nil {
						usage.LatencyMS = time.Since(started).Milliseconds()
						result.Output = strings.TrimSpace(output.String())
						result.Reasoning = strings.TrimSpace(reasoning.String())
						result.DurationMS = usage.LatencyMS
						result.TotalTokens = usage.TotalTokens
						return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, callbackErr
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			usage.LatencyMS = time.Since(started).Milliseconds()
			result.Output = strings.TrimSpace(output.String())
			result.Reasoning = strings.TrimSpace(reasoning.String())
			result.DurationMS = usage.LatencyMS
			result.TotalTokens = usage.TotalTokens
			return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, err
		}
	}
	usage.LatencyMS = time.Since(started).Milliseconds()
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	result.Output = strings.TrimSpace(output.String())
	result.Reasoning = strings.TrimSpace(reasoning.String())
	if result.Output == "" {
		result.Output = consoleChatEmptyOutput
	}
	result.DurationMS = usage.LatencyMS
	result.TotalTokens = usage.TotalTokens
	return consoleChatExecution{Result: result, Usage: usage, StatusCode: http.StatusOK}, nil
}

func parseConsoleChatStreamChunk(line []byte, protocol string) (consoleChatStreamChunk, error) {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return consoleChatStreamChunk{}, nil
	}
	raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if raw == "" || raw == "[DONE]" {
		return consoleChatStreamChunk{}, nil
	}
	rawBytes := []byte(raw)
	var payload map[string]any
	if err := json.Unmarshal(rawBytes, &payload); err != nil {
		return consoleChatStreamChunk{}, err
	}
	if protocol == domain.ProtocolAnthropic {
		if strings.EqualFold(consoleValueString(payload["type"]), "error") || payload["error"] != nil {
			return consoleChatStreamChunk{}, parseConsoleUpstreamError(http.StatusBadGateway, rawBytes)
		}
		chunk := consoleChatStreamChunk{Usage: parseConsoleChatUsageFromJSON(rawBytes, protocol)}
		if message, ok := payload["message"].(map[string]any); ok {
			chunk.ResponseID = consoleValueString(message["id"])
			if finish := consoleValueString(message["stop_reason"]); finish != "" {
				chunk.FinishReason = finish
			}
		}
		switch consoleValueString(payload["type"]) {
		case "content_block_start":
			if block, ok := payload["content_block"].(map[string]any); ok {
				chunk.Delta = consoleTextContent(block["text"])
				chunk.Reasoning = consoleReasoningPayloadText(block)
			}
		case "content_block_delta":
			if delta, ok := payload["delta"].(map[string]any); ok {
				chunk.Delta = consoleTextContent(delta["text"])
				chunk.Reasoning = consoleReasoningPayloadText(delta)
			}
		case "message_delta":
			if delta, ok := payload["delta"].(map[string]any); ok {
				if finish := consoleValueString(delta["stop_reason"]); finish != "" {
					chunk.FinishReason = finish
				}
			}
		}
		if chunk.ResponseID == "" {
			chunk.ResponseID = consoleValueString(payload["id"])
		}
		return chunk, nil
	}
	if payload["error"] != nil {
		return consoleChatStreamChunk{}, parseConsoleUpstreamError(http.StatusBadGateway, rawBytes)
	}
	chunk := consoleChatStreamChunk{Usage: parseConsoleChatUsageFromJSON(rawBytes, protocol), ResponseID: consoleValueString(payload["id"])}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if delta, ok := choice["delta"].(map[string]any); ok {
				chunk.Delta = consoleTextContent(delta["content"])
				chunk.Reasoning = consoleReasoningPayloadText(delta)
			}
			if finish := consoleValueString(choice["finish_reason"]); finish != "" {
				chunk.FinishReason = finish
			}
		}
	}
	return chunk, nil
}

func parseConsoleUpstreamError(statusCode int, body []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return &consoleUpstreamError{StatusCode: statusCode, Message: message}
	}
	message := consoleValueString(payload["message"])
	code := consoleValueString(payload["type"])
	if nested, ok := payload["error"].(map[string]any); ok {
		message = firstNonEmpty(consoleValueString(nested["message"]), message)
		code = firstNonEmpty(consoleValueString(nested["type"]), consoleValueString(nested["code"]), code)
	}
	message = firstNonEmpty(message, strings.TrimSpace(string(body)), http.StatusText(statusCode))
	return &consoleUpstreamError{StatusCode: statusCode, Code: code, Message: message}
}

func parseConsoleChatUsageFromJSON(body []byte, protocol string) domain.UsageRecord {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.UsageRecord{}
	}
	var usagePayload map[string]any
	if value, ok := payload["usage"].(map[string]any); ok {
		usagePayload = value
	}
	if protocol == domain.ProtocolAnthropic && usagePayload == nil {
		if message, ok := payload["message"].(map[string]any); ok {
			if value, ok := message["usage"].(map[string]any); ok {
				usagePayload = value
			}
		}
		if usagePayload == nil && payload["input_tokens"] != nil {
			usage := domain.UsageRecord{Currency: "USD", InputTokens: consoleIntNumber(payload["input_tokens"])}
			usage.TotalTokens = usage.InputTokens
			return usage
		}
	}
	if usagePayload == nil {
		return domain.UsageRecord{}
	}
	usage := domain.UsageRecord{Currency: "USD"}
	if protocol == domain.ProtocolAnthropic {
		usage.InputTokens = consoleIntNumber(usagePayload["input_tokens"])
		usage.OutputTokens = consoleIntNumber(usagePayload["output_tokens"])
		usage.CacheCreationTokens = consoleIntNumber(usagePayload["cache_creation_input_tokens"])
		usage.CacheReadTokens = consoleIntNumber(usagePayload["cache_read_input_tokens"])
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
		return usage
	}
	usage.InputTokens = consoleIntNumber(usagePayload["prompt_tokens"])
	usage.OutputTokens = consoleIntNumber(usagePayload["completion_tokens"])
	if usage.InputTokens == 0 {
		usage.InputTokens = consoleIntNumber(usagePayload["input_tokens"])
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = consoleIntNumber(usagePayload["output_tokens"])
	}
	usage.TotalTokens = consoleIntNumber(usagePayload["total_tokens"])
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func mergeConsoleChatUsage(target *domain.UsageRecord, next domain.UsageRecord) {
	if next.InputTokens > 0 {
		target.InputTokens = next.InputTokens
	}
	if next.OutputTokens > 0 {
		target.OutputTokens = next.OutputTokens
	}
	if next.CacheCreationTokens > 0 {
		target.CacheCreationTokens = next.CacheCreationTokens
	}
	if next.CacheReadTokens > 0 {
		target.CacheReadTokens = next.CacheReadTokens
	}
	if next.Currency != "" {
		target.Currency = next.Currency
	}
	if target.InputTokens > 0 || target.OutputTokens > 0 || target.CacheCreationTokens > 0 || target.CacheReadTokens > 0 {
		target.TotalTokens = target.InputTokens + target.OutputTokens + target.CacheCreationTokens + target.CacheReadTokens
	} else if next.TotalTokens > 0 {
		target.TotalTokens = next.TotalTokens
	}
}

func consoleTextContent(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				blockType := strings.ToLower(consoleValueString(block["type"]))
				if blockType != "" && blockType != "text" && blockType != "output_text" {
					continue
				}
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func consoleReasoningPayloadText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(consoleReasoningPayloadText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]any:
		blockType := strings.ToLower(consoleValueString(typed["type"]))
		switch blockType {
		case "thinking", "thinking_delta", "reasoning", "reasoning_delta", "summary_text":
			return firstNonEmptyRaw(
				consoleRawValueString(typed["thinking"]),
				consoleRawValueString(typed["reasoning"]),
				consoleRawValueString(typed["text"]),
				consoleRawValueString(typed["content"]),
			)
		case "", "message", "delta":
		default:
			if typed["reasoning"] == nil && typed["reasoning_content"] == nil && typed["thinking"] == nil && typed["thinking_content"] == nil {
				return ""
			}
		}
		return firstNonEmptyRaw(
			consoleRawValueString(typed["reasoning_content"]),
			consoleReasoningPayloadText(typed["reasoning"]),
			consoleRawValueString(typed["thinking_content"]),
			consoleReasoningPayloadText(typed["thinking"]),
		)
	default:
		return ""
	}
}

func firstNonEmptyRaw(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func consoleValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func consoleRawValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func consoleIntNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func consoleChatErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var upstreamErr *consoleUpstreamError
	if errors.As(err, &upstreamErr) {
		if upstreamErr.Code != "" {
			return upstreamErr.Code
		}
		return "console_chat_failed"
	}
	return modelTestErrorCode(err)
}
