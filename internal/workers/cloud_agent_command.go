package workers

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

func RunCloudAgentCommand(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, script string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	target, err := resolveCloudAgentTarget(worker, key, account, cloudSession)
	if err != nil {
		return "", err
	}
	script = strings.TrimSpace(script)
	if script == "" {
		return "", fmt.Errorf("cloud agent command script is required")
	}

	client, err := dialSSH(target.worker, target.key)
	if err != nil {
		return "", err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	session.Stdin = strings.NewReader(buildCloudAgentCommandRemoteScript(target.containerName, target.workspacePath, script))
	if err := session.Start("bash -s --"); err != nil {
		return "", fmt.Errorf("start cloud agent command: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = session.Close()
		_ = client.Close()
	}()

	if err := session.Wait(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return stdout.String(), fmt.Errorf("run cloud agent command: %w", err)
		}
		return stdout.String(), fmt.Errorf("run cloud agent command: %w: %s", err, detail)
	}
	return stdout.String(), nil
}

func buildCloudAgentCommandRemoteScript(containerName, workspacePath, script string) string {
	return fmt.Sprintf(`set -euo pipefail

container_name=%s
workspace_path=%s
if ! command -v docker >/dev/null 2>&1; then
  printf 'docker is not installed on this worker\n' >&2
  exit 127
fi
if ! docker inspect --type container "$container_name" >/dev/null 2>&1; then
  printf 'cloud agent container %%s is not available\n' "$container_name" >&2
  exit 1
fi

docker exec -i \
  -w "$workspace_path" \
  -e TERM=xterm-256color \
  -e COLORTERM=truecolor \
  -e SHELL=/bin/bash \
  -e LANG=C.UTF-8 \
  -e LC_ALL=C.UTF-8 \
  "$container_name" \
  bash -s -- <<'CONTAINER_SCRIPT'
set -euo pipefail
%s
CONTAINER_SCRIPT
`, shellQuote(containerName), shellQuote(workspacePath), script)
}