package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

type upstreamResponseMode string

const (
	upstreamResponseModeDirect                upstreamResponseMode = "direct"
	upstreamResponseModeResponsesChatFallback upstreamResponseMode = "responses_chat_fallback"
)

func shouldFallbackResponsesToChat(endpoint string, statusCode int) bool {
	if endpoint != "/v1/responses" {
		return false
	}
	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	default:
		return false
	}
}

func (handler *Handler) forwardResponsesViaChatCompletions(ctx context.Context, clientRequest *http.Request, client *http.Client, provider domain.Provider, body []byte) (*http.Response, error) {
	chatBody, err := responsesRequestToChatCompletions(body, provider)
	if err != nil {
		return nil, err
	}
	upstream, err := handler.buildUpstreamRequest(ctx, clientRequest, "/v1/chat/completions", provider, domain.ProtocolOpenAI, chatBody)
	if err != nil {
		return nil, err
	}
	return client.Do(upstream)
}

func responsesRequestToChatCompletions(body []byte, provider domain.Provider) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	chat := make(map[string]any, len(payload)+4)
	for _, key := range []string{"model", "stream", "temperature", "top_p", "stop", "user", "metadata", "parallel_tool_calls", "service_tier"} {
		if value, ok := payload[key]; ok {
			chat[key] = value
		}
	}
	if value, ok := payload["max_tokens"]; ok {
		chat["max_tokens"] = value
	} else if value, ok := payload["max_output_tokens"]; ok {
		chat["max_tokens"] = value
	}
	if messages := responsesInputToChatMessages(payload["instructions"], payload["input"]); len(messages) > 0 {
		chat["messages"] = messages
	}
	if tools := responsesToolsToChatTools(payload["tools"]); len(tools) > 0 {
		chat["tools"] = tools
	}
	if value, ok := payload["tool_choice"]; ok {
		chat["tool_choice"] = value
	}
	if responseFormat := responsesTextToChatResponseFormat(payload["text"]); responseFormat != nil {
		chat["response_format"] = responseFormat
	} else if value, ok := payload["response_format"]; ok {
		chat["response_format"] = value
	}
	if domain.IsDeepSeekProvider(provider) {
		if effort := responseReasoningEffort(payload["reasoning"]); effort != "" {
			chat["reasoning_effort"] = effort
			chat["thinking"] = map[string]any{"type": "enabled"}
		}
	}
	return json.Marshal(chat)
}

func responsesInputToChatMessages(instructions any, input any) []map[string]any {
	messages := make([]map[string]any, 0, 8)
	if text, ok := instructions.(string); ok && strings.TrimSpace(text) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": text})
	}
	messages = append(messages, responseInputValueToChatMessages(input)...)
	if len(messages) == 0 {
		messages = append(messages, map[string]any{"role": "user", "content": ""})
	}
	return messages
}

func responseInputValueToChatMessages(value any) []map[string]any {
	switch typed := value.(type) {
	case string:
		return []map[string]any{{"role": "user", "content": typed}}
	case []any:
		messages := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			messages = append(messages, responseInputValueToChatMessages(item)...)
		}
		return messages
	case map[string]any:
		if message := responseItemToChatMessage(typed); message != nil {
			return []map[string]any{message}
		}
	}
	if value != nil {
		return []map[string]any{{"role": "user", "content": stringifyChatContent(value)}}
	}
	return nil
}

func responseItemToChatMessage(item map[string]any) map[string]any {
	switch strings.TrimSpace(fmt.Sprint(item["type"])) {
	case "message", "":
		role := firstNonEmptyString(item["role"], "user")
		return map[string]any{"role": role, "content": responseContentToChatContent(item["content"])}
	case "function_call_output", "custom_tool_call_output", "mcp_tool_call_output":
		callID := strings.TrimSpace(fmt.Sprint(item["call_id"]))
		return map[string]any{"role": "tool", "tool_call_id": callID, "content": stringifyChatContent(item["output"])}
	case "function_call":
		callID := firstNonEmptyString(item["call_id"], item["id"])
		name := firstNonEmptyString(item["name"])
		arguments := firstNonEmptyString(item["arguments"])
		return map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}
	default:
		return nil
	}
}

