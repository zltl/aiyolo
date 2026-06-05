package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/zltl/aiyolo/internal/domain"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

type consoleCloudAgentChatRequest struct {
	SessionID                    string
	PublicName                   string
	PreviousResponseID           string
	History                      []consoleChatMessageView
	UserInput                    string
	Attachments                  []consoleChatAttachmentView
	ShellActiveTerminalID        string
	ShellCurrentWorkingDirectory string
	Stream                       bool
	OnDelta                      func(string) error
	OnReasoning                  func(string) error
}

type consoleCloudAgentStreamHandlers struct {
	OnDelta     func(string) error
	OnReasoning func(string) error
}

type consoleCloudAgentStreamParser struct {
	pending        strings.Builder
	output         strings.Builder
	reasoning      strings.Builder
	finalOutput    string
	finalReasoning string
	sessionID    string
	finishReason string
	durationMS   int64
	inputTokens  int
	outputTokens int
	totalTokens  int
	errMessage   string
}

func runConsoleCloudAgentChat(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
	publicName := firstNonEmpty(strings.TrimSpace(request.PublicName), strings.TrimSpace(account.ModelPublicName))
	threadID := consoleCloudAgentCodexThreadID(request.PreviousResponseID)
	parser := &consoleCloudAgentStreamParser{}
	handlers := consoleCloudAgentStreamHandlers{
		OnDelta:     request.OnDelta,
		OnReasoning: request.OnReasoning,
	}
	output, err := workerops.RunCloudAgentCodex(ctx, worker, key, account, cloudSession, workerops.CloudAgentCodexOptions{
		JobID:            strings.TrimSpace(request.SessionID),
		ThreadID:         threadID,
		Prompt:           consoleCloudAgentCurrentPrompt(request.UserInput, request.Attachments),
		InitialPrompt:    consoleCloudAgentInitialPrompt(request.History, request.UserInput, request.Attachments),
		Model:            publicName,
		WorkingDirectory: consoleCloudAgentWorkingDirectory(account, cloudSession, request.ShellActiveTerminalID, request.ShellCurrentWorkingDirectory),
		Stream:           request.Stream,
	}, func(chunk []byte) error {
		if !request.Stream {
			return nil
		}
		return parser.consumeChunk(chunk, handlers)
	})
	if request.Stream {
		if consumeErr := parser.finish(handlers); consumeErr != nil && err == nil {
			err = consumeErr
		}
	} else {
		if consumeErr := parser.consumeJSONResult(output); consumeErr != nil && err == nil {
			err = consumeErr
		}
	}
	result := consoleChatExecution{
		Result: consoleChatResultView{
			PublicName:    publicName,
			ProviderID:    "cloud-agent:" + strings.TrimSpace(worker.ID),
			ProviderName:  "Codex · " + strings.TrimSpace(worker.ID),
			UpstreamModel: firstNonEmpty(publicName, strings.TrimSpace(account.ModelPublicName)),
			Output:        parser.resultText(),
			Reasoning:     parser.reasoningText(),
			ResponseID:    firstNonEmpty(parser.sessionID, threadID),
			FinishReason:  parser.finishReason,
			DurationMS:    parser.durationMS,
			TotalTokens:   parser.totalTokens,
		},
		Usage: domain.UsageRecord{
			Currency:     domain.DefaultBillingCurrency,
			InputTokens:  parser.inputTokens,
			OutputTokens: parser.outputTokens,
			TotalTokens:  parser.totalTokens,
			Stream:       request.Stream,
		},
	}
	if result.Result.Output == "" {
		result.Result.Output = consoleChatEmptyOutput
	}
	if errMessage := strings.TrimSpace(parser.errMessage); errMessage != "" {
		if err == nil {
			err = errors.New(errMessage)
		} else {
			err = errors.New(errMessage + ": " + err.Error())
		}
	}
	if err != nil {
		if result.StatusCode == 0 {
			result.StatusCode = 500
		}
		result.Usage.StatusCode = result.StatusCode
		return result, err
	}
	result.StatusCode = 200
	result.Usage.StatusCode = 200
	return result, nil
}

func consoleCloudAgentCodexThreadID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, err := uuid.Parse(value); err != nil {
		return ""
	}
	return value
}

