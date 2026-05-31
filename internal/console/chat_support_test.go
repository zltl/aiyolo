package console

import "testing"

func TestFindConsoleChatRouteMatchesGPTImage2Aliases(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "chatgpt-image-2", UpstreamModel: "chatgpt-image-2"},
	}

	for _, requested := range []string{"gpt-image-2", "openai/gpt-image-2", "chatgpt-image-2"} {
		route, ok := findConsoleChatRoute(routes, requested)
		if !ok {
			t.Fatalf("expected alias %q to resolve", requested)
		}
		if route.PublicName != "chatgpt-image-2" {
			t.Fatalf("alias %q resolved to unexpected route: %+v", requested, route)
		}
	}
}

func TestConsoleChatFilterRoutesByAllowedModels(t *testing.T) {
	routes := []consoleChatRouteView{
		{PublicName: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"},
		{PublicName: "openai/gpt-5.4", UpstreamModel: "openai/gpt-5.4"},
		{PublicName: "chatgpt-image-2", UpstreamModel: "chatgpt-image-2"},
		{PublicName: "anthropic/claude-opus-4.7", UpstreamModel: "anthropic/claude-opus-4.7"},
	}
	filtered := consoleChatFilterRoutesByAllowedModels(routes, []string{"deepseek-v4-pro", "openai/gpt-5.4", "gpt-image-2"})
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered routes, got %d: %+v", len(filtered), filtered)
	}
	if filtered[0].PublicName != "deepseek-v4-pro" || filtered[1].PublicName != "openai/gpt-5.4" || filtered[2].PublicName != "chatgpt-image-2" {
		t.Fatalf("unexpected filtered routes: %+v", filtered)
	}
}
