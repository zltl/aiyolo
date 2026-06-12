package console

import "testing"

func TestNormalizeConsoleChatMessagePreservesOperations(t *testing.T) {
	cfg := Config{}.ChatAttachments
	message, ok := normalizeConsoleChatMessage("zh-CN", consoleChatMessageView{
		ID:      "msg-1",
		Role:    "assistant",
		Content: "",
		Operations: []consoleChatStreamOperation{{
			ID:     "toolu_1",
			Name:   "WebFetch",
			Status: "completed",
			URL:    "https://example.com",
		}},
	}, cfg)
	if !ok {
		t.Fatal("expected message to be kept")
	}
	if len(message.Operations) != 1 || message.Operations[0].Name != "WebFetch" {
		t.Fatalf("operations=%+v", message.Operations)
	}
}
