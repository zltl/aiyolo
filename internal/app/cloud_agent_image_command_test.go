package app

import (
	"testing"

	"github.com/zltl/aiyolo/internal/artifacts"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

func TestCloudAgentASSArtifactURLs(t *testing.T) {
	downloadURL, sha256URL, err := cloudAgentASSArtifactURLs(Config{
		CodexPublicBaseURL: "https://aiyolo.quant67.com",
		Artifacts: artifacts.Config{
			PublicBaseURL:  "https://aiyolo.quant67.com",
			ProxyBasePath:  "/artifacts",
			PublicViaProxy: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDownload := "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass"
	wantSHA256 := "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass.sha256"
	if downloadURL != wantDownload || sha256URL != wantSHA256 {
		t.Fatalf("urls=%q %q, want %q %q", downloadURL, sha256URL, wantDownload, wantSHA256)
	}
}

func TestCloudAgentASSArtifactURLsRequiresPublicBase(t *testing.T) {
	if _, _, err := cloudAgentASSArtifactURLs(Config{}); err == nil {
		t.Fatal("expected error when artifacts are not configured")
	}
}

func TestBuildCloudAgentImageOnWorkerCommandRegistered(t *testing.T) {
	cmd := NewRootCommand()
	found := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "build-cloud-agent-image-on-worker" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected build-cloud-agent-image-on-worker subcommand")
	}
	_ = workerops.CloudAgentASSArtifactObjectKey
}