func responseContentToChatContent(value any) any {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]any, 0, len(typed))
		textParts := make([]string, 0, len(typed))
		for _, rawPart := range typed {
			part, ok := rawPart.(map[string]any)
			if !ok {
				textParts = append(textParts, stringifyChatContent(rawPart))
				continue
			}
			switch strings.TrimSpace(fmt.Sprint(part["type"])) {
			case "input_text", "output_text", "text":
				text := firstNonEmptyString(part["text"])
				textParts = append(textParts, text)
				parts = append(parts, map[string]any{"type": "text", "text": text})
			case "input_image", "image_url":
				imageURL := firstNonEmptyString(part["image_url"], part["url"])
				if imageURL != "" {
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
				}
			default:
				textParts = append(textParts, stringifyChatContent(part))
			}
		}
		if len(parts) > 0 {
			return parts
		}
		return strings.Join(textParts, "\n")
	default:
		return stringifyChatContent(value)
	}
}

func responsesToolsToChatTools(value any) []any {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	tools := make([]any, 0, len(items))
	for _, rawTool := range items {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := tool["function"]; ok {
			tools = append(tools, tool)
			continue
		}
		if strings.TrimSpace(fmt.Sprint(tool["type"])) != "function" {
			continue
		}
		function := map[string]any{}
		for _, key := range []string{"name", "description", "parameters", "strict"} {
			if value, ok := tool[key]; ok {
				function[key] = value
			}
		}
		if function["name"] == nil {
			continue
		}
		tools = append(tools, map[string]any{"type": "function", "function": function})
	}
	return tools
}

func responsesTextToChatResponseFormat(value any) any {
	payload, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	format, ok := payload["format"].(map[string]any)
	if !ok {
		return nil
	}
	if strings.TrimSpace(fmt.Sprint(format["type"])) != "json_schema" || format["schema"] == nil {
		return nil
	}
	jsonSchema := map[string]any{"name": firstNonEmptyString(format["name"], "codex_output_schema"), "schema": format["schema"]}
	if strict, ok := format["strict"].(bool); ok {
		jsonSchema["strict"] = strict
	}
	return map[string]any{"type": "json_schema", "json_schema": jsonSchema}
}

func responseReasoningEffort(value any) string {
	payload, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	return firstNonEmptyString(payload["effort"])
}

func chatCompletionResponseToResponses(body []byte, statusCode int) ([]byte, domain.UsageRecord, error) {
	usage := parseUsageFromJSON(body, domain.ProtocolOpenAI)
	if statusCode >= http.StatusBadRequest {
		return body, usage, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, usage, err
	}
	choice := firstMapFromSlice(payload["choices"])
	message, _ := choice["message"].(map[string]any)
	content := chatMessageContentText(message["content"])
	responseID := firstNonEmptyString(payload["id"], newID("resp"))
	messageID := newID("msg")
	toolCalls := chatToolCalls(message["tool_calls"])
	output := make([]any, 0, 2)
	if content != "" || len(toolCalls) == 0 {
		output = append(output, responseMessageItem(messageID, "completed", content))
	}
	for _, toolCall := range toolCalls {
		output = append(output, responseFunctionCallItem(toolCall))
	}
	response := map[string]any{
		"id":                  responseID,
		"object":              "response",
		"created_at":          createdAtValue(payload["created"]),
		"status":              "completed",
		"model":               firstNonEmptyString(payload["model"]),
		"output":              output,
		"output_text":         content,
		"parallel_tool_calls": true,
		"usage":               responsesUsagePayload(usage),
	}
	encoded, err := json.Marshal(response)
	return encoded, usage, err
}

