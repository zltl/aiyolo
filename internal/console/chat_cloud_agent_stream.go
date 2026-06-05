package console

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const consoleChatStreamResumePath = "/console/chat/stream/resume"

type consoleCloudAgentRunRegistry struct {
	mu   sync.Mutex
	runs map[string]*consoleCloudAgentRun
}

type consoleCloudAgentRun struct {
	handler      *Handler
	registry     *consoleCloudAgentRunRegistry
	key          string
	locale       string
	userID       string
	sessionID    string
	publicName   string
	systemPrompt string
	requestID    string
	baseMessages []consoleChatMessageView
	worker       domain.WorkerServer
	sshKey       domain.WorkerSSHKey
	account      domain.CloudAgentAccount
	cloudSession domain.CloudAgentSession
	request      consoleCloudAgentChatRequest

	mu          sync.Mutex
	content     string
	reasoning   string
	result      consoleChatResultView
	eventError  string
	lastError   string
	done        bool
	subscribers map[int]chan consoleChatStreamEvent
	nextSubID   int
}

type consoleCloudAgentRunSnapshot struct {
	syncEvent  *consoleChatStreamEvent
	finalEvent *consoleChatStreamEvent
}

func newConsoleCloudAgentRunRegistry() *consoleCloudAgentRunRegistry {
	return &consoleCloudAgentRunRegistry{runs: make(map[string]*consoleCloudAgentRun)}
}

func consoleCloudAgentRunKey(userID string, sessionID string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)
}

func (registry *consoleCloudAgentRunRegistry) startOrGet(key string, build func() *consoleCloudAgentRun) (*consoleCloudAgentRun, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if existing := registry.runs[key]; existing != nil {
		return existing, false
	}
	run := build()
	registry.runs[key] = run
	return run, true
}

func (registry *consoleCloudAgentRunRegistry) get(key string) *consoleCloudAgentRun {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.runs[key]
}

func (registry *consoleCloudAgentRunRegistry) active(userID string, sessionID string) *consoleCloudAgentRun {
	run := registry.get(consoleCloudAgentRunKey(userID, sessionID))
	if run == nil {
		return nil
	}
	run.mu.Lock()
	done := run.done
	run.mu.Unlock()
	if done {
		return nil
	}
	return run
}

func (registry *consoleCloudAgentRunRegistry) delete(key string, run *consoleCloudAgentRun) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.runs[key] == run {
		delete(registry.runs, key)
	}
}

func (run *consoleCloudAgentRun) start() {
	go run.execute()
}

func (run *consoleCloudAgentRun) execute() {
	defer run.registry.delete(run.key, run)
	execution, executionErr := run.handler.runCloudAgentChat(context.Background(), run.worker, run.sshKey, run.account, run.cloudSession, consoleCloudAgentChatRequest{
		SessionID:                    run.sessionID,
		PublicName:                   run.request.PublicName,
		History:                      cloneConsoleChatMessages(run.request.History),
		UserInput:                    run.request.UserInput,
		Attachments:                  cloneConsoleChatAttachments(run.request.Attachments),
		ShellActiveTerminalID:        run.request.ShellActiveTerminalID,
		ShellCurrentWorkingDirectory: run.request.ShellCurrentWorkingDirectory,
		Stream:                       true,
		OnDelta: func(delta string) error {
			run.appendDelta(delta)
			return nil
		},
		OnReasoning: func(reasoning string) error {
			run.appendReasoning(reasoning)
			return nil
		},
	})
	run.complete(execution, executionErr)
}

func (run *consoleCloudAgentRun) appendDelta(delta string) {
	if delta == "" {
		return
	}
	run.mu.Lock()
	run.content += delta
	run.result.Output = run.content
	run.mu.Unlock()
	run.persistStreamingProgress()
	run.broadcast(consoleChatStreamEvent{Type: "delta", Delta: delta})
}

