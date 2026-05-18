package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestBuildConsoleChatRequestBodyIncludesOpenAIMultimodalParts(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "ops-chat", UpstreamModel: "openai/gpt-5.5"}, "system", nil, "review this screenshot", []consoleChatAttachmentView{{ObjectKey: "chat/user/upload.png", URL: "https://files.example.com/chat/user/upload.png", MediaType: "image/png", Name: "upload.png"}, {ObjectKey: "chat/user/spec.pdf", URL: "https://files.example.com/chat/user/spec.pdf", MediaType: "application/pdf", Name: "spec.pdf"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	userMessage, ok := messages[1].(map[string]any)
	if !ok {
		t.Fatalf("user message = %#v", messages[1])
	}
	content, ok := userMessage["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content = %#v", userMessage["content"])
	}
	if first, _ := content[0].(map[string]any); first["type"] != "text" || first["text"] != "review this screenshot" {
		t.Fatalf("unexpected first content part: %#v", content[0])
	}
	if imagePart, _ := content[1].(map[string]any); imagePart["type"] != "image_url" {
		t.Fatalf("unexpected image part: %#v", content[1])
	}
	if filePart, _ := content[2].(map[string]any); filePart["type"] != "text" {
		t.Fatalf("unexpected file part: %#v", content[2])
	}
}

func TestBuildConsoleChatRequestBodyIncludesAnthropicDocumentParts(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolAnthropic, domain.ModelRoute{PublicName: "claude-sonnet", UpstreamModel: "claude-sonnet-4-5"}, "", nil, "summarize these inputs", []consoleChatAttachmentView{{ObjectKey: "chat/user/diagram.png", URL: "https://files.example.com/chat/user/diagram.png", MediaType: "image/png", Name: "diagram.png"}, {ObjectKey: "chat/user/notes.pdf", URL: "https://files.example.com/chat/user/notes.pdf", MediaType: "application/pdf", Name: "notes.pdf"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	userMessage, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("user message = %#v", messages[0])
	}
	content, ok := userMessage["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content = %#v", userMessage["content"])
	}
	if imagePart, _ := content[1].(map[string]any); imagePart["type"] != "image" {
		t.Fatalf("unexpected image part: %#v", content[1])
	}
	if documentPart, _ := content[2].(map[string]any); documentPart["type"] != "document" || documentPart["title"] != "notes.pdf" {
		t.Fatalf("unexpected document part: %#v", content[2])
	}
}

type fakeChatAttachmentPublisher struct{}

func (fakeChatAttachmentPublisher) UploadBytes(_ context.Context, payload []byte, objectKey string, mediaType string) (artifacts.PublishedObject, error) {
	return artifacts.PublishedObject{ObjectKey: objectKey, PublicURL: "https://files.example.com/" + objectKey, SizeBytes: int64(len(payload)), MediaType: mediaType}, nil
}