func copyChatCompletionsStreamAsResponses(w http.ResponseWriter, response *http.Response, metadata *routerMetadata) (domain.UsageRecord, error) {
	copyResponseHeaders(w.Header(), response.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(response.StatusCode)
	if response.StatusCode >= http.StatusBadRequest {
		return copySSE(w, response.Body, domain.ProtocolOpenAI)
	}
	return writeChatCompletionsSSEAsResponses(w, response.Body, metadata)
}

type responseStreamState struct {
	responseID     string
	messageID      string
	messageStarted bool
	contentStarted bool
	content        strings.Builder
	toolCalls      map[int]*chatToolCallState
	usage          domain.UsageRecord
}

type chatToolCallState struct {
	CallID    string
	Name      string
	Arguments strings.Builder
}

func writeChatCompletionsSSEAsResponses(w http.ResponseWriter, body io.Reader, metadata *routerMetadata) (domain.UsageRecord, error) {
	flusher, _ := w.(http.Flusher)
	state := &responseStreamState{responseID: newID("resp"), messageID: newID("msg"), toolCalls: map[int]*chatToolCallState{}}
	if err := writeResponsesSSEEvent(w, "response.created", map[string]any{"type": "response.created", "response": baseStreamingResponse(state, "in_progress", nil)}, metadata); err != nil {
		return state.usage, err
	}
	if flusher != nil {
		flusher.Flush()
	}
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(string(line))
			if strings.HasPrefix(trimmed, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if data == "[DONE]" {
					if err := finishResponsesStream(w, state, metadata); err != nil {
						return state.usage, err
					}
					if flusher != nil {
						flusher.Flush()
					}
					return state.usage, nil
				}
				if data != "" {
					if err := consumeChatCompletionSSEChunk(w, state, []byte(data), metadata); err != nil {
						return state.usage, err
					}
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if finishErr := finishResponsesStream(w, state, metadata); finishErr != nil {
					return state.usage, finishErr
				}
				return state.usage, nil
			}
			return state.usage, err
		}
	}
}

func consumeChatCompletionSSEChunk(w http.ResponseWriter, state *responseStreamState, data []byte, metadata *routerMetadata) error {
	mergeUsage(&state.usage, parseUsageFromJSON(data, domain.ProtocolOpenAI))
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	for _, rawChoice := range anySlice(payload["choices"]) {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		delta, _ := choice["delta"].(map[string]any)
		if content := chatMessageContentText(delta["content"]); content != "" {
			if err := ensureResponseMessageStarted(w, state, metadata); err != nil {
				return err
			}
			state.content.WriteString(content)
			if err := writeResponsesSSEEvent(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": state.messageID, "output_index": 0, "content_index": 0, "delta": content}, metadata); err != nil {
				return err
			}
		}
		for _, rawToolCall := range anySlice(delta["tool_calls"]) {
			consumeChatToolCallDelta(state, rawToolCall)
		}
	}
	return nil
}

func ensureResponseMessageStarted(w http.ResponseWriter, state *responseStreamState, metadata *routerMetadata) error {
	if !state.messageStarted {
		state.messageStarted = true
		if err := writeResponsesSSEEvent(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": responseMessageItem(state.messageID, "in_progress", "")}, metadata); err != nil {
			return err
		}
	}
	if !state.contentStarted {
		state.contentStarted = true
		return writeResponsesSSEEvent(w, "response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": state.messageID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}, metadata)
	}
	return nil
}

func consumeChatToolCallDelta(state *responseStreamState, rawToolCall any) {
	toolCall, ok := rawToolCall.(map[string]any)
	if !ok {
		return
	}
	index := intNumber(toolCall["index"])
	item := state.toolCalls[index]
	if item == nil {
		item = &chatToolCallState{CallID: firstNonEmptyString(toolCall["id"], newID("call"))}
		state.toolCalls[index] = item
	}
	if id := firstNonEmptyString(toolCall["id"]); id != "" {
		item.CallID = id
	}
	function, _ := toolCall["function"].(map[string]any)
	if name := firstNonEmptyString(function["name"]); name != "" {
		item.Name = name
	}
	if arguments := firstNonEmptyString(function["arguments"]); arguments != "" {
		item.Arguments.WriteString(arguments)
	}
}

