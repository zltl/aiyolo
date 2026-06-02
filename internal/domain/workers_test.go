package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestNormalizeWorkerServerRetainsNormalizedLabels(t *testing.T) {
	worker, err := NormalizeWorkerServer(WorkerServer{
		ID:          " worker-1 ",
		SSHHost:     " 10.0.0.10 ",
		SSHUsername: " ubuntu ",
		SSHKeyID:    " deploy-key ",
		Labels:      []string{" Prod ", "prod", " ssh "},
	})
	if err != nil {
		t.Fatal(err)
	}

	if worker.Name != "worker-1" {
		t.Fatalf("Name=%q, want worker-1", worker.Name)
	}
	if !reflect.DeepEqual(worker.Labels, []string{"prod", "ssh"}) {
		t.Fatalf("Labels=%v, want [prod ssh]", worker.Labels)
	}
	if worker.LastProbeStatus != WorkerProbeStatusUnknown {
		t.Fatalf("LastProbeStatus=%q, want %q", worker.LastProbeStatus, WorkerProbeStatusUnknown)
	}
}

func TestNormalizeWorkerSSHKeyAcceptsAuthorizedKeyInput(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	authorized := string(ssh.MarshalAuthorizedKey(sshPublic))

	key, err := NormalizeWorkerSSHKey(WorkerSSHKey{ID: "deploy-key", PublicKey: authorized})
	if err != nil {
		t.Fatal(err)
	}

	want := ssh.FingerprintSHA256(sshPublic)
	if key.Fingerprint != want {
		t.Fatalf("Fingerprint=%q, want %q", key.Fingerprint, want)
	}
	if key.Name != "deploy-key" {
		t.Fatalf("Name=%q, want deploy-key", key.Name)
	}
	if key.PublicKey == "" {
		t.Fatal("PublicKey should be normalized from authorized key input")
	}
}

func TestNormalizeWorkerDisksDeduplicatesEntries(t *testing.T) {
	disks, err := NormalizeWorkerDisks([]WorkerDataDisk{
		{DevicePath: " /dev/vdb ", MountPath: " /mnt/data "},
		{DevicePath: "/dev/vdb", MountPath: "/mnt/data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 {
		t.Fatalf("len(disks)=%d, want 1", len(disks))
	}
	if disks[0].DevicePath != "/dev/vdb" || disks[0].MountPath != "/mnt/data" {
		t.Fatalf("unexpected normalized disk: %+v", disks[0])
	}

	session, err := NormalizeCloudAgentSession(CloudAgentSession{ID: "sess-1", UserID: "user-a", WorkerID: "worker-1", AccountID: "acct-1"})
	if err != nil {
		t.Fatal(err)
	}
	if session.AgentType != CloudAgentTypeCodex {
		t.Fatalf("AgentType=%q, want %q", session.AgentType, CloudAgentTypeCodex)
	}
	if session.Status != CloudAgentSessionStatusPending {
		t.Fatalf("Status=%q, want %q", session.Status, CloudAgentSessionStatusPending)
	}
	if session.WorkspacePath != DefaultCloudAgentWorkspacePath {
		t.Fatalf("WorkspacePath=%q, want %q", session.WorkspacePath, DefaultCloudAgentWorkspacePath)
	}
}