func TestUploadChatAttachmentsReturnsAttachmentMetadata(t *testing.T) {
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ChatAttachments: artifacts.Config{PublicBaseURL: "https://files.example.com", S3: artifacts.S3Config{Endpoint: "https://s3.example.com", Bucket: "chat", AccessKeyID: "key", AccessKeySecret: "secret"}}}, storage.NewMemoryStore())
	handler.newChatAttachmentPublisher = func(cfg artifacts.Config) (consoleChatAttachmentPublisher, error) {
		return fakeChatAttachmentPublisher{}, nil
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("files", "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write([]byte("png bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, consoleChatAttachmentUploadPath, body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	handler.uploadChatAttachments(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload consoleChatAttachmentUploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %#v", payload.Attachments)
	}
	if payload.Attachments[0].ObjectKey == "" || payload.Attachments[0].URL == "" {
		t.Fatalf("attachment metadata = %#v", payload.Attachments[0])
	}
}

func TestConsoleChatRoutesOnlyIncludeAllowedModels(t *testing.T) {
	routes := []domain.ModelRoute{
		{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "deepseek/deepseek-v4-pro", ProviderID: "openrouter", UpstreamModel: "deepseek/deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "openai/gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "gpt-5.5", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.5", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "gemini-3.1-pro-preview", ProviderID: "openrouter", UpstreamModel: "google/gemini-3.1-pro-preview", Protocol: domain.ProtocolOpenAI, Enabled: true},
	}
	providers := []domain.Provider{
		{ID: "deepseek", Name: "DeepSeek", Protocol: domain.ProtocolOpenAI, Status: domain.StatusEnabled},
		{ID: "openrouter", Name: "OpenRouter", Protocol: domain.ProtocolOpenAI, Status: domain.StatusEnabled},
	}

	views := consoleChatRoutes(routes, providers)
	if len(views) != 2 {
		t.Fatalf("expected 2 allowed routes, got %d: %+v", len(views), views)
	}

	joined := views[0].PublicName + "," + views[1].PublicName
	if joined != "deepseek-v4-pro,openai/gpt-5.4" {
		t.Fatalf("unexpected curated routes: %s", joined)
	}
	for _, blocked := range []string{"deepseek/deepseek-v4-pro", "gpt-5.5", "gemini-3.1-pro-preview"} {
		if strings.Contains(joined, blocked) {
			t.Fatalf("blocked model %q leaked into chat routes: %s", blocked, joined)
		}
	}
}

func TestParseConsoleChatJSONResponseCapturesReasoning(t *testing.T) {
	body := []byte(`{"id":"chatcmpl_reasoning","choices":[{"finish_reason":"stop","message":{"reasoning_content":"I should inspect weights first.","content":""}}],"usage":{"prompt_tokens":8,"completion_tokens":0,"total_tokens":8}}`)

	execution, err := parseConsoleChatJSONResponse(body, domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "ops-chat", UpstreamModel: "openai/gpt-5.4"}, domain.Provider{ID: "openrouter", Name: "OpenRouter"}, http.StatusOK, false, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Reasoning != "I should inspect weights first." {
		t.Fatalf("unexpected reasoning: %q", execution.Result.Reasoning)
	}
	if execution.Result.Output != consoleChatEmptyOutput {
		t.Fatalf("unexpected output fallback: %q", execution.Result.Output)
	}
	if execution.Result.TotalTokens != 8 {
		t.Fatalf("unexpected total tokens: %d", execution.Result.TotalTokens)
	}
}

func TestParseConsoleChatStreamResponseCapturesReasoningDeltas(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		"data: {\"id\":\"chatcmpl_reasoning\",\"choices\":[{\"delta\":{\"reasoning_content\":\"Inspect route weights. \"}}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Check provider health.\"}}]}",
		"",
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}",
		"",
		"data: [DONE]",
		"",
	}, "\n"))

	var seen strings.Builder
	execution, err := parseConsoleChatStreamResponse(stream, domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "ops-chat", UpstreamModel: "openai/gpt-5.4"}, domain.Provider{ID: "openrouter", Name: "OpenRouter"}, time.Unix(0, 0), nil, func(reasoning string) error {
		seen.WriteString(reasoning)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.String() != "Inspect route weights. Check provider health." {
		t.Fatalf("unexpected streamed reasoning: %q", seen.String())
	}
	if execution.Result.Reasoning != seen.String() {
		t.Fatalf("unexpected result reasoning: %q", execution.Result.Reasoning)
	}
	if execution.Result.Output != consoleChatEmptyOutput {
		t.Fatalf("unexpected output fallback: %q", execution.Result.Output)
	}
	if execution.Result.TotalTokens != 11 {
		t.Fatalf("unexpected total tokens: %d", execution.Result.TotalTokens)
	}
}

func TestParseConsoleChatStreamResponseReturnsReasoningCallbackError(t *testing.T) {
	callbackErr := errors.New("stop")
	stream := strings.NewReader("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Inspect route weights.\"}}]}\n\n")

	execution, err := parseConsoleChatStreamResponse(stream, domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "ops-chat"}, domain.Provider{ID: "openrouter", Name: "OpenRouter"}, time.Unix(0, 0), nil, func(string) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got %v", err)
	}
	if execution.Result.Reasoning != "Inspect route weights." {
		t.Fatalf("unexpected partial reasoning: %q", execution.Result.Reasoning)
	}
}

func TestConsoleChatDisplayOutputUsesReasoningPlaceholder(t *testing.T) {
	output := consoleChatDisplayOutput("en", consoleChatResultView{Output: consoleChatEmptyOutput, Reasoning: "Inspect route weights."})
	if output != "The model returned reasoning but no final answer text." {
		t.Fatalf("unexpected display output: %q", output)
	}

	zhOutput := consoleChatDisplayOutput("zh-CN", consoleChatResultView{Output: consoleChatEmptyOutput, Reasoning: "先检查权重。"})
	if zhOutput != "模型只返回了思考过程，没有返回最终答复。" {
		t.Fatalf("unexpected localized display output: %q", zhOutput)
	}
}
