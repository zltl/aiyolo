package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
	proxytransport "github.com/zltl/aiyolo/internal/proxy"
	"github.com/zltl/aiyolo/internal/storage"
)

const (
	consoleChatStreamPath            = "/console/chat/stream"
	consoleAnthropicVersion          = "2023-06-01"
	consoleChatStreamMediaType       = "application/x-ndjson; charset=utf-8"
	consoleChatHeartbeatInterval     = 10 * time.Second
	consoleChatAutoContinuationLimit = 2
	consoleChatContinuationPrompt    = "Continue exactly from where you stopped without repeating the text already shown."
)

type consoleChatTarget struct {
	Route       domain.ModelRoute
	Provider    domain.Provider
	Profile     domain.ProxyProfile
	PricingRule domain.PricingRule
	Protocol    string
}

type consoleChatStreamEvent struct {
	Type      string                      `json:"type"`
	Delta     string                      `json:"delta,omitempty"`
	Reasoning string                      `json:"reasoning,omitempty"`
	Operation *consoleChatStreamOperation `json:"operation,omitempty"`
	HTML      string                      `json:"html,omitempty"`
	Error     string                      `json:"error,omitempty"`
	Message   *consoleChatAPIMessage      `json:"message,omitempty"`
	Result    *consoleChatAPIResult       `json:"result,omitempty"`
}

type consoleChatAPIMessage struct {
	Role       string                       `json:"role"`
	Content    string                       `json:"content"`
	Reasoning  string                       `json:"reasoning,omitempty"`
	Operations []consoleChatStreamOperation `json:"operations,omitempty"`
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

type consoleChatEventStreamWriter struct {
	handler  *Handler
	w        http.ResponseWriter
	mu       sync.Mutex
	activity chan struct{}
}

func newConsoleChatEventStreamWriter(handler *Handler, w http.ResponseWriter) *consoleChatEventStreamWriter {
	return &consoleChatEventStreamWriter{
		handler:  handler,
		w:        w,
		activity: make(chan struct{}, 1),
	}
}

func (writer *consoleChatEventStreamWriter) Write(event consoleChatStreamEvent) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.handler.writeChatStreamEvent(writer.w, event); err != nil {
		return err
	}
	select {
	case writer.activity <- struct{}{}:
	default:
	}
	return nil
}

func (writer *consoleChatEventStreamWriter) StartHeartbeat(ctx context.Context, interval time.Duration, onError func(error)) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if interval <= 0 {
			return
		}
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-writer.activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(interval)
			case <-timer.C:
				if err := writer.Write(consoleChatStreamEvent{Type: "heartbeat"}); err != nil {
					if onError != nil {
						onError(err)
					}
					return
				}
				timer.Reset(interval)
			}
		}
	}()
	return done
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

func consoleChatStreamResultAvailable(result consoleChatResultView) bool {
	return strings.TrimSpace(result.PublicName) != ""
}

func consoleChatDisplayedResult(locale string, result consoleChatResultView) consoleChatResultView {
	if !consoleChatStreamResultAvailable(result) {
		return result
	}
	result.Output = consoleChatDisplayOutput(locale, result)
	return result
}

func consoleChatStreamMessage(locale string, result consoleChatResultView) *consoleChatAPIMessage {
	if strings.TrimSpace(result.Output) == "" && strings.TrimSpace(result.Reasoning) == "" {
		return nil
	}
	displayed := consoleChatDisplayedResult(locale, result)
	return &consoleChatAPIMessage{Role: "assistant", Content: displayed.Output, Reasoning: displayed.Reasoning}
}

func consoleChatStreamResult(locale string, result consoleChatResultView) *consoleChatAPIResult {
	if !consoleChatStreamResultAvailable(result) {
		return nil
	}
	displayed := consoleChatDisplayedResult(locale, result)
	apiResult := consoleChatAPIResultView(displayed)
	return &apiResult
}

