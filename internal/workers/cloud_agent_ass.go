package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

const cloudAgentASSSocketPath = "/run/aiyolo/ass.sock"

const (
	cloudAgentWorkspaceTreeChildLimit    = "80"
	cloudAgentWorkspaceTreePrefetchLimit = "32"
)

type CloudAgentWorkspaceEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at"`
	HasChildren bool   `json:"has_children"`
}

type CloudAgentWorkspaceTree struct {
	Path      string                                `json:"path"`
	Entries   []CloudAgentWorkspaceEntry            `json:"entries"`
	Truncated bool                                  `json:"truncated"`
	Children  map[string][]CloudAgentWorkspaceEntry `json:"children,omitempty"`
}

type CloudAgentWorkspaceFile struct {
	Path       string `json:"path"`
	Name       string `json:"name,omitempty"`
	Size       int64  `json:"size"`
	Revision   string `json:"revision"`
	Kind       string `json:"kind"`
	MediaType  string `json:"media_type,omitempty"`
	Content    string `json:"content"`
	PreviewURL string `json:"preview_url,omitempty"`
	Bytes      int64  `json:"bytes"`
}

type CloudAgentWorkspaceDownload struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
}

type CloudAgentWorkspaceDirectory struct {
	Path string `json:"path"`
}

type CloudAgentWorkspaceRename struct {
	OldPath string `json:"old_path"`
	Path    string `json:"path"`
}

type CloudAgentWorkspaceCopy struct {
	SourcePath string `json:"source_path"`
	Path       string `json:"path"`
}

type CloudAgentWorkspaceDelete struct {
	Path string `json:"path"`
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

type cloudAgentASSJSONRunner func(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, method string, endpointPath string, query url.Values, body any, data any) error

func ListCloudAgentWorkspaceTree(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string) (CloudAgentWorkspaceTree, error) {
	query := url.Values{}
	if strings.TrimSpace(relativePath) != "" {
		query.Set("path", relativePath)
	}
	query.Set("prefetch", "children")
	query.Set("child_limit", cloudAgentWorkspaceTreeChildLimit)
	query.Set("prefetch_limit", cloudAgentWorkspaceTreePrefetchLimit)
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

func DownloadCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string) (CloudAgentWorkspaceDownload, error) {
	query := url.Values{}
	query.Set("path", relativePath)
	var result CloudAgentWorkspaceDownload
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "GET", "/v1/fs/download", query, nil, &result); err != nil {
		return CloudAgentWorkspaceDownload{}, err
	}
	return result, nil
}

func WriteCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, content string) (CloudAgentWorkspaceFile, error) {
	return writeCloudAgentWorkspaceFile(ctx, worker, key, account, cloudSession, relativePath, content, false, false)
}

func CreateCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, content string, mkdirP bool) (CloudAgentWorkspaceFile, error) {
	return writeCloudAgentWorkspaceFile(ctx, worker, key, account, cloudSession, relativePath, content, true, mkdirP)
}

func UploadCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, content []byte, mkdirP bool, overwrite bool) (CloudAgentWorkspaceFile, error) {
	request := struct {
		Path      string `json:"path"`
		Content   []byte `json:"content"`
		MkdirP    bool   `json:"mkdir_p"`
		Overwrite bool   `json:"overwrite"`
	}{Path: relativePath, Content: content, MkdirP: mkdirP, Overwrite: overwrite}
	var result CloudAgentWorkspaceFile
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "PUT", "/v1/fs/upload", nil, request, &result); err != nil {
		return CloudAgentWorkspaceFile{}, err
	}
	return result, nil
}

func writeCloudAgentWorkspaceFile(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, content string, create bool, mkdirP bool) (CloudAgentWorkspaceFile, error) {
	request := struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Create  bool   `json:"create"`
		MkdirP  bool   `json:"mkdir_p"`
	}{Path: relativePath, Content: content, Create: create, MkdirP: mkdirP}
	var result CloudAgentWorkspaceFile
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "PUT", "/v1/fs/file", nil, request, &result); err != nil {
		return CloudAgentWorkspaceFile{}, err
	}
	return result, nil
}

