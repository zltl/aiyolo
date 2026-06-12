package workers

import "testing"

func TestCloudAgentVNCHostPortMatchesEnsureOptions(t *testing.T) {
	userID := "i@quant67.com"
	workerID := "worker-0"
	got := CloudAgentVNCHostPort(userID, workerID)
	want := cloudAgentHostPort(userID, workerID, defaultCloudAgentHostVNCBasePort)
	if got != want {
		t.Fatalf("CloudAgentVNCHostPort() = %d, want %d", got, want)
	}
	if got < defaultCloudAgentHostVNCBasePort {
		t.Fatalf("VNC host port %d should be >= base %d", got, defaultCloudAgentHostVNCBasePort)
	}
}
