package console

import "testing"

func TestConsoleChatBrowserMCPToolsIncludeBrowserActions(t *testing.T) {
	tools := consoleChatBrowserMCPTools()
	if len(tools) != 3 {
		t.Fatalf("tools=%d, want 3", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"browser_navigate", "browser_screenshot", "browser_snapshot"} {
		if !names[expected] {
			t.Fatalf("missing tool %q", expected)
		}
	}
}

func TestConsoleChatBrowserCDPURLIncludesSession(t *testing.T) {
	got := consoleChatBrowserCDPURL("session-abc")
	if got == "" || !containsAll(got, "session=session-abc", "/console/chat/browser/cdp/json") {
		t.Fatalf("cdp url = %q", got)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if part == "" || !contains(value, part) {
			return false
		}
	}
	return true
}

func contains(value, part string) bool {
	return len(part) == 0 || (len(value) >= len(part) && indexOf(value, part) >= 0)
}

func indexOf(value, part string) int {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