type consoleChatStreamChunk struct {
	Delta        string
	Reasoning    string
	ResponseID   string
	FinishReason string
	Done         bool
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
	Data      string `json:"data,omitempty"`
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
	if _, err := handler.ensureConsoleChatEnvironment(r.Context(), r, &state); err != nil {
		state.Error = err.Error()
		handler.streamChatReplace(w, r, state)
		return
	}

	requestID := requestID(r)
	history := state.Messages
	userMessage := buildConsoleChatMessageWithAttachments(locale, "user", state.Form.Draft, state.Form.Attachments)
	var streamWriter *consoleChatEventStreamWriter
	var execution consoleChatExecution
	var executionErr error
	var localTarget consoleChatTarget
	var hasLocalTarget bool
	var clientDisconnected atomic.Bool
	if state.Form.Environment != consoleChatEnvironmentLocal {
		worker, key, account, cloudSession, targetErr := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, state.Form.ClientSessionID)
		if targetErr != nil {
			state.Error = targetErr.Error()
			_ = handler.streamChatReplace(w, r, state)
			return
		}
		run, started, startErr := handler.startConsoleCloudAgentRun(r, state, worker, key, account, cloudSession, history, userMessage, requestID)
		if startErr != nil {
			state.Error = fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), startErr.Error())
			_ = handler.streamChatReplace(w, r, state)
			return
		}
		handler.streamConsoleCloudAgentRun(w, r, state.Form.ClientSessionID, run, func() {
			if started {
				run.start()
			}
		})
		return
	} else {
		handler.startChatEventStream(w)
		streamWriter = newConsoleChatEventStreamWriter(handler, w)
		target, errorMessage := handler.resolveConsoleChatTarget(r.Context(), r, state.Form.PublicName)
		if errorMessage != "" {
			state.Error = errorMessage
			handler.streamChatReplace(w, r, state)
			return
		}
		localTarget = target
		hasLocalTarget = true
		executionCtx, executionCancel := context.WithCancel(context.WithoutCancel(r.Context()))
		defer executionCancel()
		streamCtx, streamCancel := context.WithCancel(r.Context())
		defer streamCancel()
		consoleUserID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
		started := time.Now()
		executionProtocol := handler.consoleChatExecutionProtocol(target.Route, target.Provider, history, state.Form.Attachments)
		log.Printf("console chat stream start request_id=%s user_id=%s model=%s provider=%s protocol=%s proxy_profile=%s attachments=%d prompt_chars=%d provider_timeout_s=%d stream_idle_timeout_s=%d", requestID, consoleUserID, target.Route.PublicName, target.Provider.ID, executionProtocol, target.Profile.ID, len(state.Form.Attachments), len(strings.TrimSpace(state.Form.Draft)), domain.EffectiveProviderTimeoutSeconds(target.Provider), consoleChatStreamIdleTimeoutSeconds(target.Provider, target.Profile))
		heartbeatErrCh := make(chan error, 1)
		heartbeatDone := streamWriter.StartHeartbeat(streamCtx, consoleChatHeartbeatInterval, func(err error) {
			log.Printf("console chat stream heartbeat_failed request_id=%s model=%s provider=%s protocol=%s proxy_profile=%s err=%v", requestID, target.Route.PublicName, target.Provider.ID, executionProtocol, target.Profile.ID, err)
			clientDisconnected.Store(true)
			select {
			case heartbeatErrCh <- err:
			default:
			}
			streamCancel()
		})
		execution, executionErr = handler.runConsoleChatTurnWithContinuation(executionCtx, consoleUserID, executionProtocol, target.Provider, target.Route, target.Profile, state.Form.SystemPrompt, state.Form.ReasoningEffort, history, state.Form.Draft, state.Form.Attachments, true, func(delta string) error {
			if clientDisconnected.Load() {
				return nil
			}
			if writeErr := streamWriter.Write(consoleChatStreamEvent{Type: "delta", Delta: delta}); writeErr != nil {
				clientDisconnected.Store(true)
				streamCancel()
			}
			return nil
		}, func(reasoning string) error {
			if clientDisconnected.Load() {
				return nil
			}
			if writeErr := streamWriter.Write(consoleChatStreamEvent{Type: "reasoning", Reasoning: reasoning}); writeErr != nil {
				clientDisconnected.Store(true)
				streamCancel()
			}
			return nil
		})
		executionCancel()
		streamCancel()
		<-heartbeatDone
		if r.Context().Err() != nil {
			clientDisconnected.Store(true)
		}
		select {
		case heartbeatErr := <-heartbeatErrCh:
			if heartbeatErr != nil && !clientDisconnected.Load() && (executionErr == nil || errors.Is(executionErr, context.Canceled)) {
				executionErr = heartbeatErr
			}
		default:
		}
		if executionErr != nil {
			log.Printf("console chat stream interrupted request_id=%s user_id=%s model=%s provider=%s protocol=%s proxy_profile=%s reason_code=%s status=%d finish_reason=%q duration_ms=%d output_chars=%d reasoning_chars=%d total_tokens=%d err=%v", requestID, consoleUserID, target.Route.PublicName, target.Provider.ID, executionProtocol, target.Profile.ID, consoleChatErrorCode(executionErr), execution.StatusCode, execution.Result.FinishReason, execution.Result.DurationMS, len(strings.TrimSpace(execution.Result.Output)), len(strings.TrimSpace(execution.Result.Reasoning)), execution.Result.TotalTokens, executionErr)
		} else {
			log.Printf("console chat stream completed request_id=%s user_id=%s model=%s provider=%s protocol=%s proxy_profile=%s status=%d finish_reason=%q duration_ms=%d output_chars=%d reasoning_chars=%d total_tokens=%d", requestID, consoleUserID, target.Route.PublicName, target.Provider.ID, executionProtocol, target.Profile.ID, execution.StatusCode, execution.Result.FinishReason, execution.Result.DurationMS, len(strings.TrimSpace(execution.Result.Output)), len(strings.TrimSpace(execution.Result.Reasoning)), execution.Result.TotalTokens)
		}
		persistConsoleChatOutcome(context.WithoutCancel(r.Context()), handler.store, requestID, consoleUserID, executionProtocol, target.Route, target.Provider, target.PricingRule, started, execution)
	}
	state.Messages = append(history, userMessage)
	if executionErr != nil {
		failureDetail := executionErr.Error()
		if hasLocalTarget {
			failureDetail = handler.consoleChatStreamFailureDetail(r, executionErr, localTarget.Provider, localTarget.Profile)
		}
		state.Error = fmt.Sprintf(handler.requestText(r, "对话失败：%s", "Chat failed: %s"), failureDetail)
		state.Messages = consoleChatAppendResultMessage(locale, state.Messages, execution.Result)
		handler.syncConsoleChatPageSession(context.WithoutCancel(r.Context()), r, &state, state.Messages, consoleChatSessionStatusForError(execution.Result), requestID, execution.Result.ResponseID, failureDetail)
		if clientDisconnected.Load() {
			return
		}
		if writeErr := streamWriter.Write(consoleChatStreamEvent{Type: "error", Error: state.Error, Message: consoleChatStreamMessage(locale, execution.Result), Result: consoleChatStreamResult(locale, execution.Result)}); writeErr != nil {
			if hasLocalTarget {
				log.Printf("console chat stream error_event_write_failed request_id=%s model=%s provider=%s protocol=%s proxy_profile=%s err=%v", requestID, localTarget.Route.PublicName, localTarget.Provider.ID, localTarget.Protocol, localTarget.Profile.ID, writeErr)
			} else {
				log.Printf("console cloud agent stream error_event_write_failed request_id=%s worker=%s err=%v", requestID, execution.Result.ProviderID, writeErr)
			}
		}
		if replaceErr := handler.streamChatReplace(w, r, state); replaceErr != nil {
			if hasLocalTarget {
				log.Printf("console chat stream replace_write_failed request_id=%s model=%s provider=%s protocol=%s proxy_profile=%s err=%v", requestID, localTarget.Route.PublicName, localTarget.Provider.ID, localTarget.Protocol, localTarget.Profile.ID, replaceErr)
			} else {
				log.Printf("console cloud agent stream replace_write_failed request_id=%s worker=%s err=%v", requestID, execution.Result.ProviderID, replaceErr)
			}
		}
		return
	}
	state.Form.Draft = ""
	state.Form.Attachments = nil
	execution.Result = consoleChatDisplayedResult(locale, execution.Result)
	state.Messages = append(state.Messages, buildConsoleChatMessageWithReasoning(locale, "assistant", execution.Result.Output, execution.Result.Reasoning))
	handler.syncConsoleChatPageSession(context.WithoutCancel(r.Context()), r, &state, state.Messages, consoleChatSessionStatusCompleted, requestID, execution.Result.ResponseID, "")
	if clientDisconnected.Load() {
		return
	}
	if writeErr := streamWriter.Write(consoleChatStreamEvent{Type: "done", Message: consoleChatStreamMessage(locale, execution.Result), Result: consoleChatStreamResult(locale, execution.Result)}); writeErr != nil {
		if hasLocalTarget {
			log.Printf("console chat stream done_event_write_failed request_id=%s model=%s provider=%s protocol=%s proxy_profile=%s err=%v", requestID, localTarget.Route.PublicName, localTarget.Provider.ID, localTarget.Protocol, localTarget.Profile.ID, writeErr)
		} else {
			log.Printf("console cloud agent stream done_event_write_failed request_id=%s worker=%s err=%v", requestID, execution.Result.ProviderID, writeErr)
		}
	}
	state.Result = &execution.Result
	if replaceErr := handler.streamChatReplace(w, r, state); replaceErr != nil {
		if hasLocalTarget {
			log.Printf("console chat stream replace_write_failed request_id=%s model=%s provider=%s protocol=%s proxy_profile=%s err=%v", requestID, localTarget.Route.PublicName, localTarget.Provider.ID, localTarget.Protocol, localTarget.Profile.ID, replaceErr)
		} else {
			log.Printf("console cloud agent stream replace_write_failed request_id=%s worker=%s err=%v", requestID, execution.Result.ProviderID, replaceErr)
		}
	}
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

