package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

type recordingChatAttachmentPublisher struct {
	uploads []artifacts.PublishedObject
}

func (publisher *recordingChatAttachmentPublisher) UploadBytes(_ context.Context, payload []byte, objectKey string, mediaType string) (artifacts.PublishedObject, error) {
	published := artifacts.PublishedObject{
		ObjectKey: objectKey,
		PublicURL: "https://aiyolo-chat-assets.example.com/" + strings.TrimPrefix(objectKey, "chat/"),
		SizeBytes: int64(len(payload)),
		MediaType: mediaType,
	}
	publisher.uploads = append(publisher.uploads, published)
	return published, nil
}

func testChatAttachmentsConfig() artifacts.Config {
	return artifacts.Config{
		PublicBaseURL: "https://aiyolo-chat-assets.example.com",
		ProxyBasePath: "/console/chat/attachments/files",
		S3: artifacts.S3Config{
			Endpoint:        "https://oss.example.com",
			Bucket:          "aiyolo-chat-assets",
			Prefix:          "chat",
			AccessKeyID:     "test-key",
			AccessKeySecret: "test-secret",
		},
	}
}

func TestApplyGeneratedChatImagePersistenceUploadsOnceForStreamDelta(t *testing.T) {
	publisher := &recordingChatAttachmentPublisher{}
	handler := &Handler{
		cfg: Config{ChatAttachments: testChatAttachmentsConfig()},
		newChatAttachmentPublisher: func(artifacts.Config) (consoleChatAttachmentPublisher, error) {
			return publisher, nil
		},
	}
	route := domain.ModelRoute{PublicName: "flux-1.1-pro-ultra", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra"}
	wrapped, holder := handler.applyGeneratedChatImagePersistence(context.Background(), route, "admin@example.com", true, func(delta string) error {
		if !strings.Contains(delta, "/console/chat/attachments/files/") {
			t.Fatalf("unexpected delta: %q", delta)
		}
		return nil
	})
	if err := wrapped("![Generated image 1](data:image/png;base64,cG5n)"); err != nil {
		t.Fatal(err)
	}
	if holder == nil || !holder.set {
		t.Fatalf("holder=%+v", holder)
	}
	if len(publisher.uploads) != 1 {
		t.Fatalf("uploads=%d", len(publisher.uploads))
	}
}

func TestPersistGeneratedChatImageOutputUploadsDataURLToChatAssets(t *testing.T) {
	publisher := &recordingChatAttachmentPublisher{}
	handler := &Handler{
		cfg: Config{ChatAttachments: testChatAttachmentsConfig()},
		newChatAttachmentPublisher: func(artifacts.Config) (consoleChatAttachmentPublisher, error) {
			return publisher, nil
		},
	}

	output := handler.persistGeneratedChatImageOutput(context.Background(), "admin@example.com", "![Generated image 1](data:image/png;base64,cG5n)")
	if !strings.Contains(output, "/console/chat/attachments/files/") {
		t.Fatalf("unexpected persisted output: %q", output)
	}
	if strings.Contains(output, "data:image/png;base64") {
		t.Fatalf("expected data url to be replaced: %q", output)
	}
	if len(publisher.uploads) != 1 {
		t.Fatalf("uploads=%d", len(publisher.uploads))
	}
	if !strings.Contains(publisher.uploads[0].ObjectKey, "/generated/") {
		t.Fatalf("unexpected object key: %q", publisher.uploads[0].ObjectKey)
	}
}

func TestStreamChatPersistsGeneratedFluxImageToChatAssets(t *testing.T) {
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_flux","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","images":[{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]}}],"usage":{"prompt_tokens":8,"completion_tokens":0,"total_tokens":8}}`))
	}))
	defer providerBackend.Close()

	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPricingRule(ctx, domain.PricingRule{ID: "price_flux_assets", ModelAlias: "flux-1.1-pro-ultra", ProviderID: "openrouter", Currency: "USD", InputPricePerMillionTokens: 1000000, OutputPricePerMillionTokens: 2000000}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "flux-1.1-pro-ultra", ProviderID: "openrouter", UpstreamModel: "black-forest-labs/flux-1.1-pro-ultra", Protocol: domain.ProtocolOpenAI, PriceRuleID: "price_flux_assets", Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	publisher := &recordingChatAttachmentPublisher{}
	handler := NewHandler(Config{
		SecretKey:       "test-secret",
		AdminEmail:      "admin@example.com",
		AdminPassword:   "password",
		ChatAttachments: testChatAttachmentsConfig(),
	}, store)
	handler.newChatAttachmentPublisher = func(artifacts.Config) (consoleChatAttachmentPublisher, error) {
		return publisher, nil
	}
	server := httptest.NewServer(mountConsoleRoutesForTest(handler))
	defer server.Close()

	client, err := loggedInConsoleClientForTest(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"chat_public_name": {"flux-1.1-pro-ultra"}, "chat_draft": {"A rainy cyberpunk alley."}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat stream status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	var deltaText strings.Builder
	var doneOutput string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event consoleChatStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode stream event: %v line=%q", err, line)
		}
		if event.Type == "delta" {
			deltaText.WriteString(event.Delta)
		}
		if event.Type == "done" && event.Result != nil {
			doneOutput = event.Result.Output
		}
	}
	if !strings.Contains(deltaText.String(), "/console/chat/attachments/files/") {
		t.Fatalf("unexpected streamed delta text: %q", deltaText.String())
	}
	if strings.Contains(deltaText.String(), "data:image/png;base64") {
		t.Fatalf("streamed delta should not contain data url: %q", deltaText.String())
	}
	if !strings.Contains(doneOutput, "/console/chat/attachments/files/") {
		t.Fatalf("unexpected done output: %q", doneOutput)
	}
	if len(publisher.uploads) != 1 {
		t.Fatalf("uploads=%d", len(publisher.uploads))
	}
}

