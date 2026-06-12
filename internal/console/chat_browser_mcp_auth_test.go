package console

import (
	"strconv"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/auth"
)

func TestIssueAndVerifyBrowserMCPToken(t *testing.T) {
	handler := NewHandler(Config{SecretKey: "browser-mcp-secret"}, nil)
	token := handler.issueBrowserMCPToken("user@example.com", "session-123")
	userID, err := verifyBrowserMCPToken("browser-mcp-secret", token, "session-123")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "user@example.com" {
		t.Fatalf("userID=%q", userID)
	}
	if _, err := verifyBrowserMCPToken("browser-mcp-secret", token, "other-session"); err == nil {
		t.Fatal("expected session mismatch")
	}
}

func TestVerifyBrowserMCPTokenRejectsExpiredToken(t *testing.T) {
	expires := time.Now().Add(-time.Hour).UTC().Unix()
	payload := "aiyolo_browser_mcp:user@example.com:session-123:" + strconv.FormatInt(expires, 10)
	token := payload + ":" + auth.Sign(payload, "browser-mcp-secret")
	if _, err := verifyBrowserMCPToken("browser-mcp-secret", token, "session-123"); err == nil {
		t.Fatal("expected expired token error")
	}
}

func TestConsoleBrowserMCPURL(t *testing.T) {
	got := consoleBrowserMCPURL("https://aiyolo.example.com", "session-abc")
	want := "https://aiyolo.example.com/console/chat/browser/mcp?session=session-abc"
	if got != want {
		t.Fatalf("url=%q want=%q", got, want)
	}
}
