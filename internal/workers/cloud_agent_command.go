package workers

import (
	"context"
	"fmt"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

func RunCloudAgentCommand(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, script string) (string, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return "", fmt.Errorf("cloud agent command script is required")
	}
	result, err := RunCloudAgentShellExec(ctx, worker, key, account, cloudSession, CloudAgentShellExecRequest{
		Mode:   "bash",
		Script: script,
	})
	if err != nil {
		return "", err
	}
	if result.TimedOut {
		return result.Stdout, fmt.Errorf("run cloud agent command: timed out")
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			return result.Stdout, fmt.Errorf("run cloud agent command: exit status %d", result.ExitCode)
		}
		return result.Stdout, fmt.Errorf("run cloud agent command: exit status %d: %s", result.ExitCode, detail)
	}
	return result.Stdout, nil
}
