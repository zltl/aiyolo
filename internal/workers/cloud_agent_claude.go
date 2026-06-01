package workers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zltl/aiyolo/internal/domain"
)

type CloudAgentClaudeCodeOptions struct {
	SessionID        string
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

func RunCloudAgentClaudeCode(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, options CloudAgentClaudeCodeOptions, onOutput func([]byte) error) (string, error) {
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
	options.SessionID = strings.TrimSpace(options.SessionID)
	if options.SessionID == "" {
		return "", fmt.Errorf("cloud agent claude session id is required")
	}
	options.Prompt = strings.TrimSpace(options.Prompt)
	options.InitialPrompt = strings.TrimSpace(options.InitialPrompt)
	if options.Prompt == "" && options.InitialPrompt == "" {
		return "", fmt.Errorf("cloud agent claude prompt is required")
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
	session.Stdin = strings.NewReader(buildCloudAgentClaudeCodeRemoteScript(target.containerName, workingDirectory, options))
	if err := session.Start("bash -s --"); err != nil {
		return "", fmt.Errorf("start cloud agent claude code: %w", err)
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
			return stdoutWriter.String(), fmt.Errorf("run cloud agent claude code: %w", waitErr)
		}
		return stdoutWriter.String(), fmt.Errorf("run cloud agent claude code: %w: %s", waitErr, detail)
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

func buildCloudAgentClaudeCodeRemoteScript(containerName, workspacePath string, options CloudAgentClaudeCodeOptions) string {
	streamMode := "0"
	if options.Stream {
		streamMode = "1"
	}
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
  -u %s \
  -w "$workspace_path" \
  -e HOME=%s \
  -e USER=%s \
  -e TERM=xterm-256color \
  -e COLORTERM=truecolor \
  -e SHELL=/bin/bash \
  -e LANG=C.UTF-8 \
  -e LC_ALL=C.UTF-8 \
  "$container_name" \
  bash -s -- <<'CONTAINER_CLAUDE'
set -euo pipefail

session_id=%s
prompt=%s
initial_prompt=%s
model=%s
stream_mode=%s

project_key="$(python3 - <<'PY'
import os
import re

print(re.sub(r'[^0-9A-Za-z]', '-', os.getcwd()))
PY
)"
session_file="$HOME/.claude/projects/$project_key/${session_id}.jsonl"
prompt_to_send="$prompt"
session_args=(--session-id "$session_id")
if [[ -f "$session_file" ]]; then
  session_args=(--resume "$session_id")
else
  prompt_to_send="$initial_prompt"
fi

cmd=(claude -p "$prompt_to_send" --dangerously-skip-permissions)
if [[ "$stream_mode" == "1" ]]; then
  cmd+=(--output-format stream-json --verbose --include-partial-messages)
else
  cmd+=(--output-format json)
fi
if [[ -n "$model" ]]; then
  cmd+=(--model "$model")
fi
cmd+=("${session_args[@]}")
"${cmd[@]}"
CONTAINER_CLAUDE
`, shellQuote(containerName), shellQuote(workspacePath), shellQuote(defaultCloudAgentClaudeUser), shellQuote(defaultCloudAgentClaudeHome), shellQuote(defaultCloudAgentClaudeUser), shellQuote(options.SessionID), shellQuote(options.Prompt), shellQuote(options.InitialPrompt), shellQuote(options.Model), shellQuote(streamMode))
}
