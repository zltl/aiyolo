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

func TestFSTreePrefetchesChildDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "server.go"), []byte("package internal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodGet, "/v1/fs/tree?prefetch=children&child_limit=10&prefetch_limit=1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("tree status=%d body=%s", response.Code, response.Body.String())
	}
	var tree struct {
		Status string     `json:"status"`
		Data   fsTreeData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Status != "ok" || len(tree.Data.Entries) != 2 || tree.Data.Entries[0].Name != "cmd" {
		t.Fatalf("unexpected tree response: %+v", tree)
	}
	if len(tree.Data.Children) != 1 {
		t.Fatalf("unexpected prefetched children: %+v", tree.Data.Children)
	}
	cmdEntries := tree.Data.Children["cmd"]
	if len(cmdEntries) != 1 || cmdEntries[0].Path != "cmd/main.go" {
		t.Fatalf("unexpected cmd children: %+v", cmdEntries)
	}
	if _, ok := tree.Data.Children["internal"]; ok {
		t.Fatalf("prefetch limit should cap child directories: %+v", tree.Data.Children)
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

func TestFSCreateFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodPut, "/v1/fs/file", `{"path":"src/new.txt","content":"hello\n","create":true,"mkdir_p":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("create file status=%d body=%s", response.Code, response.Body.String())
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "new.txt")); err != nil || string(content) != "hello\n" {
		t.Fatalf("created file content=%q err=%v", content, err)
	}

	response = performRequest(server, http.MethodPut, "/v1/fs/directory", `{"path":"src/assets/icons","mkdir_p":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("create directory status=%d body=%s", response.Code, response.Body.String())
	}
	if info, err := os.Stat(filepath.Join(root, "src", "assets", "icons")); err != nil || !info.IsDir() {
		t.Fatalf("created directory info=%+v err=%v", info, err)
	}

	response = performRequest(server, http.MethodPut, "/v1/fs/directory", `{"path":"src/assets/icons","mkdir_p":true}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate directory status=%d body=%s", response.Code, response.Body.String())
	}

	uploadBody, err := json.Marshal(fsUploadRequest{Path: "src/assets/blob.bin", Content: []byte{0, 1, 2, 3}, MkdirP: true})
	if err != nil {
		t.Fatal(err)
	}
	response = performRequest(server, http.MethodPut, "/v1/fs/upload", string(uploadBody))
	if response.Code != http.StatusOK {
		t.Fatalf("upload file status=%d body=%s", response.Code, response.Body.String())
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "assets", "blob.bin")); err != nil || string(content) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("uploaded file content=%v err=%v", content, err)
	}
	response = performRequest(server, http.MethodPut, "/v1/fs/upload", string(uploadBody))
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate upload status=%d body=%s", response.Code, response.Body.String())
	}
	overwriteBody, err := json.Marshal(fsUploadRequest{Path: "src/assets/blob.bin", Content: []byte{9, 8, 7}, Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	response = performRequest(server, http.MethodPut, "/v1/fs/upload", string(overwriteBody))
	if response.Code != http.StatusOK {
		t.Fatalf("overwrite upload status=%d body=%s", response.Code, response.Body.String())
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "assets", "blob.bin")); err != nil || string(content) != string([]byte{9, 8, 7}) {
		t.Fatalf("overwritten file content=%v err=%v", content, err)
	}

	response = performRequest(server, http.MethodPost, "/v1/fs/rename", `{"path":"src/assets/blob.bin","new_path":"src/assets/data.bin"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("rename file status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "src", "assets", "blob.bin")); !os.IsNotExist(err) {
		t.Fatalf("old file should be gone after rename err=%v", err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "assets", "data.bin")); err != nil || string(content) != string([]byte{9, 8, 7}) {
		t.Fatalf("renamed file content=%v err=%v", content, err)
	}

	response = performRequest(server, http.MethodPost, "/v1/fs/rename", `{"path":"src/assets","new_path":"src/static"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("rename directory status=%d body=%s", response.Code, response.Body.String())
	}
	if info, err := os.Stat(filepath.Join(root, "src", "static")); err != nil || !info.IsDir() {
		t.Fatalf("renamed directory info=%+v err=%v", info, err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "static", "data.bin")); err != nil || string(content) != string([]byte{9, 8, 7}) {
		t.Fatalf("renamed directory content=%v err=%v", content, err)
	}

	response = performRequest(server, http.MethodPost, "/v1/fs/rename", `{"path":"src/static","new_path":"src/new.txt"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("rename conflict status=%d body=%s", response.Code, response.Body.String())
	}

	response = performRequest(server, http.MethodGet, "/v1/fs/download?path=src/static/data.bin", "")
	if response.Code != http.StatusOK {
		t.Fatalf("download file status=%d body=%s", response.Code, response.Body.String())
	}
	var download struct {
		Status string         `json:"status"`
		Data   fsDownloadData `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &download); err != nil {
		t.Fatal(err)
	}
	if download.Status != "ok" || download.Data.Path != "src/static/data.bin" || download.Data.Name != "data.bin" || string(download.Data.Content) != string([]byte{9, 8, 7}) {
		t.Fatalf("unexpected download response: %+v", download)
	}

	response = performRequest(server, http.MethodPost, "/v1/fs/copy", `{"path":"src/static","new_path":"src/static-copy"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("copy directory status=%d body=%s", response.Code, response.Body.String())
	}
	if content, err := os.ReadFile(filepath.Join(root, "src", "static-copy", "data.bin")); err != nil || string(content) != string([]byte{9, 8, 7}) {
		t.Fatalf("copied directory content=%v err=%v", content, err)
	}
	response = performRequest(server, http.MethodPost, "/v1/fs/copy", `{"path":"src/static","new_path":"src/static-copy"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("copy conflict status=%d body=%s", response.Code, response.Body.String())
	}

	response = performRequest(server, http.MethodDelete, "/v1/fs/path", `{"path":"src/static-copy"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("delete directory status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "src", "static-copy")); !os.IsNotExist(err) {
		t.Fatalf("copied directory should be gone after delete err=%v", err)
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

func TestJobStreamCapturesProcessOutput(t *testing.T) {
	root := t.TempDir()
	server := newTestServer(t, root)

	response := performRequest(server, http.MethodPost, "/v1/jobs", `{"id":"job-output","kind":"test","argv":["/bin/sh","-c","printf 'alpha\\n'; printf 'beta\\n' >&2"]}`)
	if response.Code != http.StatusCreated && response.Code != http.StatusOK {
		t.Fatalf("create job status=%d body=%s", response.Code, response.Body.String())
	}

	response = performRequest(server, http.MethodGet, "/v1/jobs/job-output/stream", "")
	if response.Code != http.StatusOK {
		t.Fatalf("stream job status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "closed pipe") {
		t.Fatalf("job stream should not report a closed pipe: %s", body)
	}
	for _, expected := range []string{"alpha", "beta", `"type":"done"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("job stream missing %q: %s", expected, body)
		}
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