func consoleCloudAgentWorkingDirectory(account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, activeTerminalID string, requestedWorkingDirectory string) string {
	if workingDirectory := normalizeConsoleChatShellWorkingDirectory(requestedWorkingDirectory); workingDirectory != "" {
		return workingDirectory
	}
	workspacePath := firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	state := consoleChatShellStateFromSession(cloudSession, cloudSession.ChatSessionID, account.WorkerID, account.ContainerName, workspacePath)
	targetTerminalID := normalizeConsoleChatOptionalShellTerminalID(activeTerminalID)
	if targetTerminalID == "" {
		targetTerminalID = state.ActiveTerminalID
	}
	for _, snapshot := range state.Instances {
		if snapshot.TerminalID != targetTerminalID {
			continue
		}
		if workingDirectory := normalizeConsoleChatShellWorkingDirectory(firstNonEmpty(snapshot.CurrentWorkingDirectory, snapshot.Meta.CurrentWorkingDirectory)); workingDirectory != "" {
			return workingDirectory
		}
		break
	}
	return firstNonEmpty(normalizeConsoleChatShellWorkingDirectory(workspacePath), domain.DefaultCloudAgentWorkspacePath)
}

func consoleCloudAgentCurrentPrompt(userInput string, attachments []consoleChatAttachmentView) string {
	sections := consoleCloudAgentInteractivePromptSections()
	if latest := consoleCloudAgentMessageBody(userInput, attachments); latest != "" {
		sections = append(sections, "Latest user message:\n"+latest)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func consoleCloudAgentInitialPrompt(history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView) string {
	sections := consoleCloudAgentInteractivePromptSections()
	sections = append(sections, "Treat the following transcript as the full prior conversation for this AIYolo chat session.")
	if transcript := consoleCloudAgentTranscript(history); transcript != "" {
		sections = append(sections, "Previous conversation:\n"+transcript)
	}
	if latest := consoleCloudAgentMessageBody(userInput, attachments); latest != "" {
		sections = append(sections, "Latest user message:\n"+latest)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func consoleCloudAgentInteractivePromptSections() []string {
	return []string{
		"Continue this AIYolo chat session inside Codex as an interactive collaboration, not a one-shot completion.",
		"Work on the latest user message in the current workspace when you have enough information.",
		"If the request is ambiguous, missing a decision, requires credentials, or could reasonably branch into different implementations, ask a concise clarification question and stop. The user will answer in the AIYolo chat input, and the next turn will resume this same Codex session.",
		"Do not invent missing requirements or mark the task complete while you are waiting for the user's answer.",
	}
}

func consoleCloudAgentTranscript(messages []consoleChatMessageView) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		entry := consoleCloudAgentTranscriptEntry(message)
		if entry == "" {
			continue
		}
		parts = append(parts, entry)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func consoleCloudAgentTranscriptEntry(message consoleChatMessageView) string {
	body := consoleCloudAgentMessageBody(message.Content, message.Attachments)
	if reasoning := strings.TrimSpace(message.Reasoning); reasoning != "" {
		if body != "" {
			body += "\n\nReasoning:\n" + reasoning
		} else {
			body = "Reasoning:\n" + reasoning
		}
	}
	if body == "" {
		return ""
	}
	role := normalizeConsoleChatRole(message.Role)
	switch role {
	case "assistant":
		return "Assistant:\n" + body
	case "system":
		return "System:\n" + body
	default:
		return "User:\n" + body
	}
}

func consoleCloudAgentMessageBody(content string, attachments []consoleChatAttachmentView) string {
	content = strings.TrimSpace(content)
	references := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if reference := strings.TrimSpace(consoleChatAttachmentReferenceText(attachment)); reference != "" {
			references = append(references, reference)
		}
	}
	switch {
	case content == "" && len(references) == 0:
		return ""
	case content == "":
		return strings.Join(references, "\n\n")
	case len(references) == 0:
		return content
	default:
		return content + "\n\n" + strings.Join(references, "\n\n")
	}
}

func (parser *consoleCloudAgentStreamParser) consumeChunk(chunk []byte, handlers consoleCloudAgentStreamHandlers) error {
	if parser == nil || len(chunk) == 0 {
		return nil
	}
	parser.pending.Write(chunk)
	payload := parser.pending.String()
	lines := strings.Split(payload, "\n")
	parser.pending.Reset()
	if len(lines) == 0 {
		return nil
	}
	for _, line := range lines[:len(lines)-1] {
		if err := parser.consumeLine(line, handlers); err != nil {
			return err
		}
	}
	parser.pending.WriteString(lines[len(lines)-1])
	return nil
}

func (parser *consoleCloudAgentStreamParser) finish(handlers consoleCloudAgentStreamHandlers) error {
	if parser == nil {
		return nil
	}
	if strings.TrimSpace(parser.pending.String()) == "" {
		return nil
	}
	line := parser.pending.String()
	parser.pending.Reset()
	return parser.consumeLine(line, handlers)
}

func (parser *consoleCloudAgentStreamParser) consumeJSONResult(raw string) error {
	if parser == nil {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, line := range strings.Split(raw, "\n") {
		if err := parser.consumeLine(line, consoleCloudAgentStreamHandlers{}); err != nil {
			return err
		}
	}
	return nil
}

func (parser *consoleCloudAgentStreamParser) consumeLine(raw string, handlers consoleCloudAgentStreamHandlers) error {
	if parser == nil {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	parser.captureSessionID(payload)
	switch strings.TrimSpace(stringValue(payload["type"])) {
	case "thread.started":
		parser.captureSessionID(payload)
	case "item.delta", "item.updated", "message.delta", "agent_message.delta", "agent_message_delta":
		if text := codexReasoningDeltaText(payload); text != "" {
			return parser.appendReasoning(text, handlers)
		}
		if text := codexAssistantDeltaText(payload); text != "" {
			parser.output.WriteString(text)
			if handlers.OnDelta != nil {
				return handlers.OnDelta(text)
			}
		}
	case "item.completed", "item.started":
		item, _ := payload["item"].(map[string]any)
		if isCodexReasoningItem(item) {
			text := codexReasoningItemText(item)
			if strings.TrimSpace(stringValue(payload["type"])) == "item.completed" && text != "" {
				parser.finalReasoning = text
			}
			return parser.appendReasoning(text, handlers)
		}
		if text := codexAssistantItemText(item); text != "" {
			if strings.TrimSpace(stringValue(payload["type"])) == "item.completed" {
				parser.finalOutput = text
				if handlers.OnDelta != nil && parser.output.Len() == 0 {
					parser.output.WriteString(text)
					return handlers.OnDelta(text)
				}
			}
		}
	case "turn.completed":
		parser.applyUsage(payload["usage"])
		parser.finishReason = firstNonEmpty(strings.TrimSpace(stringValue(payload["finish_reason"])), parser.finishReason, "stop")
	case "turn.failed":
		parser.errMessage = firstNonEmpty(codexErrorMessage(payload["error"]), strings.TrimSpace(stringValue(payload["message"])), "codex execution failed")
	case "error":
		parser.errMessage = firstNonEmpty(codexErrorMessage(payload["error"]), strings.TrimSpace(stringValue(payload["message"])), "codex execution failed")
	case "stream_event":
		event, _ := payload["event"].(map[string]any)
		if strings.TrimSpace(stringValue(event["type"])) == "content_block_delta" {
			delta, _ := event["delta"].(map[string]any)
			deltaType := strings.TrimSpace(stringValue(delta["type"]))
			if deltaType == "thinking_delta" || deltaType == "reasoning_delta" {
				text := firstNonEmpty(stringValue(delta["thinking"]), stringValue(delta["reasoning"]), stringValue(delta["text"]))
				if text != "" {
					return parser.appendReasoning(text, handlers)
				}
			}
			if deltaType == "text_delta" {
				text := stringValue(delta["text"])
				if text != "" {
					parser.output.WriteString(text)
					if handlers.OnDelta != nil {
						return handlers.OnDelta(text)
					}
				}
			}
		}
		if strings.TrimSpace(stringValue(event["type"])) == "message_delta" {
			delta, _ := event["delta"].(map[string]any)
			parser.finishReason = firstNonEmpty(strings.TrimSpace(stringValue(delta["stop_reason"])), parser.finishReason)
		}
	case "result":
		if text := strings.TrimSpace(stringValue(payload["result"])); text != "" {
			parser.finalOutput = text
		}
		if subtype := strings.TrimSpace(stringValue(payload["subtype"])); subtype != "" && parser.finishReason == "" {
			parser.finishReason = subtype
		}
		if duration := int64Value(payload["duration_ms"]); duration > 0 {
			parser.durationMS = duration
		}
		if isTrueValue(payload["is_error"]) {
			parser.errMessage = firstNonEmpty(strings.TrimSpace(stringValue(payload["error"])), strings.TrimSpace(stringValue(payload["result"])), "codex execution failed")
		}
	case "system":
		if strings.TrimSpace(stringValue(payload["subtype"])) == "init" {
			parser.sessionID = firstNonEmpty(strings.TrimSpace(stringValue(payload["session_id"])), parser.sessionID)
		}
	}
	return nil
}

func (parser *consoleCloudAgentStreamParser) captureSessionID(payload map[string]any) {
	if parser == nil || parser.sessionID != "" || payload == nil {
		return
	}
	parser.sessionID = firstNonEmpty(
		strings.TrimSpace(stringValue(payload["thread_id"])),
		strings.TrimSpace(stringValue(payload["session_id"])),
		strings.TrimSpace(stringValue(payload["conversation_id"])),
	)
	if parser.sessionID != "" {
		return
	}
	thread, _ := payload["thread"].(map[string]any)
	parser.sessionID = firstNonEmpty(strings.TrimSpace(stringValue(thread["id"])), strings.TrimSpace(stringValue(thread["thread_id"])))
}

func (parser *consoleCloudAgentStreamParser) applyUsage(value any) {
	if parser == nil {
		return
	}
	usage, _ := value.(map[string]any)
	if len(usage) == 0 {
		return
	}
	inputTokens := intValue(firstNonNil(usage["input_tokens"], usage["prompt_tokens"]))
	outputTokens := intValue(firstNonNil(usage["output_tokens"], usage["completion_tokens"]))
	totalTokens := intValue(usage["total_tokens"])
	if totalTokens == 0 && inputTokens+outputTokens > 0 {
		totalTokens = inputTokens + outputTokens
	}
	if inputTokens > 0 {
		parser.inputTokens = inputTokens
	}
	if outputTokens > 0 {
		parser.outputTokens = outputTokens
	}
	if totalTokens > 0 {
		parser.totalTokens = totalTokens
	}
}

func codexDeltaText(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	for _, key := range []string{"delta", "text", "content"} {
		if text := codexTextFromValue(payload[key]); text != "" {
			return text
		}
	}
	item, _ := payload["item"].(map[string]any)
	return codexAssistantItemText(item)
}

func codexAssistantItemText(item map[string]any) string {
	if len(item) == 0 {
		return ""
	}
	itemType := strings.ToLower(strings.TrimSpace(stringValue(item["type"])))
	role := strings.ToLower(strings.TrimSpace(stringValue(item["role"])))
	if itemType != "agent_message" && itemType != "message" && role != "assistant" {
		return ""
	}
	for _, key := range []string{"text", "content", "message"} {
		if text := codexTextFromValue(item[key]); text != "" {
			return text
		}
	}
	return ""
}

func codexTextFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := codexTextFromValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]any:
		blockType := strings.ToLower(strings.TrimSpace(stringValue(typed["type"])))
		if blockType == "tool_call" || blockType == "function_call" || blockType == "reasoning" {
			return ""
		}
		for _, key := range []string{"text", "content", "value"} {
			if text := codexTextFromValue(typed[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func isCodexReasoningItem(item map[string]any) bool {
	if len(item) == 0 {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(stringValue(item["type"])))
	return itemType == "reasoning" || itemType == "reasoning_item"
}

func codexReasoningItemText(item map[string]any) string {
	if len(item) == 0 {
		return ""
	}
	for _, key := range []string{"text", "summary", "content", "reasoning"} {
		if text := codexTextFromValue(item[key]); text != "" {
			return text
		}
	}
	return ""
}

func codexReasoningDeltaText(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if item, ok := payload["item"].(map[string]any); ok && isCodexReasoningItem(item) {
		if text := codexReasoningItemText(item); text != "" {
			return text
		}
	}
	itemType := strings.ToLower(strings.TrimSpace(stringValue(payload["item_type"])))
	if itemType == "reasoning" || itemType == "reasoning_item" {
		for _, key := range []string{"delta", "text", "reasoning", "thinking"} {
			if text := codexTextFromValue(payload[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func codexAssistantDeltaText(payload map[string]any) string {
	return codexDeltaText(payload)
}

func codexErrorMessage(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return firstNonEmpty(strings.TrimSpace(stringValue(typed["message"])), strings.TrimSpace(stringValue(typed["code"])), strings.TrimSpace(stringValue(typed["type"])))
	default:
		return ""
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (parser *consoleCloudAgentStreamParser) appendReasoning(text string, handlers consoleCloudAgentStreamHandlers) error {
	if parser == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	parser.reasoning.WriteString(text)
	if handlers.OnReasoning != nil {
		return handlers.OnReasoning(text)
	}
	return nil
}

func (parser *consoleCloudAgentStreamParser) resultText() string {
	if parser == nil {
		return ""
	}
	if text := strings.TrimSpace(parser.finalOutput); text != "" {
		return text
	}
	return strings.TrimSpace(parser.output.String())
}

func (parser *consoleCloudAgentStreamParser) reasoningText() string {
	if parser == nil {
		return ""
	}
	if text := strings.TrimSpace(parser.finalReasoning); text != "" {
		return text
	}
	return strings.TrimSpace(parser.reasoning.String())
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func intValue(value any) int {
	return int(int64Value(value))
}

func isTrueValue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}