func runConsoleRawChatTurn(ctx context.Context, protocol string, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, reasoningEffort string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView, stream bool, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	prompt := strings.TrimSpace(userInput)
	if prompt == "" && len(attachments) == 0 {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadRequest, Stream: stream}}, errors.New("message is empty")
	}
	if protocol != domain.ProtocolOpenAI && protocol != domain.ProtocolAnthropic {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadRequest, Stream: stream}}, &consoleUpstreamError{StatusCode: http.StatusBadRequest, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}

	timeoutSeconds := provider.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 90
	}

	chatCtx := ctx
	cancel := func() {}
	if !stream {
		chatCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	}
	defer cancel()

	httpClient, err := proxytransport.NewTransportFactory().HTTPClient(chatCtx, provider, profile, stream)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadGateway, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadGateway, Stream: stream}}, err
	}
	upstreamStream := stream && !consoleChatIsImageGenerationModel(route)
	body, err := buildConsoleChatRequestBody(protocol, provider, route, systemPrompt, history, prompt, attachments, upstreamStream, reasoningEffort)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadRequest, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadRequest, Stream: stream}}, err
	}
	request, err := buildConsoleChatUpstreamRequest(chatCtx, provider, route, protocol, body, upstreamStream)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusInternalServerError, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusInternalServerError, Stream: stream}}, err
	}

	started := time.Now()
	response, err := httpClient.Do(request)
	if err != nil {
		return consoleChatExecution{StatusCode: http.StatusBadGateway, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusBadGateway, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		responseBody, _ := io.ReadAll(response.Body)
		upstreamErr := parseConsoleUpstreamError(response.StatusCode, responseBody)
		return consoleChatExecution{StatusCode: response.StatusCode, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: response.StatusCode, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, upstreamErr
	}

	if upstreamStream {
		contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if contentType == "text/event-stream" {
			return parseConsoleChatStreamResponse(response.Body, protocol, route, provider, started, onDelta, onReasoning)
		}
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return consoleChatExecution{StatusCode: response.StatusCode, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: response.StatusCode, LatencyMS: time.Since(started).Milliseconds(), Stream: stream}}, err
	}
	execution, err := parseConsoleChatJSONResponse(responseBody, protocol, route, provider, response.StatusCode, stream, started)
	if err != nil {
		return execution, err
	}
	if stream && !upstreamStream {
		if text := strings.TrimSpace(consoleChatContinuationContent(execution.Result)); text != "" && onDelta != nil {
			if callbackErr := onDelta(text); callbackErr != nil {
				return execution, fmt.Errorf("write streamed image result to client: %w", callbackErr)
			}
		}
	}
	return execution, nil
}

