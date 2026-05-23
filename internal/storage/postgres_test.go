package storage

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestSanitizeConsoleChatSessionStrings(t *testing.T) {
	bad := string([]byte{'b', 'a', 'd', 0xe7, 0xe2, 0x80})
	session := domain.ConsoleChatSession{
		ID:             bad,
		UserID:         bad,
		Title:          bad,
		PublicName:     bad,
		SystemPrompt:   bad,
		Status:         bad,
		MessagesJSON:   bad,
		LastRequestID:  bad,
		LastResponseID: bad,
		LastError:      bad,
	}

	got := sanitizeConsoleChatSessionStrings(session)
	fields := []string{got.ID, got.UserID, got.Title, got.PublicName, got.SystemPrompt, got.Status, got.MessagesJSON, got.LastRequestID, got.LastResponseID, got.LastError}
	for _, field := range fields {
		if !utf8.ValidString(field) {
			t.Fatalf("field is not valid UTF-8: %q", field)
		}
		if field == bad {
			t.Fatalf("field was not sanitized: %q", field)
		}
		if !strings.HasPrefix(field, "bad") {
			t.Fatalf("field lost valid prefix: %q", field)
		}
	}
	if !strings.ContainsRune(got.LastError, utf8.RuneError) {
		t.Fatalf("expected replacement rune in sanitized last error, got %q", got.LastError)
	}
}