func mountConsoleRoutesForTest(handler *Handler) http.Handler {
	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	return router
}

func loggedInConsoleClientForTest(serverURL string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	login, err := http.NewRequest(http.MethodPost, serverURL+"/console/login", strings.NewReader(url.Values{"email": {"admin@example.com"}, "password": {"password"}}.Encode()))
	if err != nil {
		return nil, err
	}
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(login)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return client, nil
}

func TestRewriteChatAssetMarkdownURLsUsesConsoleProxy(t *testing.T) {
	cfg := testChatAttachmentsConfig()
	publicURL := "https://aiyolo-chat-assets.example.com/chat/admin-example.com/generated/2026/06/10/img-1.png"
	output := rewriteChatAssetMarkdownURLs(cfg, "![Generated image 1]("+publicURL+")")
	expected := "/console/chat/attachments/files/admin-example.com/generated/2026/06/10/img-1.png"
	if !strings.Contains(output, expected) {
		t.Fatalf("unexpected rewritten output: %q", output)
	}
}

func TestStreamChatWithoutChatAssetsKeepsRemoteGeneratedImageURL(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	providerBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"img_1","data":[{"url":"https://cdn.example.com/gpt-image-2.png"}],"usage":{"input_tokens":12,"output_tokens":0,"total_tokens":12}}`))
	}))
	defer providerBackend.Close()
	if err := store.UpsertProvider(ctx, domain.Provider{ID: "openrouter", Name: "OpenRouter", BaseURL: providerBackend.URL + "/v1", Protocol: domain.ProtocolOpenAI, MasterKey: "sk-chat-stream", Status: domain.StatusEnabled, TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoute(ctx, domain.ModelRoute{PublicName: "chatgpt-image-2", ProviderID: "openrouter", UpstreamModel: "openai/gpt-image-2", Protocol: domain.ProtocolOpenAI, Enabled: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	server := httptest.NewServer(mountConsoleRoutesForTest(handler))
	defer server.Close()
	client, err := loggedInConsoleClientForTest(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"chat_public_name": {"chatgpt-image-2"}, "chat_draft": {"A rainy cyberpunk alley."}}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/chat/stream", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "https://cdn.example.com/gpt-image-2.png") {
		t.Fatalf("expected remote generated image url to remain when chat assets are disabled: %s", body)
	}
}