func (run *consoleCloudAgentRun) appendReasoning(reasoning string) {
	if reasoning == "" {
		return
	}
	run.mu.Lock()
	run.reasoning += reasoning
	run.mu.Unlock()
	run.persistStreamingProgress()
	run.broadcast(consoleChatStreamEvent{Type: "reasoning", Reasoning: reasoning})
}

func (run *consoleCloudAgentRun) persistStreamingStart() error {
	_, err := run.handler.persistConsoleChatSessionForUser(context.Background(), run.locale, run.userID, run.sessionID, run.publicName, run.systemPrompt, "", nil, cloneConsoleChatMessages(run.baseMessages), consoleChatSessionStatusStreaming, run.requestID, "", "")
	return err
}

func (run *consoleCloudAgentRun) persistStreamingProgress() {
	run.mu.Lock()
	content := run.content
	reasoning := run.reasoning
	responseID := run.result.ResponseID
	run.mu.Unlock()
	messages := consoleChatAppendAssistantProgress(run.locale, run.baseMessages, content, reasoning)
	if _, err := run.handler.persistConsoleChatSessionForUser(context.Background(), run.locale, run.userID, run.sessionID, run.publicName, run.systemPrompt, "", nil, messages, consoleChatSessionStatusStreaming, run.requestID, responseID, ""); err != nil {
		log.Printf("console cloud agent stream progress persist failed session_id=%s err=%v", run.sessionID, err)
	}
}

func (run *consoleCloudAgentRun) complete(execution consoleChatExecution, executionErr error) {
	displayed := consoleChatDisplayedResult(run.locale, execution.Result)
	run.mu.Lock()
	if strings.TrimSpace(displayed.Output) == "" && strings.TrimSpace(run.content) != "" {
		displayed.Output = run.content
	}
	if strings.TrimSpace(displayed.Reasoning) == "" && strings.TrimSpace(run.reasoning) != "" {
		displayed.Reasoning = run.reasoning
	}
	if strings.TrimSpace(displayed.ResponseID) == "" {
		displayed.ResponseID = strings.TrimSpace(run.result.ResponseID)
	}
	run.result = displayed
	run.done = true
	run.mu.Unlock()

	if executionErr != nil {
		failureDetail := strings.TrimSpace(executionErr.Error())
		if failureDetail == "" {
			failureDetail = consoleText(run.locale, "Cloud Agent 执行失败。", "Cloud agent execution failed.")
		}
		messages := consoleChatAppendResultMessage(run.locale, run.baseMessages, displayed)
		status := consoleChatSessionStatusForError(displayed)
		if _, err := run.handler.persistConsoleChatSessionForUser(context.Background(), run.locale, run.userID, run.sessionID, run.publicName, run.systemPrompt, "", nil, messages, status, run.requestID, displayed.ResponseID, failureDetail); err != nil {
			log.Printf("console cloud agent stream failure persist failed session_id=%s err=%v", run.sessionID, err)
		}
		run.mu.Lock()
		run.lastError = failureDetail
		run.eventError = fmt.Sprintf(consoleText(run.locale, "对话失败：%s", "Chat failed: %s"), failureDetail)
		run.mu.Unlock()
		run.broadcast(consoleChatStreamEvent{
			Type:    "error",
			Error:   fmt.Sprintf(consoleText(run.locale, "对话失败：%s", "Chat failed: %s"), failureDetail),
			Message: consoleChatStreamMessage(run.locale, displayed),
			Result:  consoleChatStreamResult(run.locale, displayed),
		})
		return
	}

	messages := append(cloneConsoleChatMessages(run.baseMessages), buildConsoleChatMessageWithReasoning(run.locale, "assistant", displayed.Output, displayed.Reasoning))
	if _, err := run.handler.persistConsoleChatSessionForUser(context.Background(), run.locale, run.userID, run.sessionID, run.publicName, run.systemPrompt, "", nil, messages, consoleChatSessionStatusCompleted, run.requestID, displayed.ResponseID, ""); err != nil {
		log.Printf("console cloud agent stream completion persist failed session_id=%s err=%v", run.sessionID, err)
	}
	run.broadcast(consoleChatStreamEvent{
		Type:    "done",
		Message: consoleChatStreamMessage(run.locale, displayed),
		Result:  consoleChatStreamResult(run.locale, displayed),
	})
}