func runConsoleChatTurnWithContinuation(ctx context.Context, protocol string, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, reasoningEffort string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView, stream bool, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	workingHistory := cloneConsoleChatMessages(history)
	currentPrompt := strings.TrimSpace(userInput)
	currentAttachments := cloneConsoleChatAttachments(attachments)
	started := time.Now()
	aggregate := consoleChatExecution{
		Result: consoleChatResultView{
			PublicName:    route.PublicName,
			ProviderID:    provider.ID,
			ProviderName:  firstNonEmpty(provider.Name, provider.ID),
			UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
		},
		Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, Stream: stream},
	}
	var combinedOutput strings.Builder
	var combinedReasoning strings.Builder

	for attempt := 0; ; attempt++ {
		var turnOutput strings.Builder
		var turnReasoning strings.Builder
		deltaCallback := onDelta
		reasoningCallback := onReasoning
		if stream {
			deltaCallback = func(delta string) error {
				turnOutput.WriteString(delta)
				if onDelta != nil {
					return onDelta(delta)
				}
				return nil
			}
			reasoningCallback = func(reasoning string) error {
				turnReasoning.WriteString(reasoning)
				if onReasoning != nil {
					return onReasoning(reasoning)
				}
				return nil
			}
		}

		execution, err := runConsoleRawChatTurn(ctx, protocol, provider, route, profile, systemPrompt, reasoningEffort, workingHistory, currentPrompt, currentAttachments, stream, deltaCallback, reasoningCallback)
		if turnOutput.Len() == 0 {
			turnOutput.WriteString(consoleChatContinuationContent(execution.Result))
		}
		if turnReasoning.Len() == 0 {
			turnReasoning.WriteString(strings.TrimSpace(execution.Result.Reasoning))
		}
		if turnOutput.Len() > 0 {
			combinedOutput.WriteString(turnOutput.String())
		}
		if text := strings.TrimSpace(turnReasoning.String()); text != "" {
			if combinedReasoning.Len() > 0 {
				combinedReasoning.WriteString("\n\n")
			}
			combinedReasoning.WriteString(text)
		}
		mergeConsoleChatExecutionTotals(&aggregate, execution)
		finalizeConsoleChatExecutionAggregate(&aggregate, route, provider, started, combinedOutput.String(), combinedReasoning.String())
		if err != nil {
			return aggregate, err
		}
		if consoleChatIsImageGenerationModel(route) {
			return aggregate, nil
		}
		if !consoleChatFinishReasonNeedsContinuation(execution.Result.FinishReason) || attempt >= consoleChatAutoContinuationLimit {
			return aggregate, nil
		}

		workingHistory = append(workingHistory, consoleChatMessageView{Role: "user", Content: currentPrompt, Attachments: cloneConsoleChatAttachments(currentAttachments)})
		if assistantContent := strings.TrimSpace(turnOutput.String()); assistantContent != "" {
			workingHistory = append(workingHistory, consoleChatMessageView{Role: "assistant", Content: assistantContent})
		}
		currentPrompt = consoleChatContinuationPrompt
		currentAttachments = nil
	}
}

