package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestBuildConsoleChatRequestBodyIncludesOpenAIMultimodalParts(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.Provider{}, domain.ModelRoute{PublicName: "ops-chat", UpstreamModel: "openai/gpt-5.5"}, "system", nil, "review this screenshot", []consoleChatAttachmentView{{ObjectKey: "chat/user/upload.png", URL: "https://files.example.com/chat/user/upload.png", MediaType: "image/png", Name: "upload.png"}, {ObjectKey: "chat/user/spec.pdf", URL: "https://files.example.com/chat/user/spec.pdf", MediaType: "application/pdf", Name: "spec.pdf"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := int(payload["max_tokens"].(float64)); got != consoleChatDefaultCompletionTokens {
		t.Fatalf("max_tokens=%d", got)
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

func TestBuildConsoleChatRequestBodyUsesLargerBudgetForDeepSeekV4Pro(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.Provider{}, domain.ModelRoute{PublicName: "ops-chat", UpstreamModel: "deepseek-v4-pro"}, "", nil, "summarize this", nil, true, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := int(payload["max_tokens"].(float64)); got != consoleChatReasoningCompletionTokens {
		t.Fatalf("max_tokens=%d", got)
	}
	streamOptions, ok := payload["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options=%#v", payload["stream_options"])
	}
}

func TestBuildConsoleChatRequestBodyUsesChatCompletionsForFluxImageModels(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.Provider{}, domain.ModelRoute{PublicName: "flux-1.1-pro-ultra", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra"}, "Keep it cinematic.", nil, "Generate a rainy cyberpunk alley.", nil, true, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "black-forest-labs/flux-1.1-pro-ultra" {
		t.Fatalf("model=%#v", payload["model"])
	}
	modalities, ok := payload["modalities"].([]any)
	if !ok || len(modalities) != 1 || modalities[0] != "image" {
		t.Fatalf("modalities=%#v", payload["modalities"])
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages=%#v", payload["messages"])
	}
	if _, exists := payload["prompt"]; exists {
		t.Fatalf("chat completion payload should not include prompt: %#v", payload)
	}
}

func TestBuildConsoleChatRequestBodyUsesImagesAPIForGPTImage2(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.Provider{}, domain.ModelRoute{PublicName: "gpt-image-2", UpstreamModel: "openai/gpt-image-2"}, "Keep it cinematic.", nil, "Generate a rainy cyberpunk alley.", nil, true, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "openai/gpt-image-2" {
		t.Fatalf("model=%#v", payload["model"])
	}
	if payload["response_format"] != "url" {
		t.Fatalf("response_format=%#v", payload["response_format"])
	}
	prompt, _ := payload["prompt"].(string)
	if !strings.Contains(prompt, "Keep it cinematic.") || !strings.Contains(prompt, "Generate a rainy cyberpunk alley.") {
		t.Fatalf("prompt=%q", prompt)
	}
	if _, exists := payload["messages"]; exists {
		t.Fatalf("images payload should not include messages: %#v", payload)
	}
}

func TestBuildConsoleChatRequestBodyIncludesAnthropicDocumentParts(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolAnthropic, domain.Provider{}, domain.ModelRoute{PublicName: "claude-sonnet", UpstreamModel: "claude-sonnet-4-5"}, "", nil, "summarize these inputs", []consoleChatAttachmentView{{ObjectKey: "chat/user/diagram.png", URL: "https://files.example.com/chat/user/diagram.png", MediaType: "image/png", Name: "diagram.png"}, {ObjectKey: "chat/user/notes.pdf", URL: "https://files.example.com/chat/user/notes.pdf", MediaType: "application/pdf", Name: "notes.pdf"}}, false, "")
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

func TestBuildConsoleChatRequestBodyUsesAnthropicBase64ImageSource(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolAnthropic, domain.Provider{}, domain.ModelRoute{PublicName: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"}, "", nil, "look at this image", []consoleChatAttachmentView{{ObjectKey: "chat/user/diagram.png", URL: "data:image/png;base64,cG5n", MediaType: "image/png", Name: "diagram.png"}}, false, "")
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
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v", userMessage["content"])
	}
	imagePart, _ := content[1].(map[string]any)
	if imagePart["type"] != "image" {
		t.Fatalf("unexpected image part: %#v", imagePart)
	}
	source, _ := imagePart["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "cG5n" {
		t.Fatalf("unexpected image source: %#v", source)
	}
}

func TestBuildConsoleChatRequestBodyIncludesDeepSeekReasoningEffortForOpenAI(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolOpenAI, domain.Provider{ID: "deepseek"}, domain.ModelRoute{PublicName: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"}, "", nil, "summarize this", nil, true, "high")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%#v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking=%#v", payload["thinking"])
	}
}

func TestBuildConsoleChatRequestBodyIncludesDeepSeekReasoningEffortForAnthropic(t *testing.T) {
	body, err := buildConsoleChatRequestBody(domain.ProtocolAnthropic, domain.Provider{ID: "deepseek"}, domain.ModelRoute{PublicName: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"}, "", nil, "summarize this", nil, false, "max")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	thinking, ok := payload["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking=%#v", payload["thinking"])
	}
	outputConfig, ok := payload["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "max" {
		t.Fatalf("output_config=%#v", payload["output_config"])
	}
}

type fakeChatAttachmentPublisher struct{}

func (fakeChatAttachmentPublisher) UploadBytes(_ context.Context, payload []byte, objectKey string, mediaType string) (artifacts.PublishedObject, error) {
	return artifacts.PublishedObject{ObjectKey: objectKey, PublicURL: "https://files.example.com/" + objectKey, SizeBytes: int64(len(payload)), MediaType: mediaType}, nil
}

type fakeChatAttachmentReader struct {
	payload   []byte
	mediaType string
	err       error
}

func (reader fakeChatAttachmentReader) ReadObject(_ context.Context, _ string) ([]byte, string, error) {
	if reader.err != nil {
		return nil, "", reader.err
	}
	return append([]byte(nil), reader.payload...), reader.mediaType, nil
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

func TestUploadChatAttachmentsWithPrefixReturnsUsableURL(t *testing.T) {
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password", ChatAttachments: artifacts.Config{PublicBaseURL: "https://files.example.com", S3: artifacts.S3Config{Endpoint: "https://s3.example.com", Bucket: "chat", Prefix: "chat", AccessKeyID: "key", AccessKeySecret: "secret"}}}, storage.NewMemoryStore())
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
	if strings.Contains(payload.Attachments[0].URL, "/chat/chat/") {
		t.Fatalf("attachment url duplicated configured prefix: %#v", payload.Attachments[0])
	}
	if !strings.HasPrefix(payload.Attachments[0].URL, "https://files.example.com/chat/") {
		t.Fatalf("attachment url = %q", payload.Attachments[0].URL)
	}
	if payload.Attachments[0].BrowserURL != "/console/chat/attachments/files/"+payload.Attachments[0].ObjectKey {
		t.Fatalf("attachment browserUrl = %q", payload.Attachments[0].BrowserURL)
	}
}

func TestPrepareConsoleChatAttachmentsForDeepSeekInlinesImages(t *testing.T) {
	handler := &Handler{
		cfg: Config{ChatAttachments: artifacts.Config{PublicBaseURL: "https://files.example.com", ProxyBasePath: "/console/chat/attachments/files", S3: artifacts.S3Config{Endpoint: "https://s3.example.com", Bucket: "chat", Prefix: "chat", AccessKeyID: "key", AccessKeySecret: "secret"}}},
		newChatAttachmentReader: func(cfg artifacts.Config) (consoleChatAttachmentObjectReader, error) {
			return fakeChatAttachmentReader{payload: []byte("png bytes"), mediaType: "image/png"}, nil
		},
	}
	history := []consoleChatMessageView{{Role: "user", Content: "look", Attachments: []consoleChatAttachmentView{{ObjectKey: "chat/user/diagram.png", URL: "https://files.example.com/chat/user/diagram.png", BrowserURL: "/console/chat/attachments/files/chat/user/diagram.png", MediaType: "image/png", Name: "diagram.png"}}}}
	attachments := []consoleChatAttachmentView{{ObjectKey: "chat/user/new.png", URL: "https://files.example.com/chat/user/new.png", BrowserURL: "/console/chat/attachments/files/chat/user/new.png", MediaType: "image/png", Name: "new.png"}}

	preparedHistory, preparedAttachments, err := handler.prepareConsoleChatAttachmentsForProvider(context.Background(), domain.ProtocolOpenAI, domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Protocol: domain.ProtocolOpenAI}, history, attachments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(preparedHistory[0].Attachments[0].URL, "data:image/png;base64,") {
		t.Fatalf("history attachment url = %q", preparedHistory[0].Attachments[0].URL)
	}
	if !strings.HasPrefix(preparedAttachments[0].URL, "data:image/png;base64,") {
		t.Fatalf("prepared attachment url = %q", preparedAttachments[0].URL)
	}
	if preparedAttachments[0].BrowserURL != "/console/chat/attachments/files/chat/user/new.png" {
		t.Fatalf("browser url = %q", preparedAttachments[0].BrowserURL)
	}
}

func TestConsoleChatExecutionProtocolPrefersAnthropicForDeepSeekImages(t *testing.T) {
	handler := &Handler{}
	protocol := handler.consoleChatExecutionProtocol(
		domain.ModelRoute{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI},
		domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Protocol: domain.ProtocolOpenAI, SupportedProtocols: []string{domain.ProtocolOpenAI, domain.ProtocolAnthropic}},
		nil,
		[]consoleChatAttachmentView{{ObjectKey: "chat/user/diagram.png", URL: "https://files.example.com/chat/user/diagram.png", MediaType: "image/png", Name: "diagram.png"}},
	)
	if protocol != domain.ProtocolAnthropic {
		t.Fatalf("protocol = %q", protocol)
	}
}

func TestConsoleChatRoutesIncludeAllCompatibleEnabledModels(t *testing.T) {
	routes := []domain.ModelRoute{
		{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "deepseek/deepseek-v4-pro", ProviderID: "openrouter", UpstreamModel: "deepseek/deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "openai/gpt-5.4", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.4", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "claude-opus-4.7", ProviderID: "anthropic-main", UpstreamModel: "claude-opus-4.7", Protocol: domain.ProtocolAnthropic, Enabled: true},
		{PublicName: "claude-sonnet-4.6", ProviderID: "anthropic-main", UpstreamModel: "claude-sonnet-4.6", Protocol: domain.ProtocolAnthropic, Enabled: true},
		{PublicName: "gpt-5.5", ProviderID: "openrouter", UpstreamModel: "openai/gpt-5.5", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "gpt-image-2", ProviderID: "openrouter", UpstreamModel: "openai/gpt-image-2", Protocol: domain.ProtocolOpenAI, Enabled: true},
		{PublicName: "gemini-3.1-pro-preview", ProviderID: "openrouter", UpstreamModel: "google/gemini-3.1-pro-preview", Protocol: domain.ProtocolOpenAI, Enabled: true},
	}
	providers := []domain.Provider{
		{ID: "deepseek", Name: "DeepSeek", Protocol: domain.ProtocolOpenAI, Status: domain.StatusEnabled},
		{ID: "openrouter", Name: "OpenRouter", Protocol: domain.ProtocolOpenAI, Status: domain.StatusEnabled},
		{ID: "anthropic-main", Name: "Anthropic", Protocol: domain.ProtocolAnthropic, Status: domain.StatusEnabled},
	}

	views := consoleChatRoutes(routes, providers)
	if len(views) != 8 {
		t.Fatalf("expected 8 compatible routes, got %d: %+v", len(views), views)
	}

	joined := strings.Join([]string{views[0].PublicName, views[1].PublicName, views[2].PublicName, views[3].PublicName, views[4].PublicName, views[5].PublicName, views[6].PublicName, views[7].PublicName}, ",")
	if joined != "claude-opus-4.7,claude-sonnet-4.6,deepseek-v4-pro,deepseek/deepseek-v4-pro,gemini-3.1-pro-preview,gpt-5.5,gpt-image-2,openai/gpt-5.4" {
		t.Fatalf("unexpected compatible routes: %s", joined)
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

func TestParseConsoleChatJSONResponseBuildsChatCompletionImageMarkdownOutput(t *testing.T) {
	body := []byte(`{"id":"chatcmpl_flux","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","images":[{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]}}],"usage":{"prompt_tokens":12,"completion_tokens":0,"total_tokens":12}}`)

	execution, err := parseConsoleChatJSONResponse(body, domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "flux-1.1-pro-ultra", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra"}, domain.Provider{ID: "openrouter", Name: "OpenRouter"}, http.StatusOK, false, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Output != "![Generated image 1](data:image/png;base64,cG5n)" {
		t.Fatalf("unexpected output: %q", execution.Result.Output)
	}
}

func TestParseConsoleChatJSONResponseBuildsImageMarkdownOutput(t *testing.T) {
	body := []byte(`{"id":"img_1","data":[{"url":"https://cdn.example.com/generated.png"}],"usage":{"input_tokens":12,"output_tokens":0,"total_tokens":12}}`)

	execution, err := parseConsoleChatJSONResponse(body, domain.ProtocolOpenAI, domain.ModelRoute{PublicName: "gpt-image-2", UpstreamModel: "openai/gpt-image-2"}, domain.Provider{ID: "openrouter", Name: "OpenRouter"}, http.StatusOK, false, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Output != "![Generated image 1](https://cdn.example.com/generated.png)" {
		t.Fatalf("unexpected output: %q", execution.Result.Output)
	}
	if execution.Result.FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: %q", execution.Result.FinishReason)
	}
	if execution.Result.TotalTokens != 12 {
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

func TestRunConsoleChatTurnWithFluxImageModelUsesChatCompletions(t *testing.T) {
	var requests atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		modalities, ok := payload["modalities"].([]any)
		if !ok || len(modalities) != 1 || modalities[0] != "image" {
			t.Fatalf("modalities=%#v", payload["modalities"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_flux","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","images":[{"type":"image_url","image_url":{"url":"https://cdn.example.com/generated.png"}}]}}],"usage":{"prompt_tokens":8,"completion_tokens":0,"total_tokens":8}}`))
	}))
	defer providerBackend.Close()

	provider := domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat", Status: domain.StatusEnabled, TimeoutSeconds: 30}
	route := domain.ModelRoute{PublicName: "flux-1.1-pro-ultra", ProviderID: "openrouter", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra", Protocol: domain.ProtocolOpenAI, Enabled: true}
	var streamed strings.Builder

	execution, err := runConsoleChatTurnWithContinuation(context.Background(), domain.ProtocolOpenAI, provider, route, domain.ProxyProfile{}, "", "", nil, "A rainy cyberpunk alley.", nil, true, func(delta string) error {
		streamed.WriteString(delta)
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	if execution.Result.Output != "![Generated image 1](https://cdn.example.com/generated.png)" {
		t.Fatalf("unexpected output: %q", execution.Result.Output)
	}
	if streamed.String() != execution.Result.Output {
		t.Fatalf("unexpected streamed output: %q", streamed.String())
	}
}

func TestRunConsoleChatTurnWithContinuationAutoRetriesLength(t *testing.T) {
	var requests atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		call := requests.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		messages, ok := payload["messages"].([]any)
		if !ok {
			t.Fatalf("messages payload = %#v", payload["messages"])
		}
		switch call {
		case 1:
			if len(messages) != 1 {
				t.Fatalf("first request messages = %#v", messages)
			}
			message, _ := messages[0].(map[string]any)
			if message["role"] != "user" || message["content"] != "How would you route failover?" {
				t.Fatalf("unexpected first request message: %#v", message)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_partial\",\"choices\":[{\"delta\":{\"content\":\"Route \"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case 2:
			if len(messages) != 3 {
				t.Fatalf("second request messages = %#v", messages)
			}
			userMessage, _ := messages[0].(map[string]any)
			assistantMessage, _ := messages[1].(map[string]any)
			continueMessage, _ := messages[2].(map[string]any)
			if userMessage["role"] != "user" || userMessage["content"] != "How would you route failover?" {
				t.Fatalf("unexpected original user message: %#v", userMessage)
			}
			if assistantMessage["role"] != "assistant" || assistantMessage["content"] != "Route" {
				t.Fatalf("unexpected partial assistant message: %#v", assistantMessage)
			}
			if continueMessage["role"] != "user" || continueMessage["content"] != consoleChatContinuationPrompt {
				t.Fatalf("unexpected continuation prompt: %#v", continueMessage)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_final\",\"choices\":[{\"delta\":{\"content\":\"failover via weights.\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":5,\"total_tokens\":11}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected extra upstream call %d", call)
		}
	}))
	defer providerBackend.Close()

	provider := domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat", Status: domain.StatusEnabled, TimeoutSeconds: 30}
	route := domain.ModelRoute{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true}
	var streamed strings.Builder

	execution, err := runConsoleChatTurnWithContinuation(context.Background(), domain.ProtocolOpenAI, provider, route, domain.ProxyProfile{}, "", "", nil, "How would you route failover?", nil, true, func(delta string) error {
		streamed.WriteString(delta)
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	if streamed.String() != "Route failover via weights." {
		t.Fatalf("unexpected streamed text: %q", streamed.String())
	}
	if execution.Result.Output != "Route failover via weights." {
		t.Fatalf("unexpected output: %q", execution.Result.Output)
	}
	if execution.Result.FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: %q", execution.Result.FinishReason)
	}
	if execution.Usage.InputTokens != 14 || execution.Usage.OutputTokens != 9 || execution.Result.TotalTokens != 23 {
		t.Fatalf("unexpected usage aggregation: %+v", execution.Usage)
	}
}

func TestRunConsoleChatTurnAutoRetriesLengthForJSONResponses(t *testing.T) {
	var requests atomic.Int32
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		call := requests.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		messages, ok := payload["messages"].([]any)
		if !ok {
			t.Fatalf("messages payload = %#v", payload["messages"])
		}
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			if len(messages) != 1 {
				t.Fatalf("first request messages = %#v", messages)
			}
			_, _ = w.Write([]byte(`{"id":"chatcmpl_json_partial","choices":[{"index":0,"message":{"role":"assistant","content":"Route "},"finish_reason":"length"}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`))
		case 2:
			if len(messages) != 3 {
				t.Fatalf("second request messages = %#v", messages)
			}
			continueMessage, _ := messages[2].(map[string]any)
			if continueMessage["content"] != consoleChatContinuationPrompt {
				t.Fatalf("unexpected continuation prompt: %#v", continueMessage)
			}
			_, _ = w.Write([]byte(`{"id":"chatcmpl_json_final","choices":[{"index":0,"message":{"role":"assistant","content":"failover via weights."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
		default:
			t.Fatalf("unexpected extra upstream call %d", call)
		}
	}))
	defer providerBackend.Close()

	provider := domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat", Status: domain.StatusEnabled, TimeoutSeconds: 30}
	route := domain.ModelRoute{PublicName: "deepseek-v4-pro", ProviderID: "deepseek", UpstreamModel: "deepseek-v4-pro", Protocol: domain.ProtocolOpenAI, Enabled: true}

	execution, err := runConsoleChatTurn(context.Background(), provider, route, domain.ProxyProfile{}, "", "", nil, "How would you route failover?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	if execution.Result.Output != "Route failover via weights." {
		t.Fatalf("unexpected output: %q", execution.Result.Output)
	}
	if execution.Result.FinishReason != "stop" {
		t.Fatalf("unexpected finish reason: %q", execution.Result.FinishReason)
	}
	if execution.Usage.InputTokens != 14 || execution.Usage.OutputTokens != 9 || execution.Result.TotalTokens != 23 {
		t.Fatalf("unexpected usage aggregation: %+v", execution.Usage)
	}
}

type fakeStreamTimeoutError struct{}

func (fakeStreamTimeoutError) Error() string {
	return "read tcp 127.0.0.1:12345->127.0.0.1:443: i/o timeout"
}
func (fakeStreamTimeoutError) Timeout() bool   { return true }
func (fakeStreamTimeoutError) Temporary() bool { return true }

func TestConsoleChatStreamFailureDetailClassifiesIdleTimeout(t *testing.T) {
	handler := &Handler{}
	request := httptest.NewRequest(http.MethodPost, consoleChatStreamPath, nil)
	err := fmt.Errorf("read streamed response: %w", fakeStreamTimeoutError{})

	if code := consoleChatErrorCode(err); code != "stream_idle_timeout" {
		t.Fatalf("unexpected error code: %s", code)
	}

	detail := handler.consoleChatStreamFailureDetail(request, err, domain.Provider{TimeoutSeconds: 30, StreamIdleTimeoutSeconds: 15}, domain.ProxyProfile{})
	if !strings.Contains(detail, "15 秒的流空闲超时") {
		t.Fatalf("unexpected timeout detail: %q", detail)
	}
}

func TestConsoleChatDisplayOutputUsesReasoningPlaceholder(t *testing.T) {
	output := consoleChatDisplayOutput("zh-CN", consoleChatResultView{Output: consoleChatEmptyOutput, Reasoning: "先检查权重。"})
	if output != "模型只返回了思考过程，没有返回最终答复。" {
		t.Fatalf("unexpected display output: %q", output)
	}
}
