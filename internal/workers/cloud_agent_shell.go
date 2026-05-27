package workers

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/zltl/aiyolo/internal/domain"
)

const (
	defaultCloudAgentShellColumns = 120
	defaultCloudAgentShellRows    = 32
)

type InteractiveShell interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type interactiveSSHCloudAgentShell struct {
	client     *ssh.Client
	session    *ssh.Session
	stdin      io.WriteCloser
	output     *io.PipeReader
	outputSink *io.PipeWriter
	closeOnce  sync.Once
}

type synchronizedPipeWriter struct {
	mu     sync.Mutex
	writer *io.PipeWriter
}

func OpenCloudAgentShell(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, cols, rows int) (InteractiveShell, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	target, err := resolveCloudAgentTarget(worker, key, account, cloudSession)
	if err != nil {
		return nil, err
	}
	cols, rows = normalizeCloudAgentShellSize(cols, rows)

	client, err := dialSSH(target.worker, target.key)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		client.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	claudeSessionID := domain.CloudAgentClaudeSessionID(account.UserID, firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(cloudSession.ID)))

	stdoutReader, stdoutWriter := io.Pipe()
	stdoutSink := &synchronizedPipeWriter{writer: stdoutWriter}
	session.Stdout = stdoutSink
	session.Stderr = stdoutSink
	stdin, err := session.StdinPipe()
	if err != nil {
		stdoutReader.Close()
		stdoutWriter.Close()
		session.Close()
		client.Close()
		return nil, fmt.Errorf("open stdin pipe: %w", err)
	}
	if err := session.Start(buildCloudAgentShellCommand(target.containerName, target.workspacePath, claudeSessionID, account.ModelPublicName)); err != nil {
		stdin.Close()
		stdoutReader.Close()
		stdoutWriter.Close()
		session.Close()
		client.Close()
		return nil, fmt.Errorf("start cloud agent shell: %w", err)
	}

	shell := &interactiveSSHCloudAgentShell{
		client:     client,
		session:    session,
		stdin:      stdin,
		output:     stdoutReader,
		outputSink: stdoutWriter,
	}
	go func() {
		if waitErr := session.Wait(); waitErr != nil {
			_ = stdoutWriter.CloseWithError(waitErr)
			return
		}
		_ = stdoutWriter.Close()
	}()
	go func() {
		<-ctx.Done()
		_ = shell.Close()
	}()
	return shell, nil
}

func normalizeCloudAgentShellSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = defaultCloudAgentShellColumns
	}
	if rows <= 0 {
		rows = defaultCloudAgentShellRows
	}
	return cols, rows
}

func buildCloudAgentShellCommand(containerName, workspacePath, claudeSessionID, model string) string {
	innerScript := fmt.Sprintf(`set -euo pipefail

if ! command -v claude >/dev/null 2>&1; then
	printf 'claude is not installed in this container\n' >&2
	exit 127
fi

session_id=%s
model=%s
project_key="$(python3 - <<'PY'
import os
import re

print(re.sub(r'[^0-9A-Za-z]', '-', os.getcwd()))
PY
)"
session_file="$HOME/.claude/projects/$project_key/${session_id}.jsonl"
cmd=(claude --dangerously-skip-permissions)
if [[ -n "$model" ]]; then
	cmd+=(--model "$model")
fi
if [[ -f "$session_file" ]]; then
	cmd+=(--resume "$session_id")
else
	cmd+=(--session-id "$session_id")
fi
exec "${cmd[@]}"
`, shellQuote(strings.TrimSpace(claudeSessionID)), shellQuote(strings.TrimSpace(model)))
	script := fmt.Sprintf(`container_name=%s
workspace_path=%s
if ! command -v docker >/dev/null 2>&1; then
  printf 'docker is not installed on this worker\n' >&2
  exit 127
fi
if ! docker inspect --type container "$container_name" >/dev/null 2>&1; then
	printf 'cloud agent container %%s is not available\n' "$container_name" >&2
  exit 1
fi
exec docker exec -it \
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
	bash -lc %s
`, shellQuote(containerName), shellQuote(workspacePath), shellQuote(defaultCloudAgentClaudeUser), shellQuote(defaultCloudAgentClaudeHome), shellQuote(defaultCloudAgentClaudeUser), shellQuote(innerScript))
	return bashCommand(script)
}

func (writer *synchronizedPipeWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(payload)
}

func (shell *interactiveSSHCloudAgentShell) Read(payload []byte) (int, error) {
	return shell.output.Read(payload)
}

func (shell *interactiveSSHCloudAgentShell) Write(payload []byte) (int, error) {
	return shell.stdin.Write(payload)
}

func (shell *interactiveSSHCloudAgentShell) Resize(cols, rows int) error {
	cols, rows = normalizeCloudAgentShellSize(cols, rows)
	return shell.session.WindowChange(rows, cols)
}

func (shell *interactiveSSHCloudAgentShell) Close() error {
	var closeErr error
	shell.closeOnce.Do(func() {
		if shell.stdin != nil {
			_ = shell.stdin.Close()
		}
		if shell.outputSink != nil {
			_ = shell.outputSink.Close()
		}
		if shell.session != nil {
			if err := shell.session.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if shell.client != nil {
			if err := shell.client.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if shell.output != nil {
			_ = shell.output.Close()
		}
	})
	return closeErr
}
