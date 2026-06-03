package workers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zltl/aiyolo/internal/domain"
)

type CloudAgentCodexOptions struct {
	ThreadID         string
	Prompt           string
	InitialPrompt    string
	Model            string
	WorkingDirectory string
	Stream           bool
}

type cloudAgentChunkWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	onChunk func([]byte) error
	err     error
}

func (writer *cloudAgentChunkWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if _, err := writer.buffer.Write(payload); err != nil {
		return 0, err
	}
	if writer.err != nil || writer.onChunk == nil || len(payload) == 0 {
		return len(payload), writer.err
	}
	copied := append([]byte(nil), payload...)
	if err := writer.onChunk(copied); err != nil {
		writer.err = err
		return 0, err
	}
	return len(payload), nil
}

func (writer *cloudAgentChunkWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func (writer *cloudAgentChunkWriter) Err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.err
}

func RunCloudAgentCodex(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, options CloudAgentCodexOptions, onOutput func([]byte) error) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	target, err := resolveCloudAgentTarget(worker, key, account, cloudSession)
	if err != nil {
		return "", err
	}
	workingDirectory := normalizeCloudAgentWorkingDirectory(options.WorkingDirectory)
	if workingDirectory == "" {
		workingDirectory = target.workspacePath
	}
	options.ThreadID = strings.TrimSpace(options.ThreadID)
	options.Prompt = strings.TrimSpace(options.Prompt)
	options.InitialPrompt = strings.TrimSpace(options.InitialPrompt)
	if options.Prompt == "" && options.InitialPrompt == "" {
		return "", fmt.Errorf("cloud agent codex prompt is required")
	}
	if options.InitialPrompt == "" {
		options.InitialPrompt = options.Prompt
	}
	options.Model = strings.TrimSpace(options.Model)

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

	stdoutWriter := &cloudAgentChunkWriter{onChunk: onOutput}
	var stderr bytes.Buffer
	session.Stdout = stdoutWriter
	session.Stderr = &stderr
	session.Stdin = strings.NewReader(buildCloudAgentCodexRemoteScript(target.containerName, workingDirectory, target.account.Credential, options))
	if err := session.Start("bash -s --"); err != nil {
		return "", fmt.Errorf("start cloud agent codex: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = session.Close()
		_ = client.Close()
	}()

	waitErr := session.Wait()
	if writeErr := stdoutWriter.Err(); writeErr != nil && waitErr == nil {
		waitErr = writeErr
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdoutWriter.String())
		}
		if detail == "" {
			return stdoutWriter.String(), fmt.Errorf("run cloud agent codex: %w", waitErr)
		}
		return stdoutWriter.String(), fmt.Errorf("run cloud agent codex: %w: %s", waitErr, detail)
	}
	return stdoutWriter.String(), nil
}

func normalizeCloudAgentWorkingDirectory(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, "\x00\r\n") || !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	return trimmed
}

func buildCloudAgentCodexRemoteScript(containerName, workspacePath string, apiKey string, options CloudAgentCodexOptions) string {
	return fmt.Sprintf(`set -euo pipefail

container_name=%s
workspace_path=%s
api_key=%s
if ! command -v docker >/dev/null 2>&1; then
  printf 'docker is not installed on this worker\n' >&2
  exit 127
fi
if ! docker inspect --type container "$container_name" >/dev/null 2>&1; then
  printf 'cloud agent container %%s is not available\n' "$container_name" >&2
  exit 1
fi
credential_env_args=()
if [[ -n "$api_key" ]]; then
	credential_env_args=(
		-e "AIYOLO_API_KEY=$api_key"
		-e "OPENAI_API_KEY=$api_key"
		-e "CODEX_API_KEY=$api_key"
		-e "ANTHROPIC_API_KEY=$api_key"
	)
fi

docker exec -i \
  -u %s \
  -w "$workspace_path" \
  -e HOME=%s \
  -e USER=%s \
	-e CODEX_HOME=%s \
	"${credential_env_args[@]}" \
  -e TERM=xterm-256color \
  -e COLORTERM=truecolor \
  -e SHELL=/bin/bash \
  -e LANG=C.UTF-8 \
  -e LC_ALL=C.UTF-8 \
  "$container_name" \
	bash -s -- <<'CONTAINER_CODEX'
set -euo pipefail

thread_id=%s
prompt=%s
initial_prompt=%s
model=%s

mkdir -p "${CODEX_HOME:-$HOME/.codex}"
prompt_to_send="$initial_prompt"
cmd=(codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox)
if [[ -n "${OPENAI_BASE_URL:-}" ]]; then
  cmd+=(-c "openai_base_url=${OPENAI_BASE_URL}")
fi
if [[ -n "$model" ]]; then
  cmd+=(-m "$model")
fi
if [[ -n "$thread_id" ]]; then
  prompt_to_send="$prompt"
  cmd+=(resume "$thread_id" "$prompt_to_send")
else
  cmd+=("$prompt_to_send")
fi
"${cmd[@]}"
CONTAINER_CODEX
`, shellQuote(containerName), shellQuote(workspacePath), shellQuote(strings.TrimSpace(apiKey)), shellQuote(defaultCloudAgentUser), shellQuote(defaultCloudAgentHome), shellQuote(defaultCloudAgentUser), shellQuote(defaultCloudAgentCodexHome), shellQuote(options.ThreadID), shellQuote(options.Prompt), shellQuote(options.InitialPrompt), shellQuote(options.Model))
}
