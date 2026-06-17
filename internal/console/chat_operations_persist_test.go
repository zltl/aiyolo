package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

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

func TestConsoleChatPreferRicherSessionMessagesKeepsExistingAssistantReply(t *testing.T) {
	cfg := Config{}.ChatAttachments
	existingJSON := `[{"id":"u1","role":"user","content":"hello"},{"id":"a1","role":"assistant","content":"Recovered output"}]`
	incoming := []consoleChatMessageView{{ID: "u1", Role: "user", Content: "hello"}}
	got := consoleChatPreferRicherSessionMessages("zh-CN", existingJSON, incoming, cfg)
	if len(got) != 2 || got[1].Content != "Recovered output" {
		t.Fatalf("expected existing assistant reply to be kept, got %+v", got)
	}
}

func TestConsoleChatPreferRicherSessionMessagesAcceptsLongerIncoming(t *testing.T) {
	cfg := Config{}.ChatAttachments
	existingJSON := `[{"id":"u1","role":"user","content":"hello"}]`
	incoming := []consoleChatMessageView{
		{ID: "u1", Role: "user", Content: "hello"},
		{ID: "a1", Role: "assistant", Content: "New reply"},
	}
	got := consoleChatPreferRicherSessionMessages("zh-CN", existingJSON, incoming, cfg)
	if len(got) != 2 || !strings.Contains(got[1].Content, "New reply") {
		t.Fatalf("expected incoming transcript to win, got %+v", got)
	}
}

func TestPersistConsoleChatSessionForUserKeepsRicherExistingMessages(t *testing.T) {
	store := storage.NewMemoryStore()
	ctx := context.Background()
	cfg := Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}
	handler := NewHandler(cfg, store)
	now := time.Now().UTC()
	if err := store.UpsertConsoleChatSession(ctx, domain.ConsoleChatSession{
		UserID:       "admin@example.com",
		ID:           "session-persist-rich",
		Title:        "Recovered thread",
		PublicName:   "gpt-5.4",
		Status:       "completed",
		MessagesJSON: `[{"id":"u1","role":"user","content":"hello"},{"id":"a1","role":"assistant","content":"Recovered output"}]`,
		MessageCount: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := handler.persistConsoleChatSessionForUser(ctx, "zh-CN", "admin@example.com", "session-persist-rich", "gpt-5.4", "", "", nil, []consoleChatMessageView{
		{ID: "u1", Role: "user", Content: "hello"},
	}, "streaming", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	session, err := store.GetConsoleChatSession(ctx, "admin@example.com", "session-persist-rich")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(session.MessagesJSON, "Recovered output") {
		t.Fatalf("assistant reply was overwritten by stale persist: %s", session.MessagesJSON)
	}
}