func (run *consoleCloudAgentRun) subscribe() (consoleCloudAgentRunSnapshot, <-chan consoleChatStreamEvent, func()) {
	run.mu.Lock()
	defer run.mu.Unlock()
	snapshot := consoleCloudAgentRunSnapshot{}
	if run.done {
		if strings.TrimSpace(run.eventError) != "" {
			snapshot.finalEvent = &consoleChatStreamEvent{
				Type:    "error",
				Error:   run.eventError,
				Message: consoleChatStreamMessage(run.locale, run.result),
				Result:  consoleChatStreamResult(run.locale, run.result),
			}
		} else {
			snapshot.finalEvent = &consoleChatStreamEvent{
				Type:    "done",
				Message: consoleChatStreamMessage(run.locale, run.result),
				Result:  consoleChatStreamResult(run.locale, run.result),
			}
		}
		return snapshot, nil, func() {}
	}
	if strings.TrimSpace(run.content) != "" || strings.TrimSpace(run.reasoning) != "" {
		syncEvent := consoleChatStreamEvent{
			Type: "sync",
			Message: &consoleChatAPIMessage{
				Role:      "assistant",
				Content:   run.content,
				Reasoning: run.reasoning,
			},
		}
		snapshot.syncEvent = &syncEvent
	}
	ch := make(chan consoleChatStreamEvent, 32)
	subscriptionID := run.nextSubID
	run.nextSubID++
	if run.subscribers == nil {
		run.subscribers = make(map[int]chan consoleChatStreamEvent)
	}
	run.subscribers[subscriptionID] = ch
	return snapshot, ch, func() {
		run.mu.Lock()
		defer run.mu.Unlock()
		if current := run.subscribers[subscriptionID]; current != nil {
			delete(run.subscribers, subscriptionID)
			close(current)
		}
	}
}

func (run *consoleCloudAgentRun) broadcast(event consoleChatStreamEvent) {
	run.mu.Lock()
	defer run.mu.Unlock()
	for id, subscriber := range run.subscribers {
		select {
		case subscriber <- event:
		default:
			close(subscriber)
			delete(run.subscribers, id)
		}
	}
}

func (handler *Handler) startConsoleCloudAgentRun(r *http.Request, state consoleChatPageState, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, history []consoleChatMessageView, userMessage consoleChatMessageView, requestID string) (*consoleCloudAgentRun, bool, error) {
	locale := resolveConsoleLocale(r)
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	runKey := consoleCloudAgentRunKey(userID, state.Form.ClientSessionID)
	run, started := handler.cloudAgentRuns.startOrGet(runKey, func() *consoleCloudAgentRun {
		return &consoleCloudAgentRun{
			handler:      handler,
			registry:     handler.cloudAgentRuns,
			key:          runKey,
			locale:       locale,
			userID:       userID,
			sessionID:    state.Form.ClientSessionID,
			publicName:   state.Form.PublicName,
			systemPrompt: state.Form.SystemPrompt,
			requestID:    requestID,
			baseMessages: append(cloneConsoleChatMessages(history), userMessage),
			worker:       worker,
			sshKey:       key,
			account:      account,
			cloudSession: cloudSession,
			request: consoleCloudAgentChatRequest{
				PublicName:                   state.Form.PublicName,
				PreviousResponseID:           state.LastResponseID,
				History:                      cloneConsoleChatMessages(history),
				UserInput:                    state.Form.Draft,
				Attachments:                  cloneConsoleChatAttachments(state.Form.Attachments),
				ShellActiveTerminalID:        state.Form.ShellActiveTerminalID,
				ShellCurrentWorkingDirectory: state.Form.ShellCurrentWorkingDirectory,
			},
		}
	})
	if started {
		if err := run.persistStreamingStart(); err != nil {
			handler.cloudAgentRuns.delete(runKey, run)
			return nil, false, err
		}
	}
	return run, started, nil
}

