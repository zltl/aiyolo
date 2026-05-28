package ass

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSTreeReadAndWriteFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodGet, "/v1/fs/tree", "")
	if response.Code != http.StatusOK {
		t.Fatalf("tree status=%d body=%s", response.Code, response.Body.String())
	}
	var tree struct {
		Status string `json:"status"`
		Data   struct {
			Entries []fsEntry `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Status != "ok" || len(tree.Data.Entries) != 2 || tree.Data.Entries[0].Name != "cmd" || tree.Data.Entries[1].Name != "README.md" {
		t.Fatalf("unexpected tree response: %+v", tree)
	}

	response = performRequest(server, http.MethodGet, "/v1/fs/file?path=README.md", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
	var read struct {
		Data fsFileData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Data.Kind != "text" || read.Data.Content != "hello\n" || !strings.HasPrefix(read.Data.Revision, "sha256:") {
		t.Fatalf("unexpected read response: %+v", read.Data)
	}

	body := `{"path":"README.md","content":"updated\n","expected_revision":"` + read.Data.Revision + `"}`
	response = performRequest(server, http.MethodPut, "/v1/fs/file", body)
	if response.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
	if content, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(content) != "updated\n" {
		t.Fatalf("file was not updated content=%q err=%v", content, err)
	}
}

func TestWriteFileRejectsStaleRevision(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodPut, "/v1/fs/file", `{"path":"README.md","content":"updated\n","expected_revision":"sha256:stale"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error responseError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "conflict" {
		t.Fatalf("unexpected error: %+v", payload.Error)
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodGet, "/v1/fs/file?path=../README.md", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error responseError `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "path_invalid" {
		t.Fatalf("unexpected error: %+v", payload.Error)
	}
}

func TestReadFileRejectsBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), []byte{'a', 0, 'b'}, 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodGet, "/v1/fs/file?path=blob.bin", "")
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReadFileReturnsImagePreview(t *testing.T) {
	root := t.TempDir()
	payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	if err := os.WriteFile(filepath.Join(root, "diagram.png"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodGet, "/v1/fs/file?path=diagram.png", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
	var read struct {
		Data fsFileData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Data.Kind != "image" || read.Data.MediaType != "image/png" {
		t.Fatalf("unexpected image response: %+v", read.Data)
	}
	if !strings.HasPrefix(read.Data.PreviewURL, "data:image/png;base64,") {
		t.Fatalf("unexpected preview url: %+v", read.Data)
	}
	if read.Data.Content != "" {
		t.Fatalf("image preview should not include text content: %+v", read.Data)
	}
}

func TestShellExecRunsInWorkspace(t *testing.T) {
	root := t.TempDir()
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodPost, "/v1/shell/exec", `{"mode":"bash","script":"printf '%s' \"$PWD\"","timeout_ms":5000}`)
	if response.Code != http.StatusOK {
		t.Fatalf("exec status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data shellExecData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ExitCode != 0 || payload.Data.Stdout != root {
		t.Fatalf("unexpected exec response: %+v", payload.Data)
	}
}

func newTestServer(t *testing.T, root string) *Server {
	t.Helper()
	execUser := "root"
	if current, err := user.Current(); err == nil && current.Username != "" {
		execUser = current.Username
	}
	server, err := NewServer(Config{WorkspaceRoot: root, ExecUser: execUser, ExecHome: root})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func performRequest(server *Server, method string, target string, body string) *httptest.ResponseRecorder {
	reader := strings.NewReader(body)
	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
