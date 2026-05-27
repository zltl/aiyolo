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
	PublicName  string
	History     []consoleChatMessageView
	UserInput   string
	Attachments []consoleChatAttachmentView
	Stream      bool
	OnDelta     func(string) error
}

type consoleCloudAgentStreamParser struct {
	pending      strings.Builder
	output       strings.Builder
	finalOutput  string
	sessionID    string
	finishReason string
	durationMS   int64
	errMessage   string
}

func runConsoleCloudAgentChat(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
	publicName := firstNonEmpty(strings.TrimSpace(request.PublicName), strings.TrimSpace(account.ModelPublicName))
	sessionID := consoleCloudAgentClaudeSessionID(account.UserID, cloudSession.ChatSessionID)
	parser := &consoleCloudAgentStreamParser{}
	output, err := workerops.RunCloudAgentClaudeCode(ctx, worker, key, account, cloudSession, workerops.CloudAgentClaudeCodeOptions{
		SessionID:     sessionID,
		Prompt:        consoleCloudAgentCurrentPrompt(request.UserInput, request.Attachments),
		InitialPrompt: consoleCloudAgentInitialPrompt(request.History, request.UserInput, request.Attachments),
		Model:         publicName,
		Stream:        request.Stream,
	}, func(chunk []byte) error {
		if !request.Stream {
			return nil
		}
		return parser.consumeChunk(chunk, request.OnDelta)
	})
	if request.Stream {
		if consumeErr := parser.finish(request.OnDelta); consumeErr != nil && err == nil {
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
			ProviderName:  "Claude Code · " + strings.TrimSpace(worker.ID),
			UpstreamModel: firstNonEmpty(publicName, strings.TrimSpace(account.ModelPublicName)),
			Output:        parser.resultText(),
			ResponseID:    firstNonEmpty(parser.sessionID, sessionID),
			FinishReason:  parser.finishReason,
			DurationMS:    parser.durationMS,
		},
		Usage: domain.UsageRecord{
			Currency: "USD",
			Stream:   request.Stream,
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

func consoleCloudAgentClaudeSessionID(userID string, chatSessionID string) string {
	token := strings.TrimSpace(userID) + ":" + strings.TrimSpace(chatSessionID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("aiyolo-console-cloud-agent:"+token)).String()
}

func consoleCloudAgentCurrentPrompt(userInput string, attachments []consoleChatAttachmentView) string {
	return consoleCloudAgentMessageBody(userInput, attachments)
}

func consoleCloudAgentInitialPrompt(history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView) string {
	sections := []string{
		"Continue this AIYolo chat session inside Claude Code.",
		"Treat the following transcript as the full prior conversation, then answer the latest user message.",
	}
	if transcript := consoleCloudAgentTranscript(history); transcript != "" {
		sections = append(sections, "Previous conversation:\n"+transcript)
	}
	if latest := consoleCloudAgentMessageBody(userInput, attachments); latest != "" {
		sections = append(sections, "Latest user message:\n"+latest)
	}
	sections = append(sections, "Reply to the latest user message and use the current workspace when it helps solve the task.")
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
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

func (parser *consoleCloudAgentStreamParser) consumeChunk(chunk []byte, onDelta func(string) error) error {
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
		if err := parser.consumeLine(line, onDelta); err != nil {
			return err
		}
	}
	parser.pending.WriteString(lines[len(lines)-1])
	return nil
}

func (parser *consoleCloudAgentStreamParser) finish(onDelta func(string) error) error {
	if parser == nil {
		return nil
	}
	if strings.TrimSpace(parser.pending.String()) == "" {
		return nil
	}
	line := parser.pending.String()
	parser.pending.Reset()
	return parser.consumeLine(line, onDelta)
}

func (parser *consoleCloudAgentStreamParser) consumeJSONResult(raw string) error {
	if parser == nil {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return parser.consumeLine(raw, nil)
}

func (parser *consoleCloudAgentStreamParser) consumeLine(raw string, onDelta func(string) error) error {
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
	if parser.sessionID == "" {
		parser.sessionID = strings.TrimSpace(stringValue(payload["session_id"]))
	}
	switch strings.TrimSpace(stringValue(payload["type"])) {
	case "stream_event":
		event, _ := payload["event"].(map[string]any)
		if strings.TrimSpace(stringValue(event["type"])) == "content_block_delta" {
			delta, _ := event["delta"].(map[string]any)
			if strings.TrimSpace(stringValue(delta["type"])) == "text_delta" {
				text := stringValue(delta["text"])
				if text != "" {
					parser.output.WriteString(text)
					if onDelta != nil {
						return onDelta(text)
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
			parser.errMessage = firstNonEmpty(strings.TrimSpace(stringValue(payload["error"])), strings.TrimSpace(stringValue(payload["result"])), "claude code execution failed")
		}
	case "system":
		if strings.TrimSpace(stringValue(payload["subtype"])) == "init" {
			parser.sessionID = firstNonEmpty(strings.TrimSpace(stringValue(payload["session_id"])), parser.sessionID)
		}
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

func isTrueValue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}