func (handler *Handler) streamConsoleCloudAgentRun(w http.ResponseWriter, r *http.Request, sessionID string, currentRun *consoleCloudAgentRun, onSubscribed func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	handler.startChatEventStream(w)
	streamWriter := newConsoleChatEventStreamWriter(handler, w)
	streamCtx, streamCancel := context.WithCancel(r.Context())
	heartbeatDone := streamWriter.StartHeartbeat(streamCtx, consoleChatHeartbeatInterval, func(error) {
		streamCancel()
	})
	defer func() {
		streamCancel()
		<-heartbeatDone
	}()

	locale := resolveConsoleLocale(r)
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	run := currentRun
	if run == nil {
		run = handler.cloudAgentRuns.get(consoleCloudAgentRunKey(userID, sessionID))
	}
	if run == nil {
		events, err := handler.consoleCloudAgentStoredResumeEvents(r.Context(), locale, userID, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pendingReconnect := false
		for _, event := range events {
			if event.Type == "reconnect" {
				pendingReconnect = true
			}
			if writeErr := streamWriter.Write(event); writeErr != nil {
				return
			}
		}
		if pendingReconnect {
			handler.streamConsoleCloudAgentASSJobResume(r, streamWriter, sessionID, locale, userID)
		}
		return
	}

	snapshot, updates, unsubscribe := run.subscribe()
	defer unsubscribe()
	if onSubscribed != nil {
		onSubscribed()
		onSubscribed = nil
	}
	if snapshot.syncEvent != nil {
		if err := streamWriter.Write(*snapshot.syncEvent); err != nil {
			return
		}
	}
	if snapshot.finalEvent != nil {
		_ = streamWriter.Write(*snapshot.finalEvent)
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-updates:
			if !ok {
				return
			}
			if err := streamWriter.Write(event); err != nil {
				return
			}
			if event.Type == "done" || event.Type == "error" {
				return
			}
		}
	}
}

func (handler *Handler) consoleCloudAgentStoredResumeEvents(ctx context.Context, locale string, userID string, sessionID string) ([]consoleChatStreamEvent, error) {
	record, err := handler.store.GetConsoleChatSession(ctx, userID, sessionID)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	messages := decodeConsoleChatSessionMessages(locale, record.MessagesJSON, handler.cfg.ChatAttachments)
	message := consoleChatLastAssistantStreamMessage(messages)
	switch strings.TrimSpace(record.Status) {
	case consoleChatSessionStatusCompleted:
		return []consoleChatStreamEvent{{Type: "done", Message: message}}, nil
	case consoleChatSessionStatusInterrupted, consoleChatSessionStatusFailed:
		errorText := strings.TrimSpace(record.LastError)
		if errorText != "" {
			errorText = fmt.Sprintf(consoleText(locale, "对话失败：%s", "Chat failed: %s"), errorText)
		}
		return []consoleChatStreamEvent{{Type: "error", Error: errorText, Message: message}}, nil
	case consoleChatSessionStatusStreaming:
		reconnectMessage := consoleText(locale,
			"服务已重启，后台任务继续运行，正在重连…",
			"Server restarted; background task still running, reconnecting...",
		)
		events := make([]consoleChatStreamEvent, 0, 2)
		if message != nil {
			events = append(events, consoleChatStreamEvent{
				Type:    "sync",
				Message: message,
			})
		}
		events = append(events, consoleChatStreamEvent{
			Type:    "reconnect",
			Error:   reconnectMessage,
			Message: message,
		})
		return events, nil
	default:
		return nil, nil
	}
}

