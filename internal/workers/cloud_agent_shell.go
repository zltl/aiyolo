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
	shellSessionID := domain.CloudAgentSessionID(account.UserID, firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(cloudSession.ID)))

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
	if err := session.Start(buildCloudAgentShellCommand(target.containerName, target.workspacePath, shellSessionID, account.ModelPublicName, target.account.Credential)); err != nil {
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
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			_ = shell.Close()
		}()
	}
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

func buildCloudAgentShellCommand(containerName, workspacePath, shellSessionID, model string, apiKey string) string {
	innerScript := `set -euo pipefail

export TERM="${TERM:-xterm-256color}"
export COLORTERM="${COLORTERM:-truecolor}"
export CLICOLOR=1
export CLICOLOR_FORCE=1
export FORCE_COLOR=1
export npm_config_color=always
export GCC_COLORS="${GCC_COLORS:-error=01;31:warning=01;35:note=01;36:caret=01;32:locus=01:quote=01}"

if ! command -v bash >/dev/null 2>&1; then
	printf 'bash is not installed in this container\n' >&2
	exit 127
fi

aiyolo_shell_rc_dir="${HOME:-/tmp}/.cache/aiyolo"
mkdir -p "$aiyolo_shell_rc_dir" 2>/dev/null || aiyolo_shell_rc_dir="${TMPDIR:-/tmp}"
aiyolo_shell_rc="$aiyolo_shell_rc_dir/chat-shell-bashrc"
cat >"$aiyolo_shell_rc" <<'AIYOLO_BASHRC'
force_color_prompt=yes

export TERM="${TERM:-xterm-256color}"
export COLORTERM="${COLORTERM:-truecolor}"
export CLICOLOR="${CLICOLOR:-1}"
export CLICOLOR_FORCE="${CLICOLOR_FORCE:-1}"
export FORCE_COLOR="${FORCE_COLOR:-1}"
export npm_config_color="${npm_config_color:-always}"
export GCC_COLORS="${GCC_COLORS:-error=01;31:warning=01;35:note=01;36:caret=01;32:locus=01:quote=01}"

if [[ -r "$HOME/.bashrc" ]]; then
  . "$HOME/.bashrc"
fi

if command -v dircolors >/dev/null 2>&1; then
  eval "$(dircolors -b 2>/dev/null || true)"
fi

alias ls='ls --color=auto'
alias ll='ls -alF --color=auto'
alias la='ls -A --color=auto'
alias l='ls -CF --color=auto'
alias grep='grep --color=auto'
alias diff='diff --color=auto'

__aiyolo_report_cwd() {
	local __aiyolo_cwd_b64
	__aiyolo_cwd_b64="$(printf '%s' "$PWD" | base64 2>/dev/null | tr -d '\n' 2>/dev/null || true)"
	if [[ -n "$__aiyolo_cwd_b64" ]]; then
		printf '\033]6973;AiyoloCwd=%s\007' "$__aiyolo_cwd_b64"
	fi
}

if declare -p PROMPT_COMMAND 2>/dev/null | grep -q '^declare -[aA]'; then
	PROMPT_COMMAND=(__aiyolo_report_cwd "${PROMPT_COMMAND[@]}")
elif [[ "${PROMPT_COMMAND:-}" != *"__aiyolo_report_cwd"* ]]; then
	PROMPT_COMMAND="__aiyolo_report_cwd${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
fi

case "${PS1:-}" in
  *"\\e["*|*"\\033["*) ;;
  *) PS1='\[\e[01;32m\]\u@\h\[\e[00m\]:\[\e[01;34m\]\w\[\e[00m\]\$ ' ;;
esac
AIYOLO_BASHRC

exec bash --rcfile "$aiyolo_shell_rc" -i
`
	script := fmt.Sprintf(`container_name=%s
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
exec docker exec -it \
	-u %s \
  -w "$workspace_path" \
	-e HOME=%s \
	-e USER=%s \
	"${credential_env_args[@]}" \
  -e TERM=xterm-256color \
  -e COLORTERM=truecolor \
	-e CLICOLOR=1 \
	-e CLICOLOR_FORCE=1 \
	-e FORCE_COLOR=1 \
	-e npm_config_color=always \
  -e SHELL=/bin/bash \
  -e LANG=C.UTF-8 \
  -e LC_ALL=C.UTF-8 \
  "$container_name" \
	bash -lc %s
`, shellQuote(containerName), shellQuote(workspacePath), shellQuote(strings.TrimSpace(apiKey)), shellQuote(defaultCloudAgentUser), shellQuote(defaultCloudAgentHome), shellQuote(defaultCloudAgentUser), shellQuote(innerScript))
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