func CreateCloudAgentWorkspaceDirectory(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, mkdirP bool) (CloudAgentWorkspaceDirectory, error) {
	return createCloudAgentWorkspaceDirectory(ctx, worker, key, account, cloudSession, relativePath, mkdirP, runCloudAgentASSJSON)
}

func createCloudAgentWorkspaceDirectory(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, mkdirP bool, runASSJSON cloudAgentASSJSONRunner) (CloudAgentWorkspaceDirectory, error) {
	request := struct {
		Path   string `json:"path"`
		MkdirP bool   `json:"mkdir_p"`
	}{Path: relativePath, MkdirP: mkdirP}
	var result CloudAgentWorkspaceDirectory
	if err := runASSJSON(ctx, worker, key, account, cloudSession, http.MethodPut, "/v1/fs/directory", nil, request, &result); err != nil {
		if !cloudAgentASSMissingEndpoint(err, http.MethodPut, "/v1/fs/directory") {
			return CloudAgentWorkspaceDirectory{}, err
		}
		fallbackResult, fallbackErr := createCloudAgentWorkspaceDirectoryViaShell(ctx, worker, key, account, cloudSession, relativePath, mkdirP, runASSJSON)
		if fallbackErr != nil {
			return CloudAgentWorkspaceDirectory{}, fmt.Errorf("%w; fallback via /v1/shell/exec failed: %v", err, fallbackErr)
		}
		return fallbackResult, nil
	}
	return result, nil
}

func createCloudAgentWorkspaceDirectoryViaShell(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string, mkdirP bool, runASSJSON cloudAgentASSJSONRunner) (CloudAgentWorkspaceDirectory, error) {
	fallbackPath, err := normalizeCloudAgentWorkspaceFallbackPath(relativePath)
	if err != nil {
		return CloudAgentWorkspaceDirectory{}, err
	}
	request := CloudAgentShellExecRequest{
		Mode:           "bash",
		Script:         buildCloudAgentWorkspaceDirectoryFallbackScript(fallbackPath, mkdirP),
		TimeoutMS:      10000,
		MaxOutputBytes: 8192,
	}
	var result CloudAgentShellExecResult
	if err := runASSJSON(ctx, worker, key, account, cloudSession, http.MethodPost, "/v1/shell/exec", nil, request, &result); err != nil {
		return CloudAgentWorkspaceDirectory{}, err
	}
	if result.ExitCode != 0 || result.TimedOut {
		return CloudAgentWorkspaceDirectory{}, cloudAgentWorkspaceDirectoryFallbackExecError(result)
	}
	return CloudAgentWorkspaceDirectory{Path: fallbackPath}, nil
}

func cloudAgentASSMissingEndpoint(err error, method string, endpointPath string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "aiyolo-ass endpoint not available") && strings.Contains(message, strings.ToLower(method+" "+endpointPath))
}

func normalizeCloudAgentWorkspaceFallbackPath(raw string) (string, error) {
	value := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if strings.ContainsRune(value, '\x00') || value == "" || value == "." || value == "/" || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("aiyolo-ass path_invalid: workspace path is invalid")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("aiyolo-ass path_invalid: workspace path escapes root")
	}
	return cleaned, nil
}

func buildCloudAgentWorkspaceDirectoryFallbackScript(relativePath string, mkdirP bool) string {
	mkdirCommand := "mkdir -- \"$target\""
	if mkdirP {
		mkdirCommand = "mkdir -p -- \"$target\""
	}
	return strings.Join([]string{
		"set -u",
		"target=" + cloudAgentShellQuote(relativePath),
		"if [ -z \"$target\" ] || [ \"$target\" = . ] || [ \"$target\" = / ]; then",
		"  printf '%s\\n' 'workspace path is invalid' >&2",
		"  exit 64",
		"fi",
		"if [ -e \"$target\" ] || [ -L \"$target\" ]; then",
		"  printf '%s\\n' 'workspace path already exists' >&2",
		"  exit 17",
		"fi",
		mkdirCommand,
	}, "\n")
}

func cloudAgentShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func cloudAgentWorkspaceDirectoryFallbackExecError(result CloudAgentShellExecResult) error {
	if result.TimedOut {
		return fmt.Errorf("aiyolo-ass exec_timeout: directory create timed out")
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	lowerDetail := strings.ToLower(detail)
	switch {
	case result.ExitCode == 17 || strings.Contains(lowerDetail, "already exists") || strings.Contains(lowerDetail, "file exists"):
		return fmt.Errorf("aiyolo-ass path_exists: workspace path already exists")
	case result.ExitCode == 64:
		return fmt.Errorf("aiyolo-ass path_invalid: workspace path is invalid")
	case strings.Contains(lowerDetail, "no such file or directory"):
		return fmt.Errorf("aiyolo-ass path_not_found: workspace parent directory does not exist")
	}
	if detail == "" {
		detail = "exit code " + strconv.Itoa(result.ExitCode)
	}
	return fmt.Errorf("aiyolo-ass exec_failed: %s", detail)
}

func RenameCloudAgentWorkspacePath(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, oldPath string, newPath string) (CloudAgentWorkspaceRename, error) {
	request := struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}{Path: oldPath, NewPath: newPath}
	var result CloudAgentWorkspaceRename
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "POST", "/v1/fs/rename", nil, request, &result); err != nil {
		return CloudAgentWorkspaceRename{}, err
	}
	return result, nil
}

func CopyCloudAgentWorkspacePath(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, oldPath string, newPath string) (CloudAgentWorkspaceCopy, error) {
	request := struct {
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
	}{Path: oldPath, NewPath: newPath}
	var result CloudAgentWorkspaceCopy
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "POST", "/v1/fs/copy", nil, request, &result); err != nil {
		return CloudAgentWorkspaceCopy{}, err
	}
	return result, nil
}

func DeleteCloudAgentWorkspacePath(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, account domain.CloudAgentAccount, cloudSession domain.CloudAgentSession, relativePath string) (CloudAgentWorkspaceDelete, error) {
	request := struct {
		Path string `json:"path"`
	}{Path: relativePath}
	var result CloudAgentWorkspaceDelete
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "DELETE", "/v1/fs/path", nil, request, &result); err != nil {
		return CloudAgentWorkspaceDelete{}, err
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
	sshClient, err := dialSSH(target.worker, target.key)
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
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(requestBody)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return fmt.Errorf("call aiyolo-ass direct %s: %w", cloudAgentASSWorkerAddress(target), err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read aiyolo-ass response: %w", err)
	}
	return decodeCloudAgentASSResponse(method, endpointPath, response.StatusCode, payload, data)
}

func decodeCloudAgentASSResponse(method string, endpointPath string, statusCode int, payload []byte, data any) error {
	var envelope cloudAgentASSEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		detail := strings.TrimSpace(string(payload))
		if detail == "" {
			detail = http.StatusText(statusCode)
		}
		if statusCode == http.StatusNotFound {
			return fmt.Errorf("aiyolo-ass endpoint not available: %s %s returned HTTP %d: %s", method, endpointPath, statusCode, detail)
		}
		if statusCode != 0 {
			return fmt.Errorf("aiyolo-ass %s %s returned HTTP %d with invalid JSON: %w: %s", method, endpointPath, statusCode, err, detail)
		}
		return fmt.Errorf("parse aiyolo-ass response: %w: %s", err, detail)
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

func dialCloudAgentASS(ctx context.Context, client sshDialer, target cloudAgentTarget) (net.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	resultCh := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := client.Dial("tcp", cloudAgentASSWorkerAddress(target))
		resultCh <- struct {
			conn net.Conn
			err  error
		}{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, fmt.Errorf("connect aiyolo-ass: %w", result.err)
		}
		return result.conn, nil
	}
}

type sshDialer interface {
	Dial(network string, addr string) (net.Conn, error)
}

func cloudAgentASSWorkerAddress(target cloudAgentTarget) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(cloudAgentASSHostPort(target)))
}

func cloudAgentASSHostPort(target cloudAgentTarget) int {
	userID := firstNonEmpty(target.account.UserID, target.cloudSession.UserID)
	return cloudAgentHostPort(userID, target.worker.ID+"-ass", defaultCloudAgentHostASSBasePort)
}