func consoleChatLastAssistantStreamMessage(messages []consoleChatMessageView) *consoleChatAPIMessage {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if normalizeConsoleChatRole(message.Role) != "assistant" {
			continue
		}
		if strings.TrimSpace(message.Content) == "" && strings.TrimSpace(message.Reasoning) == "" {
			continue
		}
		return &consoleChatAPIMessage{
			Role:      "assistant",
			Content:   message.Content,
			Reasoning: message.Reasoning,
		}
	}
	return nil
}

func (handler *Handler) resumeCloudAgentChatStream(w http.ResponseWriter, r *http.Request) {
	handler.streamConsoleCloudAgentRun(w, r, r.URL.Query().Get("session"), nil, nil)
}

func (handler *Handler) hasActiveConsoleCloudAgentRun(userID string, sessionID string) bool {
	if handler == nil || handler.cloudAgentRuns == nil {
		return false
	}
	return handler.cloudAgentRuns.active(userID, sessionID) != nil
}

func (handler *Handler) streamConsoleCloudAgentASSJobResume(r *http.Request, streamWriter *consoleChatEventStreamWriter, sessionID, locale, userID string) {
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(r.Context(), r, sessionID)
	if err != nil {
		handler.finishConsoleCloudAgentLostResume(r, streamWriter, sessionID, locale, userID, err.Error())
		return
	}
	jobInfo, err := workerops.GetCloudAgentASSJob(r.Context(), worker, key, account, cloudSession, sessionID)
	if !workerops.CloudAgentASSJobResumable(jobInfo, err) {
		detail := consoleText(locale,
			"Cloud Agent 后台任务已结束或丢失，无法继续重连。",
			"The cloud agent background task ended or was lost; unable to reconnect.",
		)
		if err != nil && !workerops.CloudAgentASSJobNotFound(err) {
			detail = strings.TrimSpace(err.Error())
		}
		handler.finishConsoleCloudAgentLostResume(r, streamWriter, sessionID, locale, userID, detail)
		return
	}
	record, err := handler.store.GetConsoleChatSession(r.Context(), userID, sessionID)
	if err != nil {
		handler.finishConsoleCloudAgentLostResume(r, streamWriter, sessionID, locale, userID, err.Error())
		return
	}
	parser := &consoleCloudAgentStreamParser{}
	content := strings.Builder{}
	onDelta := func(delta string) error {
		content.WriteString(delta)
		return streamWriter.Write(consoleChatStreamEvent{Type: "delta", Delta: delta})
	}
	handlers := consoleCloudAgentStreamHandlers{
		OnDelta: onDelta,
		OnReasoning: func(reasoning string) error {
			return streamWriter.Write(consoleChatStreamEvent{Type: "reasoning", Reasoning: reasoning})
		},
	}
	streamErr := workerops.StreamCloudAgentASSJobLive(r.Context(), worker, key, account, cloudSession, sessionID, func(event workerops.CloudAgentASSJobStreamEvent) error {
		switch event.Type {
		case "sync", "delta":
			if event.Delta == "" {
				return nil
			}
			return parser.consumeChunk([]byte(event.Delta), handlers)
		case "error":
			if detail := strings.TrimSpace(event.Error); detail != "" {
				return fmt.Errorf("%s", detail)
			}
		}
		return nil
	})
	_ = parser.finish(handlers)
	result := consoleChatResultView{
		PublicName: firstNonEmpty(strings.TrimSpace(record.PublicName), strings.TrimSpace(account.ModelPublicName)),
		ProviderID: "cloud-agent:" + strings.TrimSpace(worker.ID),
		ProviderName: "Codex · " + strings.TrimSpace(worker.ID),
		UpstreamModel: firstNonEmpty(strings.TrimSpace(record.PublicName), strings.TrimSpace(account.ModelPublicName)),
		Output: parser.resultText(),
		Reasoning: parser.reasoningText(),
		ResponseID: firstNonEmpty(parser.sessionID, strings.TrimSpace(record.LastResponseID)),
		FinishReason: parser.finishReason,
		DurationMS: parser.durationMS,
		TotalTokens: parser.totalTokens,
	}
	if result.Output == "" {
		result.Output = strings.TrimSpace(content.String())
	}
	messages := decodeConsoleChatSessionMessages(locale, record.MessagesJSON, handler.cfg.ChatAttachments)
	if streamErr != nil {
		failureDetail := strings.TrimSpace(streamErr.Error())
		if failureDetail == "" {
			failureDetail = consoleText(locale, "Cloud Agent 执行失败。", "Cloud agent execution failed.")
		}
		messages = consoleChatAppendResultMessage(locale, messages, result)
		if _, persistErr := handler.persistConsoleChatSessionForUser(r.Context(), locale, userID, sessionID, record.PublicName, record.SystemPrompt, record.Draft, decodeConsoleChatSessionAttachments(record.DraftAttachmentsJSON, handler.cfg.ChatAttachments), messages, consoleChatSessionStatusForError(result), record.LastRequestID, result.ResponseID, failureDetail); persistErr != nil {
			log.Printf("console cloud agent ass job resume failure persist failed session_id=%s err=%v", sessionID, persistErr)
		}
		_ = streamWriter.Write(consoleChatStreamEvent{
			Type:    "error",
			Error:   fmt.Sprintf(consoleText(locale, "对话失败：%s", "Chat failed: %s"), failureDetail),
			Message: consoleChatStreamMessage(locale, result),
			Result:  consoleChatStreamResult(locale, result),
		})
		return
	}
	messages = append(cloneConsoleChatMessages(messages), buildConsoleChatMessageWithReasoning(locale, "assistant", result.Output, result.Reasoning))
	if _, persistErr := handler.persistConsoleChatSessionForUser(r.Context(), locale, userID, sessionID, record.PublicName, record.SystemPrompt, record.Draft, decodeConsoleChatSessionAttachments(record.DraftAttachmentsJSON, handler.cfg.ChatAttachments), messages, consoleChatSessionStatusCompleted, record.LastRequestID, result.ResponseID, ""); persistErr != nil {
		log.Printf("console cloud agent ass job resume completion persist failed session_id=%s err=%v", sessionID, persistErr)
	}
	_ = streamWriter.Write(consoleChatStreamEvent{
		Type:    "done",
		Message: consoleChatStreamMessage(locale, result),
		Result:  consoleChatStreamResult(locale, result),
	})
}

