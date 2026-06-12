package console

import (
	"strings"
	"testing"
)

func TestConsoleChatBrowserMCPEnabledFromStateDefaultsTrue(t *testing.T) {
	if !consoleChatBrowserMCPEnabledFromState(consoleChatShellState{}) {
		t.Fatal("expected default enabled")
	}
	disabled := false
	if consoleChatBrowserMCPEnabledFromState(consoleChatShellState{BrowserMCPEnabled: &disabled}) {
		t.Fatal("expected disabled state")
	}
	enabled := true
	if !consoleChatBrowserMCPEnabledFromState(consoleChatShellState{BrowserMCPEnabled: &enabled}) {
		t.Fatal("expected enabled state")
	}
}

func TestConsoleChatShellStatePayloadKeepsBrowserMCPPreference(t *testing.T) {
	disabled := false
	payload := consoleChatShellStatePayload(consoleChatShellState{BrowserMCPEnabled: &disabled})
	if payload == "" || !strings.Contains(payload, `"browserMCPEnabled":false`) {
		t.Fatalf("payload=%q", payload)
	}
}
