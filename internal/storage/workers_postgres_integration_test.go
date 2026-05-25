package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

func TestPostgresCloudWorkersSecretRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("AIYOLO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AIYOLO_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store, err := storage.OpenPostgres(ctx, databaseURL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	privateKey := mustGeneratePrivateKeyPEM(t)
	sshKeyID := "ssh-key-pg-" + time.Now().UTC().Format("20060102150405.000000000")
	workerID := "worker-pg-" + time.Now().UTC().Format("20060102150405.000000000")
	accountID := "acct-pg-" + time.Now().UTC().Format("20060102150405.000000000")
	sessionID := "sess-pg-" + time.Now().UTC().Format("20060102150405.000000000")

	if err := store.UpsertWorkerSSHKey(ctx, domain.WorkerSSHKey{ID: sshKeyID, PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkerServer(ctx, domain.WorkerServer{ID: workerID, SSHHost: "10.0.0.9", SSHUsername: "ubuntu", SSHKeyID: sshKeyID}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentAccount(ctx, domain.CloudAgentAccount{ID: accountID, UserID: "pg-user", WorkerID: workerID, Credential: "pg-token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCloudAgentSession(ctx, domain.CloudAgentSession{ID: sessionID, UserID: "pg-user", WorkerID: workerID, AccountID: accountID}); err != nil {
		t.Fatal(err)
	}
	storedKey, err := store.GetWorkerSSHKey(ctx, sshKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if storedKey.PrivateKey != privateKey {
		t.Fatal("expected decrypted private key round-trip")
	}
	storedAccount, err := store.GetCloudAgentAccount(ctx, "pg-user", accountID)
	if err != nil {
		t.Fatal(err)
	}
	if storedAccount.Credential != "pg-token" {
		t.Fatalf("unexpected credential: %q", storedAccount.Credential)
	}
	sessions, err := store.ListCloudAgentSessions(ctx, "pg-user", workerID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 || sessions[0].ID != sessionID {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
}