func (handler *Handler) finishConsoleCloudAgentLostResume(r *http.Request, streamWriter *consoleChatEventStreamWriter, sessionID, locale, userID, detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = consoleText(locale, "Cloud Agent 后台任务已结束或丢失，无法继续重连。", "The cloud agent background task ended or was lost; unable to reconnect.")
	}
	record, err := handler.store.GetConsoleChatSession(r.Context(), userID, sessionID)
	var message *consoleChatAPIMessage
	if err == nil {
		messages := decodeConsoleChatSessionMessages(locale, record.MessagesJSON, handler.cfg.ChatAttachments)
		message = consoleChatLastAssistantStreamMessage(messages)
		if _, persistErr := handler.persistConsoleChatSessionForUser(
			r.Context(),
			locale,
			userID,
			sessionID,
			record.PublicName,
			record.SystemPrompt,
			record.Draft,
			decodeConsoleChatSessionAttachments(record.DraftAttachmentsJSON, handler.cfg.ChatAttachments),
			messages,
			consoleChatSessionStatusInterrupted,
			record.LastRequestID,
			record.LastResponseID,
			detail,
		); persistErr != nil {
			log.Printf("console cloud agent lost resume persist failed session_id=%s err=%v", sessionID, persistErr)
		}
	} else if err != storage.ErrNotFound {
		log.Printf("console cloud agent lost resume session lookup failed session_id=%s err=%v", sessionID, err)
	}
	_ = streamWriter.Write(consoleChatStreamEvent{
		Type:    "error",
		Error:   fmt.Sprintf(consoleText(locale, "对话失败：%s", "Chat failed: %s"), detail),
		Message: message,
	})
}
