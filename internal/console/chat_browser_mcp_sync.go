package console

import (
	"context"

	"github.com/zltl/aiyolo/internal/domain"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

func (handler *Handler) syncConsoleChatBrowserMCPConfig(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, baseURL, userID, chatSessionID string) error {
	enabled, err := handler.consoleChatBrowserMCPEnabled(ctx, userID, chatSessionID)
	if err != nil || !enabled {
		return err
	}
	mcpURL := consoleBrowserMCPURL(baseURL, chatSessionID)
	token := handler.issueBrowserMCPToken(userID, chatSessionID)
	script := workerops.BuildCloudAgentBrowserMCPConfigShell(workerops.CloudAgentBrowserMCPConfig{
		URL:   mcpURL,
		Token: token,
	})
	if script == "" {
		return nil
	}
	_, err = handler.runCloudAgentCommand(ctx, worker, key, account, cloudSession, script)
	return err
}

func (handler *Handler) clearConsoleChatBrowserMCPConfig(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession) error {
	_, err := handler.runCloudAgentCommand(ctx, worker, key, account, cloudSession, workerops.BuildCloudAgentBrowserMCPClearShell())
	return err
}
