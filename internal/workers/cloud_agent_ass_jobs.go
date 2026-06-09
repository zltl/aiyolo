package workers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

type CloudAgentASSJobInfo struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Active     bool   `json:"active"`
	Done       bool   `json:"done"`
	OutputSize int64  `json:"output_size"`
	ExitCode   int    `json:"exit_code,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

type CloudAgentASSJobStreamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
}

func GetCloudAgentASSJob(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, jobID string) (CloudAgentASSJobInfo, error) {
	jobID = normalizeJobID(jobID)
	if jobID == "" {
		return CloudAgentASSJobInfo{}, fmt.Errorf("cloud agent ass job id is required")
	}
	var result CloudAgentASSJobInfo
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, http.MethodGet, "/v1/jobs/"+jobID, nil, nil, &result); err != nil {
		return CloudAgentASSJobInfo{}, err
	}
	return result, nil
}

func StartCloudAgentASSJob(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, jobID, kind, cwd string, argv []string, env map[string]string, stdin string) (CloudAgentASSJobInfo, error) {
	jobID = normalizeJobID(jobID)
	if jobID == "" || len(argv) == 0 {
		return CloudAgentASSJobInfo{}, fmt.Errorf("cloud agent ass job id and argv are required")
	}
	request := struct {
		ID    string            `json:"id"`
		Kind  string            `json:"kind"`
		Argv  []string          `json:"argv"`
		CWD   string            `json:"cwd,omitempty"`
		Env   map[string]string `json:"env,omitempty"`
		Stdin string            `json:"stdin,omitempty"`
	}{ID: jobID, Kind: kind, Argv: argv, CWD: cwd, Env: env, Stdin: stdin}
	var result CloudAgentASSJobInfo
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, http.MethodPost, "/v1/jobs", nil, request, &result); err != nil {
		return CloudAgentASSJobInfo{}, err
	}
	return result, nil
}

func StreamCloudAgentASSJobLive(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, jobID string, onEvent func(CloudAgentASSJobStreamEvent) error) error {
	jobID = normalizeJobID(jobID)
	if jobID == "" {
		return fmt.Errorf("cloud agent ass job id is required")
	}
	target, err := resolveCloudAgentTarget(worker, key, account, cloudSession)
	if err != nil {
		return err
	}
	sshClient, err := dialSSHStreaming(target.worker, target.key)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			return dialCloudAgentASS(ctx, sshClient, target)
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://aiyolo-ass/v1/jobs/"+jobID+"/stream", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return fmt.Errorf("stream aiyolo-ass job: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("aiyolo-ass job_not_found: job was not found")
	}
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		detail := strings.TrimSpace(string(payload))
		if detail == "" {
			detail = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("aiyolo-ass GET /v1/jobs/%s/stream returned HTTP %d: %s", jobID, response.StatusCode, detail)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event CloudAgentASSJobStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if onEvent != nil {
			if err := onEvent(event); err != nil {
				return err
			}
		}
		if event.Done || event.Type == "done" || event.Type == "error" {
			if event.Type == "error" && strings.TrimSpace(event.Error) != "" {
				return sanitizeCloudAgentPipeInfrastructureError(fmt.Errorf("%s", strings.TrimSpace(event.Error)))
			}
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return sanitizeCloudAgentPipeInfrastructureError(err)
	}
	return nil
}

func waitForCloudAgentASSJobDone(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, jobID string, interval time.Duration) (CloudAgentASSJobInfo, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		info, err := GetCloudAgentASSJob(ctx, worker, key, account, cloudSession, jobID)
		if err != nil {
			return CloudAgentASSJobInfo{}, err
		}
		if info.Done {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return CloudAgentASSJobInfo{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func StreamCloudAgentASSJobWithRecovery(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, jobID string, onEvent func(CloudAgentASSJobStreamEvent) error) error {
	streamErr := StreamCloudAgentASSJobLive(ctx, worker, key, account, cloudSession, jobID, onEvent)
	if streamErr == nil || !cloudAgentPipeInfrastructureError(streamErr) {
		return streamErr
	}
	log.Printf("cloud agent ass job stream interrupted job_id=%s err=%v; waiting for job completion", jobID, streamErr)
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	info, waitErr := waitForCloudAgentASSJobDone(waitCtx, worker, key, account, cloudSession, jobID, 500*time.Millisecond)
	if waitErr != nil {
		return streamErr
	}
	if info.ExitCode != 0 && strings.TrimSpace(info.LastError) != "" {
		return fmt.Errorf("%s", strings.TrimSpace(info.LastError))
	}
	retryErr := StreamCloudAgentASSJobLive(waitCtx, worker, key, account, cloudSession, jobID, onEvent)
	if retryErr == nil {
		return nil
	}
	if cloudAgentPipeInfrastructureError(retryErr) && info.Done && info.ExitCode == 0 {
		return nil
	}
	return retryErr
}

func cloudAgentPipeInfrastructureError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "closed pipe") || strings.Contains(message, "broken pipe")
}

func sanitizeCloudAgentPipeInfrastructureError(err error) error {
	if !cloudAgentPipeInfrastructureError(err) {
		return err
	}
	return fmt.Errorf("aiyolo-ass stream transport interrupted; refresh the cloud agent environment and retry")
}

func cloudAgentASSJobUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "aiyolo-ass endpoint not available") ||
		strings.Contains(message, "connect aiyolo-ass") ||
		strings.Contains(message, "call aiyolo-ass")
}

func CloudAgentASSJobNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "job_not_found") || strings.Contains(message, "job was not found")
}

func CloudAgentASSJobsEndpointUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "aiyolo-ass endpoint not available") && strings.Contains(message, "/v1/jobs")
}

func CloudAgentASSJobResumable(info CloudAgentASSJobInfo, err error) bool {
	if err != nil {
		return false
	}
	return info.Active || info.Done
}

func normalizeJobID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range trimmed {
		if r <= 32 || strings.ContainsRune("/?#&=\\", r) {
			continue
		}
		builder.WriteRune(r)
		if builder.Len() >= 80 {
			break
		}
	}
	return builder.String()
}

func buildCloudAgentCodexASSJobEnv(apiKey string) map[string]string {
	env := map[string]string{
		"HOME":              defaultCloudAgentHome,
		"USER":              defaultCloudAgentUser,
		"CLAUDE_CONFIG_DIR": defaultCloudAgentClaudeConfigDir,
		"TERM":              "xterm-256color",
		"COLORTERM":         "truecolor",
		"SHELL":             "/bin/bash",
		"LANG":              "C.UTF-8",
		"LC_ALL":            "C.UTF-8",
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		env["AIYOLO_API_KEY"] = apiKey
		env["OPENAI_API_KEY"] = apiKey
		env["ANTHROPIC_API_KEY"] = apiKey
	}
	return env
}

func buildCloudAgentCodexASSScript(options CloudAgentCodexOptions) string {
	return fmt.Sprintf(`set -euo pipefail
thread_id=%s
prompt=%s
initial_prompt=%s
model=%s
mkdir -p "${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
prompt_to_send="$initial_prompt"
cmd=(claude -p --output-format stream-json --verbose --dangerously-skip-permissions)
if [[ -n "$model" ]]; then
  cmd+=(--model "$model")
fi
if [[ -n "$thread_id" ]]; then
  prompt_to_send="$prompt"
	if ! "${cmd[@]}" --resume "$thread_id" "$prompt_to_send"; then
		printf '{"type":"system","subtype":"resume_failed","message":"claude session resume failed; starting a new session"}\n'
		"${cmd[@]}" "$prompt_to_send"
	fi
else
	"${cmd[@]}" "$prompt_to_send"
fi
`, shellQuote(options.ThreadID), shellQuote(options.Prompt), shellQuote(options.InitialPrompt), shellQuote(options.Model))
}
