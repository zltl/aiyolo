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
		PublicName:  run.request.PublicName,
		History:     cloneConsoleChatMessages(run.request.History),
		UserInput:   run.request.UserInput,
		Attachments: cloneConsoleChatAttachments(run.request.Attachments),
		Stream:      true,
		OnDelta: func(delta string) error {
			run.appendDelta(delta)
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
				PublicName:  state.Form.PublicName,
				History:     cloneConsoleChatMessages(history),
				UserInput:   state.Form.Draft,
				Attachments: cloneConsoleChatAttachments(state.Form.Attachments),
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
		if event, ok, err := handler.consoleCloudAgentStoredResumeEvent(r.Context(), locale, userID, sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else if ok {
			_ = streamWriter.Write(event)
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

func (handler *Handler) consoleCloudAgentStoredResumeEvent(ctx context.Context, locale string, userID string, sessionID string) (consoleChatStreamEvent, bool, error) {
	record, err := handler.store.GetConsoleChatSession(ctx, userID, sessionID)
	if err != nil {
		if err == storage.ErrNotFound {
			return consoleChatStreamEvent{}, false, nil
		}
		return consoleChatStreamEvent{}, false, err
	}
	messages := decodeConsoleChatSessionMessages(locale, record.MessagesJSON, handler.cfg.ChatAttachments)
	message := consoleChatLastAssistantStreamMessage(messages)
	switch strings.TrimSpace(record.Status) {
	case consoleChatSessionStatusCompleted:
		return consoleChatStreamEvent{Type: "done", Message: message}, true, nil
	case consoleChatSessionStatusInterrupted, consoleChatSessionStatusFailed:
		errorText := strings.TrimSpace(record.LastError)
		if errorText != "" {
			errorText = fmt.Sprintf(consoleText(locale, "对话失败：%s", "Chat failed: %s"), errorText)
		}
		return consoleChatStreamEvent{Type: "error", Error: errorText, Message: message}, true, nil
	case consoleChatSessionStatusStreaming:
		if message != nil {
			return consoleChatStreamEvent{Type: "sync", Message: message}, true, nil
		}
	}
	return consoleChatStreamEvent{}, false, nil
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