func consoleChatContinuationContent(result consoleChatResultView) string {
	content := result.Output
	if content == consoleChatEmptyOutput {
		return ""
	}
	return content
}

func consoleChatFinishReasonNeedsContinuation(finishReason string) bool {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens":
		return true
	default:
		return false
	}
}

func mergeConsoleChatExecutionTotals(target *consoleChatExecution, next consoleChatExecution) {
	if target == nil {
		return
	}
	if next.StatusCode != 0 {
		target.StatusCode = next.StatusCode
	}
	if next.Result.PublicName != "" {
		target.Result.PublicName = next.Result.PublicName
	}
	if next.Result.ProviderID != "" {
		target.Result.ProviderID = next.Result.ProviderID
	}
	if next.Result.ProviderName != "" {
		target.Result.ProviderName = next.Result.ProviderName
	}
	if next.Result.UpstreamModel != "" {
		target.Result.UpstreamModel = next.Result.UpstreamModel
	}
	if next.Result.ResponseID != "" {
		target.Result.ResponseID = next.Result.ResponseID
	}
	if next.Result.FinishReason != "" {
		target.Result.FinishReason = next.Result.FinishReason
	}
	addConsoleChatUsageTotals(&target.Usage, next.Usage)
}

func addConsoleChatUsageTotals(target *domain.UsageRecord, next domain.UsageRecord) {
	if target == nil {
		return
	}
	target.InputTokens += next.InputTokens
	target.OutputTokens += next.OutputTokens
	target.CacheCreationTokens += next.CacheCreationTokens
	target.CacheReadTokens += next.CacheReadTokens
	target.TotalTokens += next.TotalTokens
	target.CostMicroCents += next.CostMicroCents
	target.Stream = target.Stream || next.Stream
	if next.StatusCode != 0 {
		target.StatusCode = next.StatusCode
	}
	if target.Currency == "" && next.Currency != "" {
		target.Currency = next.Currency
	}
}

