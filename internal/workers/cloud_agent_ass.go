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
	Size       int64  `json:"size"`
	Revision   string `json:"revision"`
	Kind       string `json:"kind"`
	MediaType  string `json:"media_type,omitempty"`
	Content    string `json:"content"`
	PreviewURL string `json:"preview_url,omitempty"`
	Bytes      int64  `json:"bytes"`
}

type CloudAgentWorkspaceDirectory struct {
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
	request := struct {
		Path   string `json:"path"`
		MkdirP bool   `json:"mkdir_p"`
	}{Path: relativePath, MkdirP: mkdirP}
	var result CloudAgentWorkspaceDirectory
	if err := runCloudAgentASSJSON(ctx, worker, key, account, cloudSession, "PUT", "/v1/fs/directory", nil, request, &result); err != nil {
		return CloudAgentWorkspaceDirectory{}, err
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
	var envelope cloudAgentASSEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("parse aiyolo-ass response: %w: %s", err, strings.TrimSpace(string(payload)))
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
