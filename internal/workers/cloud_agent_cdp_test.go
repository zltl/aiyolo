package workers

import "testing"

func TestCloudAgentChromeHostPortMatchesEnsureOptions(t *testing.T) {
	userID := "i@quant67.com"
	workerID := "worker-0"
	got := CloudAgentChromeHostPort(userID, workerID)
	want := cloudAgentHostPort(userID, workerID+"-chrome", defaultCloudAgentHostChromeBasePort)
	if got != want {
		t.Fatalf("CloudAgentChromeHostPort() = %d, want %d", got, want)
	}
	if got < defaultCloudAgentHostChromeBasePort {
		t.Fatalf("chrome host port %d should be >= base %d", got, defaultCloudAgentHostChromeBasePort)
	}
}

func TestCloudAgentChromeDevToolsWSPath(t *testing.T) {
	got := cloudAgentChromeDevToolsWSPath("ws://127.0.0.1:19042/devtools/page/ABC123")
	want := "/devtools/page/ABC123"
	if got != want {
		t.Fatalf("ws path = %q, want %q", got, want)
	}
}