func finalizeConsoleChatExecutionAggregate(target *consoleChatExecution, route domain.ModelRoute, provider domain.Provider, started time.Time, output string, reasoning string) {
	if target == nil {
		return
	}
	target.Result.PublicName = firstNonEmpty(target.Result.PublicName, route.PublicName)
	target.Result.ProviderID = firstNonEmpty(target.Result.ProviderID, provider.ID)
	target.Result.ProviderName = firstNonEmpty(target.Result.ProviderName, provider.Name, provider.ID)
	target.Result.UpstreamModel = firstNonEmpty(target.Result.UpstreamModel, route.UpstreamModel, route.PublicName)
	target.Usage.Currency = firstNonEmpty(target.Usage.Currency, domain.DefaultBillingCurrency)
	target.Usage.LatencyMS = time.Since(started).Milliseconds()
	if target.Usage.TotalTokens == 0 {
		target.Usage.TotalTokens = target.Usage.InputTokens + target.Usage.OutputTokens + target.Usage.CacheCreationTokens + target.Usage.CacheReadTokens
	}
	target.Result.Output = strings.TrimSpace(output)
	if target.Result.Output == "" {
		target.Result.Output = consoleChatEmptyOutput
	}
	target.Result.Reasoning = strings.TrimSpace(reasoning)
	target.Result.DurationMS = target.Usage.LatencyMS
	target.Result.TotalTokens = target.Usage.TotalTokens
}

