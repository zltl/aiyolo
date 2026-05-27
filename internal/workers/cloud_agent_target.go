package workers

import (
	"fmt"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

type cloudAgentTarget struct {
	worker        domain.WorkerServer
	key           domain.WorkerSSHKey
	account       domain.CloudAgentAccount
	cloudSession  domain.CloudAgentSession
	containerName string
	workspacePath string
}

func resolveCloudAgentTarget(worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession) (cloudAgentTarget, error) {
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return cloudAgentTarget{}, err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return cloudAgentTarget{}, err
	}
	account, err = domain.NormalizeCloudAgentAccount(account)
	if err != nil {
		return cloudAgentTarget{}, err
	}
	cloudSession, err = domain.NormalizeCloudAgentSession(cloudSession)
	if err != nil {
		return cloudAgentTarget{}, err
	}
	if account.ID != cloudSession.AccountID {
		return cloudAgentTarget{}, fmt.Errorf("cloud agent account %s does not match session %s", account.ID, cloudSession.ID)
	}
	if account.WorkerID != cloudSession.WorkerID {
		return cloudAgentTarget{}, fmt.Errorf("cloud agent account %s does not belong to worker %s", account.ID, cloudSession.WorkerID)
	}
	containerName := strings.TrimSpace(account.ContainerName)
	if containerName == "" {
		return cloudAgentTarget{}, fmt.Errorf("cloud agent container name is required")
	}
	workspacePath := firstNonEmpty(cloudSession.WorkspacePath, account.WorkspacePath, domain.DefaultCloudAgentWorkspacePath)
	return cloudAgentTarget{
		worker:        worker,
		key:           key,
		account:       account,
		cloudSession:  cloudSession,
		containerName: containerName,
		workspacePath: workspacePath,
	}, nil
}
