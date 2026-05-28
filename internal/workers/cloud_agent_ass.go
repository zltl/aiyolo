package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

const cloudAgentASSSocketPath = "/run/aiyolo/ass.sock"

type CloudAgentWorkspaceEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at"`
	HasChildren bool   `json:"has_children"`
}

type CloudAgentWorkspaceTree struct {
	Path      string                     `json:"path"`
	Entries   []CloudAgentWorkspaceEntry `json:"entries"`
	Truncated bool                       `json:"truncated"`
}

type CloudAgentWorkspaceFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
	Bytes    int64  `json:"bytes"`
}

type CloudAgentShellExecRequest struct {
	Mode           string            `json:"mode"`
	Script         string            `json:"script,omitempty"`
	Argv           []string          `json:"argv,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Stdin          string            `json:"stdin,omitempty"`
	TimeoutMS      int64             `json:"timeout_ms,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
}

type CloudAgentShellExecResult struct {
	Mode      string `json:"mode"`
	CWD       string `json:"cwd"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Duration  int64  `json:"duration_ms"`
}

type cloudAgentASSEnvelope struct {
	Status string              `json:"status"`
	Data   json.RawMessage     `json:"data"`
	Error  *cloudAgentASSError `json:"error"`
}

type cloudAgentASSError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func ListCloudAgentWorkspaceTree(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string) (CloudAgentWorkspaceTree, error) {
	query := url.Values{}
	if strings.TrimSpace(relativePath) != "" {
		query.Set("path", relativePath)
	}
	var result CloudAgentWorkspaceTree
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "GET", "/v1/fs/tree", query, nil, &result); err != nil {
		return CloudAgentWorkspaceTree{}, err
	}
	return result, nil
}

func ReadCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string) (CloudAgentWorkspaceFile, error) {
	query := url.Values{}
	query.Set("path", relativePath)
	var result CloudAgentWorkspaceFile
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "GET", "/v1/fs/file", query, nil, &result); err != nil {
		return CloudAgentWorkspaceFile{}, err
	}
	return result, nil
}

func WriteCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, content string) (CloudAgentWorkspaceFile, error) {
	request := struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}{Path: relativePath, Content: content}
	var result CloudAgentWorkspaceFile
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "PUT", "/v1/fs/file", nil, request, &result); err != nil {
		return CloudAgentWorkspaceFile{}, err
	}
	return result, nil
}

func RunCloudAgentShellExec(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, request CloudAgentShellExecRequest) (CloudAgentShellExecResult, error) {
	var result CloudAgentShellExecResult
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "POST", "/v1/shell/exec", nil, request, &result); err != nil {
		return CloudAgentShellExecResult{}, err
	}
	return result, nil
}

func runCloudAgentASSJSON(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, method string, endpointPath string, query url.Values, body any, data any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	target, err := resolveCloudAgentTarget(worker, key, account, cloudSession)
	if err != nil {
		return err
	}
	endpoint := url.URL{Scheme: "http", Host: "aiyolo-ass", Path: endpointPath}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	var requestBody []byte
	if body != nil {
		requestBody, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	client, err := dialSSH(target.worker, target.key)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if body != nil {
		session.Stdin = bytes.NewReader(requestBody)
	}
	if err := session.Start(buildCloudAgentASSRemoteCommand(target.containerName, method, endpoint.String(), body != nil)); err != nil {
		return fmt.Errorf("start aiyolo-ass request: %w", err)
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
			return fmt.Errorf("call aiyolo-ass: %w", err)
		}
		return fmt.Errorf("call aiyolo-ass: %w: %s", err, detail)
	}
	var envelope cloudAgentASSEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return fmt.Errorf("parse aiyolo-ass response: %w: %s", err, strings.TrimSpace(stdout.String()))
	}
	if envelope.Status != "ok" {
		if envelope.Error != nil {
			if strings.TrimSpace(envelope.Error.Code) != "" {
				return fmt.Errorf("aiyolo-ass %s: %s", envelope.Error.Code, envelope.Error.Message)
			}
			return fmt.Errorf("aiyolo-ass error: %s", envelope.Error.Message)
		}
		return fmt.Errorf("aiyolo-ass returned status %q", envelope.Status)
	}
	if data == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("aiyolo-ass response missing data")
	}
	if err := json.Unmarshal(envelope.Data, data); err != nil {
		return fmt.Errorf("decode aiyolo-ass data: %w", err)
	}
	return nil
}

func buildCloudAgentASSRemoteCommand(containerName, method, endpoint string, hasBody bool) string {
	hasBodyValue := "0"
	if hasBody {
		hasBodyValue = "1"
	}
	return fmt.Sprintf(`set -euo pipefail

container_name=%s
method=%s
endpoint=%s
has_body=%s
socket_path=%s

if ! command -v docker >/dev/null 2>&1; then
  printf 'docker is not installed on this worker\n' >&2
  exit 127
fi
if ! docker inspect --type container "$container_name" >/dev/null 2>&1; then
  printf 'cloud agent container %%s is not available\n' "$container_name" >&2
  exit 1
fi
if ! docker exec "$container_name" test -S "$socket_path" >/dev/null 2>&1; then
  printf 'aiyolo-ass socket %%s is not available in container %%s\n' "$socket_path" "$container_name" >&2
  exit 1
fi

curl_args=(curl -sS --unix-socket "$socket_path" -X "$method")
if [[ "$has_body" == "1" ]]; then
  curl_args+=(-H 'Content-Type: application/json' --data-binary @-)
fi
curl_args+=("$endpoint")
exec docker exec -i "$container_name" "${curl_args[@]}"
`, shellQuote(containerName), shellQuote(method), shellQuote(endpoint), shellQuote(hasBodyValue), shellQuote(cloudAgentASSSocketPath))
}
