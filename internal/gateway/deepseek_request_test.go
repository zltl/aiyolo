package gateway

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestBuildUpstreamRequestUsesDeepSeekAnthropicBaseURL(t *testing.T) {
	handler := &Handler{}
	clientRequest := httptest.NewRequest("POST", "/v1/messages", nil)
	clientRequest.Header.Set("anthropic-version", "2023-06-01")

	request, err := handler.buildUpstreamRequest(context.Background(), clientRequest, "/v1/messages", domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", MasterKey: "sk-ds-test"}, domain.ProtocolAnthropic, []byte(`{"model":"deepseek-v4-pro"}`))
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
	if got := request.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version=%q", got)
	}
}

func TestBuildUpstreamRequestKeepsDeepSeekOpenAIBaseURL(t *testing.T) {
	handler := &Handler{}
	clientRequest := httptest.NewRequest("POST", "/v1/chat/completions", nil)

	request, err := handler.buildUpstreamRequest(context.Background(), clientRequest, "/v1/chat/completions", domain.Provider{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", MasterKey: "sk-ds-test"}, domain.ProtocolOpenAI, []byte(`{"model":"deepseek-v4-pro"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := request.URL.String(); got != "https://api.deepseek.com/v1/chat/completions" {
		t.Fatalf("url=%q", got)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer sk-ds-test" {
		t.Fatalf("authorization=%q", got)
	}
}