func buildConsoleChatRequestBody(protocol string, provider domain.Provider, route domain.ModelRoute, systemPrompt string, history []consoleChatMessageView, prompt string, attachments []consoleChatAttachmentView, stream bool, reasoningEffort string) ([]byte, error) {
	upstreamModel := firstNonEmpty(route.UpstreamModel, route.PublicName)
	if protocol == domain.ProtocolOpenAI && consoleChatImageGenerationKind(route) == consoleChatImageGenImagesAPI {
		return json.Marshal(buildConsoleOpenAIImageGenerationPayload(upstreamModel, systemPrompt, prompt, attachments))
	}
	appliedReasoningEffort := consoleChatAppliedReasoningEffort(route, provider, reasoningEffort)
	switch protocol {
	case domain.ProtocolAnthropic:
		payload := map[string]any{
			"model":      upstreamModel,
			"max_tokens": consoleChatCompletionTokens(route),
			"messages":   buildConsoleAnthropicMessages(history, prompt, attachments),
			"stream":     stream,
		}
		if systemPrompt = strings.TrimSpace(systemPrompt); systemPrompt != "" {
			payload["system"] = systemPrompt
		}
		if appliedReasoningEffort != "" {
			payload["thinking"] = map[string]any{"type": "enabled"}
			payload["output_config"] = map[string]any{"effort": appliedReasoningEffort}
		}
		return json.Marshal(payload)
	default:
		payload := map[string]any{
			"model":      upstreamModel,
			"max_tokens": consoleChatCompletionTokens(route),
			"messages":   buildConsoleOpenAIMessages(systemPrompt, history, prompt, attachments),
			"stream":     stream,
		}
		if consoleChatImageGenerationKind(route) == consoleChatImageGenChatAPI {
			payload["modalities"] = consoleChatImageGenerationModalities(route)
		}
		if stream {
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
		if appliedReasoningEffort != "" {
			payload["thinking"] = map[string]any{"type": "enabled"}
			payload["reasoning_effort"] = appliedReasoningEffort
		}
		return json.Marshal(payload)
	}
}

func buildConsoleOpenAIImageGenerationPayload(upstreamModel string, systemPrompt string, prompt string, attachments []consoleChatAttachmentView) map[string]any {
	finalPrompt := strings.TrimSpace(prompt)
	if instructions := strings.TrimSpace(systemPrompt); instructions != "" {
		if finalPrompt != "" {
			finalPrompt = instructions + "\n\n" + finalPrompt
		} else {
			finalPrompt = instructions
		}
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.URL) == "" {
			continue
		}
		finalPrompt += "\n\n" + consoleChatAttachmentReferenceText(attachment)
	}
	return map[string]any{
		"model":           upstreamModel,
		"prompt":          strings.TrimSpace(finalPrompt),
		"response_format": "url",
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
			parts = append(parts, consoleChatAnthropicContentPayload{Type: "image", Source: consoleChatAnthropicAttachmentSource(attachment)})
		case consoleChatAttachmentIsDocument(attachment.MediaType):
			parts = append(parts, consoleChatAnthropicContentPayload{Type: "document", Title: attachment.Name, Source: consoleChatAnthropicAttachmentSource(attachment)})
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

func consoleChatAnthropicAttachmentSource(attachment consoleChatAttachmentView) *consoleChatAnthropicSourcePayload {
	if mediaType, data, ok := consoleChatAttachmentDataPayload(attachment); ok {
		return &consoleChatAnthropicSourcePayload{Type: "base64", MediaType: mediaType, Data: data}
	}
	return &consoleChatAnthropicSourcePayload{Type: "url", URL: attachment.URL, MediaType: attachment.MediaType}
}

func consoleChatAttachmentDataPayload(attachment consoleChatAttachmentView) (string, string, bool) {
	trimmedURL := strings.TrimSpace(attachment.URL)
	if !strings.HasPrefix(trimmedURL, "data:") {
		return "", "", false
	}
	raw := strings.TrimPrefix(trimmedURL, "data:")
	header, data, ok := strings.Cut(raw, ",")
	if !ok {
		return "", "", false
	}
	header = strings.TrimSpace(header)
	data = strings.TrimSpace(data)
	if header == "" || data == "" || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", "", false
	}
	mediaType := strings.TrimSpace(strings.TrimSuffix(header, ";base64"))
	if mediaType == "" {
		mediaType = strings.TrimSpace(attachment.MediaType)
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, data, true
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

func buildConsoleChatUpstreamRequest(ctx context.Context, provider domain.Provider, route domain.ModelRoute, protocol string, body []byte, stream bool) (*http.Request, error) {
	upstreamURL, err := joinConsoleChatUpstreamURL(domain.ProviderBaseURLForProtocol(provider, protocol), consoleChatUpstreamEndpoint(protocol, route))
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

func consoleChatUpstreamEndpoint(protocol string, route domain.ModelRoute) string {
	if protocol == domain.ProtocolOpenAI && consoleChatImageGenerationKind(route) == consoleChatImageGenImagesAPI {
		return "/v1/images/generations"
	}
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
		return consoleChatExecution{StatusCode: statusCode, Usage: domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: statusCode, Stream: stream, LatencyMS: time.Since(started).Milliseconds()}}, err
	}
	usage := parseConsoleChatUsageFromJSON(body, protocol)
	usage.Currency = firstNonEmpty(usage.Currency, domain.DefaultBillingCurrency)
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
	if protocol == domain.ProtocolOpenAI && consoleChatImageGenerationKind(route) == consoleChatImageGenImagesAPI {
		result.Output = consoleOpenAIImageOutput(payload)
		result.FinishReason = "stop"
		if strings.TrimSpace(result.Output) == "" {
			result.Output = consoleChatEmptyOutput
		}
		return consoleChatExecution{Result: result, Usage: usage, StatusCode: statusCode}, nil
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
					result.Output = consoleChatAssistantOutputWithImages(consoleTextContent(message["content"]), message)
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

func consoleOpenAIImageOutput(payload map[string]any) string {
	data, ok := payload["data"].([]any)
	if !ok || len(data) == 0 {
		return ""
	}
	parts := make([]string, 0, len(data))
	for index, item := range data {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		label := fmt.Sprintf("Generated image %d", index+1)
		if value := strings.TrimSpace(consoleValueString(entry["url"])); value != "" {
			parts = append(parts, fmt.Sprintf("![%s](%s)", label, value))
			continue
		}
		b64 := strings.TrimSpace(consoleValueString(entry["b64_json"]))
		if b64 == "" {
			continue
		}
		if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("![%s](data:image/png;base64,%s)", label, b64))
	}
	return strings.Join(parts, "\n\n")
}

func parseConsoleChatStreamResponse(body io.Reader, protocol string, route domain.ModelRoute, provider domain.Provider, started time.Time, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	reader := bufio.NewReader(body)
	usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency, StatusCode: http.StatusOK, Stream: true}
	result := consoleChatResultView{
		PublicName:    route.PublicName,
		ProviderID:    provider.ID,
		ProviderName:  firstNonEmpty(provider.Name, provider.ID),
		UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
	}
	var output strings.Builder
	var reasoning strings.Builder
	completed := false
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
			if chunk.Done || chunk.FinishReason != "" {
				completed = true
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
						return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, fmt.Errorf("write streamed delta to client: %w", callbackErr)
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
						return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, fmt.Errorf("write streamed reasoning to client: %w", callbackErr)
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !completed {
					usage.LatencyMS = time.Since(started).Milliseconds()
					result.Output = strings.TrimSpace(output.String())
					result.Reasoning = strings.TrimSpace(reasoning.String())
					result.DurationMS = usage.LatencyMS
					result.TotalTokens = usage.TotalTokens
					return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, fmt.Errorf("upstream stream ended before completion marker: %w", io.ErrUnexpectedEOF)
				}
				break
			}
			usage.LatencyMS = time.Since(started).Milliseconds()
			result.Output = strings.TrimSpace(output.String())
			result.Reasoning = strings.TrimSpace(reasoning.String())
			result.DurationMS = usage.LatencyMS
			result.TotalTokens = usage.TotalTokens
			return consoleChatExecution{Result: result, Usage: usage, StatusCode: usage.StatusCode}, fmt.Errorf("read streamed response: %w", err)
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
	if raw == "" {
		return consoleChatStreamChunk{}, nil
	}
	if raw == "[DONE]" {
		return consoleChatStreamChunk{Done: true}, nil
	}
	if protocol == domain.ProtocolAnthropic && strings.EqualFold(raw, "message_stop") {
		return consoleChatStreamChunk{Done: true}, nil
	}
	if raw == "" {
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
		case "message_stop":
			chunk.Done = true
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
				if imageDelta := consoleChatStreamImageDelta(delta["images"]); imageDelta != "" {
					if chunk.Delta != "" {
						chunk.Delta += "\n\n" + imageDelta
					} else {
						chunk.Delta = imageDelta
					}
				}
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
			usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency, InputTokens: consoleIntNumber(payload["input_tokens"])}
			usage.TotalTokens = usage.InputTokens
			return usage
		}
	}
	if usagePayload == nil {
		return domain.UsageRecord{}
	}
	usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency}
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
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "stream_unexpected_eof"
	case consoleChatErrorIsStreamTimeout(err):
		return "stream_idle_timeout"
	case consoleChatErrorIsClientDisconnect(err):
		return "client_disconnected"
	case errors.Is(err, context.Canceled):
		return "stream_canceled"
	}
	return modelTestErrorCode(err)
}

func consoleChatErrorIsStreamTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func consoleChatErrorIsClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func consoleChatStreamIdleTimeoutSeconds(provider domain.Provider, profile domain.ProxyProfile) int {
	timeoutSeconds := domain.EffectiveProviderStreamIdleTimeoutSeconds(provider)
	if profile.StreamIdleTimeoutSeconds > 0 {
		return profile.StreamIdleTimeoutSeconds
	}
	if profile.TimeoutSeconds > 0 && timeoutSeconds < profile.TimeoutSeconds {
		return profile.TimeoutSeconds
	}
	return timeoutSeconds
}

func (handler *Handler) consoleChatStreamFailureDetail(r *http.Request, err error, provider domain.Provider, profile domain.ProxyProfile) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	var upstreamErr *consoleUpstreamError
	if errors.As(err, &upstreamErr) {
		return raw
	}
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Sprintf(handler.requestText(r, "上游流在返回完成标记前提前断开（%s）。", "The upstream stream closed before sending a completion marker (%s)."), raw)
	case consoleChatErrorIsStreamTimeout(err):
		return fmt.Sprintf(handler.requestText(r, "等待上游下一段输出超时，已超过 %d 秒的流空闲超时（%s）。", "Timed out waiting for the next upstream chunk after exceeding the %d-second stream idle timeout (%s)."), consoleChatStreamIdleTimeoutSeconds(provider, profile), raw)
	case consoleChatErrorIsClientDisconnect(err):
		return fmt.Sprintf(handler.requestText(r, "浏览器连接已断开，网关停止继续写入流（%s）。", "The browser connection closed, so the gateway stopped writing the stream (%s)."), raw)
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf(handler.requestText(r, "流式请求在完成前被取消（%s）。", "The stream was canceled before completion (%s)."), raw)
	default:
		return raw
	}
}
