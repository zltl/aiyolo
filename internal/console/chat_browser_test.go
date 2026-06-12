package console

import (
	"strings"
	"testing"
)

func TestConsoleChatBrowserViewURLIncludesSession(t *testing.T) {
	got := consoleChatBrowserViewURL("session-123")
	want := "/console/chat/browser/view?session=session-123"
	if got != want {
		t.Fatalf("view URL = %q, want %q", got, want)
	}
}

func TestConsoleChatBrowserSocketURLIncludesSession(t *testing.T) {
	got := consoleChatBrowserSocketURL("session-456")
	want := "/console/chat/browser/ws?session=session-456"
	if got != want {
		t.Fatalf("socket URL = %q, want %q", got, want)
	}
}

func TestConsoleChatBrowserShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := consoleChatBrowserShellQuote("it's a test")
	want := "'it'\"'\"'s a test'"
	if got != want {
		t.Fatalf("shell quote = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Fatalf("shell quote should be wrapped in single quotes: %q", got)
	}
}
