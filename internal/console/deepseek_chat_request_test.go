package console

import (
	"context"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestBuildConsoleChatUpstreamRequestUsesDeepSeekAnthropicBaseURL(t *testing.T) {
	request, err := buildConsoleChatUpstreamRequest(context.Background(), domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", MasterKey: "sk-ds-test"}, domain.ProtocolAnthropic, []byte(`{"model":"deepseek-v4-pro"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.URL.String(); got != "https://api.deepseek.com/anthropic/v1/messages" {
		t.Fatalf("url=%q", got)
	}
	if got := request.Header.Get("x-api-key"); got != "sk-ds-test" {
		t.Fatalf("x-api-key=%q", got)
	}
	if got := request.Header.Get("Authorization"); got != "" {
		t.Fatalf("authorization=%q, want empty", got)
	}
	if got := request.Header.Get("anthropic-version"); got != consoleAnthropicVersion {
		t.Fatalf("anthropic-version=%q", got)
	}
}

func TestBuildConsoleChatUpstreamRequestKeepsDeepSeekOpenAIBaseURL(t *testing.T) {
	request, err := buildConsoleChatUpstreamRequest(context.Background(), domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", MasterKey: "sk-ds-test"}, domain.ProtocolOpenAI, []byte(`{"model":"deepseek-v4-pro"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.URL.String(); got != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("url=%q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer sk-ds-test" {
		t.Fatalf("authorization=%q", got)
	}
	if got := request.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("accept=%q", got)
	}
}