func finishResponsesStream(w http.ResponseWriter, state *responseStreamState, metadata *routerMetadata) error {
	output := make([]any, 0, 1+len(state.toolCalls))
	if state.messageStarted || state.content.Len() > 0 {
		content := state.content.String()
		if state.contentStarted {
			if err := writeResponsesSSEEvent(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": state.messageID, "output_index": 0, "content_index": 0, "text": content}, metadata); err != nil {
				return err
			}
			if err := writeResponsesSSEEvent(w, "response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": state.messageID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": content, "annotations": []any{}}}, metadata); err != nil {
				return err
			}
		}
		messageItem := responseMessageItem(state.messageID, "completed", content)
		if err := writeResponsesSSEEvent(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": messageItem}, metadata); err != nil {
			return err
		}
		output = append(output, messageItem)
	}
	for _, item := range sortedChatToolCallStates(state.toolCalls) {
		functionItem := responseFunctionCallItem(map[string]any{"id": item.CallID, "type": "function", "function": map[string]any{"name": item.Name, "arguments": item.Arguments.String()}})
		if err := writeResponsesSSEEvent(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": len(output), "item": functionItem}, metadata); err != nil {
			return err
		}
		output = append(output, functionItem)
	}
	response := baseStreamingResponse(state, "completed", output)
	response["usage"] = responsesUsagePayload(state.usage)
	if err := writeResponsesSSEEvent(w, "response.completed", map[string]any{"type": "response.completed", "response": response}, metadata); err != nil {
		return err
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	return err
}

func writeResponsesSSEEvent(w http.ResponseWriter, event string, payload map[string]any, metadata *routerMetadata) error {
	if metadata != nil {
		payload["aiyolo_metadata"] = metadata
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
	return err
}

func baseStreamingResponse(state *responseStreamState, status string, output []any) map[string]any {
	if output == nil {
		output = []any{}
	}
	return map[string]any{"id": state.responseID, "object": "response", "created_at": time.Now().Unix(), "status": status, "output": output, "parallel_tool_calls": true}
}

func responseMessageItem(id string, status string, content string) map[string]any {
	return map[string]any{"id": id, "type": "message", "status": status, "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": content, "annotations": []any{}}}}
}

func responseFunctionCallItem(toolCall map[string]any) map[string]any {
	function, _ := toolCall["function"].(map[string]any)
	callID := firstNonEmptyString(toolCall["id"], newID("call"))
	return map[string]any{"id": firstNonEmptyString(toolCall["id"], newID("fc")), "type": "function_call", "call_id": callID, "name": firstNonEmptyString(function["name"]), "arguments": firstNonEmptyString(function["arguments"], "{}"), "status": "completed"}
}

func responsesUsagePayload(usage domain.UsageRecord) map[string]any {
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	return map[string]any{"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens}
}

func chatToolCalls(value any) []map[string]any {
	items := anySlice(value)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if payload, ok := item.(map[string]any); ok {
			result = append(result, payload)
		}
	}
	return result
}

func sortedChatToolCallStates(values map[int]*chatToolCallState) []*chatToolCallState {
	result := make([]*chatToolCallState, 0, len(values))
	for index := 0; index < len(values)+8; index++ {
		if item := values[index]; item != nil {
			result = append(result, item)
		}
	}
	if len(result) == len(values) {
		return result
	}
	for _, item := range values {
		found := false
		for _, existing := range result {
			if existing == item {
				found = true
				break
			}
		}
		if !found {
			result = append(result, item)
		}
	}
	return result
}

func firstMapFromSlice(value any) map[string]any {
	for _, item := range anySlice(value) {
		if payload, ok := item.(map[string]any); ok {
			return payload
		}
	}
	return nil
}

func anySlice(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

func chatMessageContentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, rawPart := range typed {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(fmt.Sprint(part["type"])) == "text" || strings.TrimSpace(fmt.Sprint(part["type"])) == "output_text" {
				parts = append(parts, firstNonEmptyString(part["text"]))
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func stringifyChatContent(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func firstNonEmptyString(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		default:
			if value != nil {
				text := strings.TrimSpace(fmt.Sprint(value))
				if text != "" && text != "<nil>" {
					return text
				}
			}
		}
	}
	return ""
}

func createdAtValue(value any) int64 {
	if created := intNumber(value); created > 0 {
		return int64(created)
	}
	return time.Now().Unix()
}
