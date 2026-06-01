package storage_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestMemoryStoreCloudWorkersRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()

	privateKey := mustGeneratePrivateKeyPEM(t)
	if err := store.UpsertWorkerSSHKey(ctx, domain.WorkerSSHKey{ID: "ssh-key-1", PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	sshKey, err := store.GetWorkerSSHKey(ctx, "ssh-key-1")
	if err != nil {
		t.Fatal(err)
	}
	if sshKey.Fingerprint == "" {
		t.Fatal("expected fingerprint")
	}
	if sshKey.PrivateKey != privateKey {
		t.Fatal("expected private key round-trip")
	}

	worker := domain.WorkerServer{ID: "worker-1", SSHHost: "10.0.0.5", SSHUsername: "ubuntu", SSHKeyID: sshKey.ID}
	if err := store.UpsertWorkerServer(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceWorkerDataDisks(ctx, worker.ID, []domain.WorkerDataDisk{{DevicePath: "/dev/vdb", MountPath: "/srv/aiyolo"}}); err != nil {
		t.Fatal(err)
	}
	disks, err := store.ListWorkerDataDisks(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].DevicePath != "/dev/vdb" {
		t.Fatalf("unexpected disks: %+v", disks)
	}

	job := domain.WorkerInitJob{ID: "job-1", WorkerID: worker.ID, TriggeredBy: "user@example.com"}
	if err := store.UpsertWorkerInitJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Message: "probe complete"}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListWorkerInitJobEvents(ctx, worker.ID, job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("unexpected events: %+v", events)
	}

	accountA := domain.CloudAgentAccount{ID: "acct-1", UserID: "user-a", WorkerID: worker.ID, Credential: "token-a"}
	accountB := domain.CloudAgentAccount{ID: "acct-2", UserID: "user-b", WorkerID: worker.ID, Credential: "token-b"}
	if err := store.UpsertCloudAgentAccount(ctx, accountA); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentAccount(ctx, accountB); err != nil {
		t.Fatal(err)
	}
	sessionA := domain.CloudAgentSession{ID: "sess-1", UserID: "user-a", WorkerID: worker.ID, AccountID: accountA.ID, ShellStateJSON: `{"instances":[{"terminalID":"term-a"}]}`}
	sessionB := domain.CloudAgentSession{ID: "sess-2", UserID: "user-b", WorkerID: worker.ID, AccountID: accountB.ID}
	if err := store.UpsertCloudAgentSession(ctx, sessionA); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, sessionB); err != nil {
		t.Fatal(err)
	}
	accounts, err := store.ListCloudAgentAccounts(ctx, "user-a", worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Credential != "token-a" {
		t.Fatalf("unexpected user-a accounts: %+v", accounts)
	}
	sessions, err := store.ListCloudAgentSessions(ctx, "user-a", worker.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != sessionA.ID {
		t.Fatalf("unexpected user-a sessions: %+v", sessions)
	}
	storedAccount, err := store.GetCloudAgentAccount(ctx, "user-a", accountA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedAccount.Credential != accountA.Credential {
		t.Fatalf("unexpected credential: %q", storedAccount.Credential)
	}
	storedSession, err := store.GetCloudAgentSession(ctx, "user-a", sessionA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSession.ShellStateJSON != sessionA.ShellStateJSON {
		t.Fatalf("unexpected shell state json: %q", storedSession.ShellStateJSON)
	}
}

func mustGeneratePrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}
