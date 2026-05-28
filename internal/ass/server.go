package ass

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	ServiceName = "aiyolo-ass"
	Version     = "0.1.0"

	DefaultWorkspaceRoot      = "/workspace"
	DefaultExecUser           = "aiyolo"
	DefaultExecHome           = "/workspace"
	DefaultMaxFileBytes       = int64(512 * 1024)
	DefaultMaxTreeEntries     = 1000
	DefaultMaxExecOutputBytes = int64(1024 * 1024)
	DefaultShellTimeout       = 30 * time.Second
	DefaultShellOutputBytes   = int64(256 * 1024)
)

type Config struct {
	WorkspaceRoot     string
	ExecUser          string
	ExecHome          string
	MaxFileBytes      int64
	MaxTreeEntries    int
	MaxExecOutputByte int64
}

type Server struct {
	workspaceRoot     string
	workspaceRootReal string
	execUser          string
	execHome          string
	maxFileBytes      int64
	maxTreeEntries    int
	maxExecOutputByte int64
}

type envelope struct {
	Status    string          `json:"status"`
	Data      any             `json:"data,omitempty"`
	Error     *responseError  `json:"error,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	RawData   json.RawMessage `json:"-"`
}

type responseError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type apiError struct {
	status    int
	code      string
	message   string
	retryable bool
}

func (err *apiError) Error() string {
	return err.message
}

func ConfigFromEnv() Config {
	return Config{
		WorkspaceRoot:     envString("AIYOLO_ASS_WORKSPACE_ROOT", DefaultWorkspaceRoot),
		ExecUser:          envString("AIYOLO_ASS_USER", DefaultExecUser),
		ExecHome:          envString("AIYOLO_ASS_HOME", DefaultExecHome),
		MaxFileBytes:      envInt64("AIYOLO_ASS_MAX_FILE_BYTES", DefaultMaxFileBytes),
		MaxTreeEntries:    envInt("AIYOLO_ASS_MAX_TREE_ENTRIES", DefaultMaxTreeEntries),
		MaxExecOutputByte: envInt64("AIYOLO_ASS_MAX_EXEC_OUTPUT_BYTES", DefaultMaxExecOutputBytes),
	}
}

func NewServer(cfg Config) (*Server, error) {
	workspaceRoot := strings.TrimSpace(cfg.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = DefaultWorkspaceRoot
	}
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	rootReal := absRoot
	if evaluated, err := filepath.EvalSymlinks(absRoot); err == nil {
		rootReal = evaluated
	}
	execUser := strings.TrimSpace(cfg.ExecUser)
	if execUser == "" {
		execUser = DefaultExecUser
	}
	execHome := strings.TrimSpace(cfg.ExecHome)
	if execHome == "" {
		execHome = DefaultExecHome
	}
	maxFileBytes := cfg.MaxFileBytes
	if maxFileBytes <= 0 {
		maxFileBytes = DefaultMaxFileBytes
	}
	maxTreeEntries := cfg.MaxTreeEntries
	if maxTreeEntries <= 0 {
		maxTreeEntries = DefaultMaxTreeEntries
	}
	maxExecOutputBytes := cfg.MaxExecOutputByte
	if maxExecOutputBytes <= 0 {
		maxExecOutputBytes = DefaultMaxExecOutputBytes
	}
	return &Server{
		workspaceRoot:     absRoot,
		workspaceRootReal: rootReal,
		execUser:          execUser,
		execHome:          execHome,
		maxFileBytes:      maxFileBytes,
		maxTreeEntries:    maxTreeEntries,
		maxExecOutputByte: maxExecOutputBytes,
	}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", server.handleHealthz)
	mux.HandleFunc("GET /v1/fs/tree", server.handleFSTree)
	mux.HandleFunc("GET /v1/fs/file", server.handleReadFile)
	mux.HandleFunc("PUT /v1/fs/file", server.handleWriteFile)
	mux.HandleFunc("POST /v1/shell/exec", server.handleShellExec)
	return mux
}

func (server *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if info, err := os.Stat(server.workspaceRootReal); err != nil || !info.IsDir() {
		server.writeError(w, r, newAPIError(http.StatusInternalServerError, "internal_error", "workspace root is not available"))
		return
	}
	server.writeOK(w, r, map[string]string{
		"service":        ServiceName,
		"version":        Version,
		"workspace_root": server.workspaceRootReal,
	})
}

func (server *Server) handleFSTree(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			server.writeError(w, r, newAPIError(http.StatusBadRequest, "path_invalid", "tree limit must be a positive integer"))
			return
		}
		limit = parsed
	}
	if limit > server.maxTreeEntries {
		limit = server.maxTreeEntries
	}
	includeHidden := parseBool(r.URL.Query().Get("include_hidden"))
	relativePath, realPath, info, apiErr := server.resolveExisting(r.URL.Query().Get("path"), true)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	if !info.IsDir() {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "path_not_directory", "workspace path is not a directory"))
		return
	}
	entries, truncated, apiErr := server.listDirectory(realPath, relativePath, limit, includeHidden)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, fsTreeData{Path: relativePath, Entries: entries, Truncated: truncated})
}

func (server *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	relativePath, realPath, info, apiErr := server.resolveExisting(r.URL.Query().Get("path"), false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	if !info.Mode().IsRegular() {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "path_not_file", "workspace path is not a file"))
		return
	}
	payload, apiErr := server.readTextFile(realPath, info)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, fsFileData{
		Path:     relativePath,
		Size:     int64(len(payload)),
		Revision: revision(payload),
		Content:  string(payload),
	})
}

func (server *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	var request fsWriteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, server.maxFileBytes+1024*64)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "file write request is invalid"))
		return
	}
	payload := []byte(request.Content)
	if int64(len(payload)) > server.maxFileBytes {
		server.writeError(w, r, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the editable size limit"))
		return
	}
	if bytes.Contains(payload, []byte{0}) {
		server.writeError(w, r, newAPIError(http.StatusUnsupportedMediaType, "file_binary", "workspace file content contains NUL bytes"))
		return
	}
	if !utf8.Valid(payload) {
		server.writeError(w, r, newAPIError(http.StatusUnsupportedMediaType, "encoding_invalid", "workspace file content is not valid UTF-8"))
		return
	}
	relativePath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.writeTextFile(relativePath, payload, strings.TrimSpace(request.ExpectedRevision), request.Create, request.MkdirP)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

func (server *Server) handleShellExec(w http.ResponseWriter, r *http.Request) {
	var request shellExecRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "exec_invalid", "shell exec request is invalid"))
		return
	}
	result, apiErr := server.runShellExec(r.Context(), request)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

type fsEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at"`
	HasChildren bool   `json:"has_children"`
}

type fsTreeData struct {
	Path      string    `json:"path"`
	Entries   []fsEntry `json:"entries"`
	Truncated bool      `json:"truncated"`
}

type fsFileData struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Revision string `json:"revision"`
	Content  string `json:"content"`
}

type fsWriteRequest struct {
	Path             string `json:"path"`
	Content          string `json:"content"`
	ExpectedRevision string `json:"expected_revision"`
	Create           bool   `json:"create"`
	MkdirP           bool   `json:"mkdir_p"`
}

type fsWriteData struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Revision string `json:"revision"`
}

type shellExecRequest struct {
	Mode           string            `json:"mode"`
	Script         string            `json:"script"`
	Argv           []string          `json:"argv"`
	CWD            string            `json:"cwd"`
	Env            map[string]string `json:"env"`
	Stdin          string            `json:"stdin"`
	TimeoutMS      int64             `json:"timeout_ms"`
	MaxOutputBytes int64             `json:"max_output_bytes"`
}

type shellExecData struct {
	Mode      string `json:"mode"`
	CWD       string `json:"cwd"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Duration  int64  `json:"duration_ms"`
}

func (server *Server) listDirectory(realPath string, relativePath string, limit int, includeHidden bool) ([]fsEntry, bool, *apiError) {
	items, err := os.ReadDir(realPath)
	if err != nil {
		return nil, false, newAPIError(http.StatusInternalServerError, "internal_error", "read workspace directory failed")
	}
	entries := make([]fsEntry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		if name == "." || name == ".." || (!includeHidden && strings.HasPrefix(name, ".")) {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			continue
		}
		entryType := ""
		switch {
		case mode.IsDir():
			entryType = "directory"
		case mode.IsRegular():
			entryType = "file"
		default:
			continue
		}
		entryPath := path.Join(relativePath, name)
		if relativePath == "" {
			entryPath = name
		}
		entries = append(entries, fsEntry{
			Name:        name,
			Path:        entryPath,
			Type:        entryType,
			Size:        info.Size(),
			ModifiedAt:  info.ModTime().UTC().Format(time.RFC3339),
			HasChildren: entryType == "directory" && directoryHasChildren(filepath.Join(realPath, name), includeHidden),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		leftType := 1
		if entries[i].Type == "directory" {
			leftType = 0
		}
		rightType := 1
		if entries[j].Type == "directory" {
			rightType = 0
		}
		if leftType != rightType {
			return leftType < rightType
		}
		leftName := strings.ToLower(entries[i].Name)
		rightName := strings.ToLower(entries[j].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		return entries[i].Name < entries[j].Name
	})
	truncated := false
	if len(entries) > limit {
		entries = entries[:limit]
		truncated = true
	}
	return entries, truncated, nil
}

func (server *Server) readTextFile(realPath string, info os.FileInfo) ([]byte, *apiError) {
	if info.Size() > server.maxFileBytes {
		return nil, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the editable size limit")
	}
	payload, err := os.ReadFile(realPath)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error", "read workspace file failed")
	}
	if int64(len(payload)) > server.maxFileBytes {
		return nil, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the editable size limit")
	}
	if bytes.Contains(payload, []byte{0}) {
		return nil, newAPIError(http.StatusUnsupportedMediaType, "file_binary", "workspace file is binary and cannot be edited")
	}
	if !utf8.Valid(payload) {
		return nil, newAPIError(http.StatusUnsupportedMediaType, "encoding_invalid", "workspace file is not valid UTF-8")
	}
	return payload, nil
}

func (server *Server) writeTextFile(relativePath string, payload []byte, expectedRevision string, create bool, mkdirP bool) (fsWriteData, *apiError) {
	realPath := filepath.Join(server.workspaceRootReal, filepath.FromSlash(relativePath))
	info, statErr := os.Stat(realPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace file failed")
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if !create {
			return fsWriteData{}, newAPIError(http.StatusNotFound, "path_not_found", "workspace file does not exist")
		}
		parentReal, apiErr := server.resolveWritableParent(relativePath, mkdirP)
		if apiErr != nil {
			return fsWriteData{}, apiErr
		}
		realPath = filepath.Join(parentReal, filepath.Base(filepath.FromSlash(relativePath)))
		if !server.pathWithinRoot(realPath) {
			return fsWriteData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
		}
		return server.atomicWrite(relativePath, realPath, payload, 0644, nil)
	}
	evaluated, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "resolve workspace file failed")
	}
	if !server.pathWithinRoot(evaluated) {
		return fsWriteData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	info, err = os.Stat(evaluated)
	if err != nil {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace file failed")
	}
	if !info.Mode().IsRegular() {
		return fsWriteData{}, newAPIError(http.StatusBadRequest, "path_not_file", "workspace path is not a file")
	}
	current, apiErr := server.readTextFile(evaluated, info)
	if apiErr != nil {
		return fsWriteData{}, apiErr
	}
	if expectedRevision != "" && expectedRevision != revision(current) {
		return fsWriteData{}, newAPIError(http.StatusConflict, "conflict", "workspace file revision does not match")
	}
	return server.atomicWrite(relativePath, evaluated, payload, info.Mode().Perm(), info)
}

func (server *Server) atomicWrite(relativePath string, realPath string, payload []byte, mode os.FileMode, ownerFrom os.FileInfo) (fsWriteData, *apiError) {
	dir := filepath.Dir(realPath)
	tmpFile, err := os.CreateTemp(dir, ".aiyolo-ass-*")
	if err != nil {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "create temporary workspace file failed")
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "set temporary workspace file mode failed")
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "write temporary workspace file failed")
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "sync temporary workspace file failed")
	}
	if err := tmpFile.Close(); err != nil {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "close temporary workspace file failed")
	}
	server.applyOwnership(tmpName, ownerFrom)
	if err := os.Rename(tmpName, realPath); err != nil {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "replace workspace file failed")
	}
	return fsWriteData{Path: relativePath, Bytes: int64(len(payload)), Revision: revision(payload)}, nil
}

func (server *Server) resolveWritableParent(relativePath string, mkdirP bool) (string, *apiError) {
	parentRelative := path.Dir(relativePath)
	if parentRelative == "." {
		parentRelative = ""
	}
	parentPath := filepath.Join(server.workspaceRootReal, filepath.FromSlash(parentRelative))
	if mkdirP {
		if err := os.MkdirAll(parentPath, 0755); err != nil {
			return "", newAPIError(http.StatusInternalServerError, "internal_error", "create workspace parent directory failed")
		}
	}
	parentReal, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", newAPIError(http.StatusNotFound, "path_not_found", "workspace parent directory does not exist")
		}
		return "", newAPIError(http.StatusInternalServerError, "internal_error", "resolve workspace parent directory failed")
	}
	if !server.pathWithinRoot(parentReal) {
		return "", newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	info, err := os.Stat(parentReal)
	if err != nil {
		return "", newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace parent directory failed")
	}
	if !info.IsDir() {
		return "", newAPIError(http.StatusBadRequest, "path_not_directory", "workspace parent path is not a directory")
	}
	return parentReal, nil
}

func (server *Server) resolveExisting(raw string, allowRoot bool) (string, string, os.FileInfo, *apiError) {
	relativePath, apiErr := server.normalizeWorkspacePath(raw, allowRoot)
	if apiErr != nil {
		return "", "", nil, apiErr
	}
	realPath := filepath.Join(server.workspaceRootReal, filepath.FromSlash(relativePath))
	evaluated, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil, newAPIError(http.StatusNotFound, "path_not_found", "workspace path does not exist")
		}
		return "", "", nil, newAPIError(http.StatusInternalServerError, "internal_error", "resolve workspace path failed")
	}
	if !server.pathWithinRoot(evaluated) {
		return "", "", nil, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	info, err := os.Stat(evaluated)
	if err != nil {
		return "", "", nil, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace path failed")
	}
	return relativePath, evaluated, info, nil
}

func (server *Server) normalizeWorkspacePath(raw string, allowRoot bool) (string, *apiError) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if strings.ContainsRune(raw, '\x00') {
		return "", newAPIError(http.StatusBadRequest, "path_invalid", "workspace path is invalid")
	}
	if raw == "" || raw == "." || raw == "/" {
		if allowRoot {
			return "", nil
		}
		return "", newAPIError(http.StatusBadRequest, "path_invalid", "workspace file path is required")
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if cleaned == "." || cleaned == "" {
		if allowRoot {
			return "", nil
		}
		return "", newAPIError(http.StatusBadRequest, "path_invalid", "workspace file path is required")
	}
	return cleaned, nil
}

func (server *Server) pathWithinRoot(candidate string) bool {
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(server.workspaceRootReal, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (server *Server) runShellExec(ctx context.Context, request shellExecRequest) (shellExecData, *apiError) {
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	var argv []string
	switch mode {
	case "bash":
		if strings.TrimSpace(request.Script) == "" {
			return shellExecData{}, newAPIError(http.StatusBadRequest, "exec_invalid", "bash script is required")
		}
		argv = []string{"/bin/bash", "-lc", request.Script}
	case "argv":
		if len(request.Argv) == 0 || strings.TrimSpace(request.Argv[0]) == "" {
			return shellExecData{}, newAPIError(http.StatusBadRequest, "exec_invalid", "argv command is required")
		}
		argv = append([]string(nil), request.Argv...)
		for _, value := range argv {
			if strings.ContainsRune(value, '\x00') {
				return shellExecData{}, newAPIError(http.StatusBadRequest, "exec_invalid", "argv contains an invalid NUL byte")
			}
		}
	default:
		return shellExecData{}, newAPIError(http.StatusBadRequest, "exec_invalid", "exec mode must be bash or argv")
	}
	relativeCWD, realCWD, info, apiErr := server.resolveExisting(request.CWD, true)
	if apiErr != nil {
		return shellExecData{}, apiErr
	}
	if !info.IsDir() {
		return shellExecData{}, newAPIError(http.StatusBadRequest, "path_not_directory", "exec cwd is not a directory")
	}
	timeout := DefaultShellTimeout
	if request.TimeoutMS > 0 {
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
	}
	maxOutputBytes := request.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = DefaultShellOutputBytes
	}
	if maxOutputBytes > server.maxExecOutputByte {
		maxOutputBytes = server.maxExecOutputByte
	}
	env, apiErr := server.shellEnv(request.Env)
	if apiErr != nil {
		return shellExecData{}, apiErr
	}
	credential, apiErr := server.execCredential()
	if apiErr != nil {
		return shellExecData{}, apiErr
	}

	start := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = realCWD
	cmd.Env = env
	cmd.Stdin = strings.NewReader(request.Stdin)
	limit := newLimitedOutput(maxOutputBytes)
	cmd.Stdout = limit.stdoutWriter()
	cmd.Stderr = limit.stderrWriter()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if credential != nil {
		cmd.SysProcAttr.Credential = credential
	}
	if err := cmd.Start(); err != nil {
		return shellExecData{}, newAPIError(http.StatusBadRequest, "exec_invalid", "start exec command failed: "+err.Error())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	timedOut := false
	select {
	case err = <-done:
	case <-execCtx.Done():
		timedOut = errors.Is(execCtx.Err(), context.DeadlineExceeded)
		killProcessGroup(cmd.Process)
		err = <-done
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return shellExecData{}, newAPIError(http.StatusInternalServerError, "internal_error", "wait exec command failed: "+err.Error())
		}
	}
	stdout, stderr, truncated := limit.result()
	return shellExecData{
		Mode:      mode,
		CWD:       relativeCWD,
		ExitCode:  exitCode,
		TimedOut:  timedOut,
		Stdout:    stdout,
		Stderr:    stderr,
		Truncated: truncated,
		Duration:  time.Since(start).Milliseconds(),
	}, nil
}

func (server *Server) shellEnv(extra map[string]string) ([]string, *apiError) {
	protected := map[string]struct{}{"HOME": {}, "USER": {}, "PATH": {}, "SHELL": {}}
	env := append([]string(nil), os.Environ()...)
	env = upsertEnv(env, "HOME", server.execHome)
	env = upsertEnv(env, "USER", server.execUser)
	env = upsertEnv(env, "SHELL", "/bin/bash")
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, newAPIError(http.StatusBadRequest, "exec_invalid", "exec environment contains an invalid variable")
		}
		if _, ok := protected[key]; ok {
			return nil, newAPIError(http.StatusBadRequest, "exec_invalid", "exec environment cannot override "+key)
		}
		env = upsertEnv(env, key, value)
	}
	return env, nil
}

func (server *Server) execCredential() (*syscall.Credential, *apiError) {
	if server.execUser == "" || os.Geteuid() != 0 {
		return nil, nil
	}
	lookup, err := user.Lookup(server.execUser)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "exec_invalid", "exec user is not available")
	}
	uid, err := strconv.ParseUint(lookup.Uid, 10, 32)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "exec_invalid", "exec user uid is invalid")
	}
	gid, err := strconv.ParseUint(lookup.Gid, 10, 32)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "exec_invalid", "exec user gid is invalid")
	}
	groupIDs, _ := lookup.GroupIds()
	groups := make([]uint32, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		parsed, err := strconv.ParseUint(groupID, 10, 32)
		if err == nil {
			groups = append(groups, uint32(parsed))
		}
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups}, nil
}

func (server *Server) applyOwnership(path string, ownerFrom os.FileInfo) {
	uid := -1
	gid := -1
	if ownerFrom != nil {
		if stat, ok := ownerFrom.Sys().(*syscall.Stat_t); ok {
			uid = int(stat.Uid)
			gid = int(stat.Gid)
		}
	} else if os.Geteuid() == 0 {
		if lookup, err := user.Lookup(server.execUser); err == nil {
			if parsedUID, err := strconv.Atoi(lookup.Uid); err == nil {
				uid = parsedUID
			}
			if parsedGID, err := strconv.Atoi(lookup.Gid); err == nil {
				gid = parsedGID
			}
		}
	}
	if uid >= 0 || gid >= 0 {
		_ = os.Chown(path, uid, gid)
	}
}

func (server *Server) writeOK(w http.ResponseWriter, r *http.Request, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(envelope{Status: "ok", Data: data, RequestID: requestID(r)})
}

func (server *Server) writeError(w http.ResponseWriter, r *http.Request, err *apiError) {
	if err == nil {
		err = newAPIError(http.StatusInternalServerError, "internal_error", "internal error")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(err.status)
	_ = json.NewEncoder(w).Encode(envelope{
		Status: "error",
		Error: &responseError{
			Code:      err.code,
			Message:   err.message,
			Retryable: err.retryable,
		},
		RequestID: requestID(r),
	})
}

func newAPIError(status int, code string, message string) *apiError {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if code == "" {
		code = "internal_error"
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &apiError{status: status, code: code, message: message}
}

type limitedOutput struct {
	mu        sync.Mutex
	remaining int64
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	truncated bool
}

type limitedStreamWriter struct {
	parent *limitedOutput
	stderr bool
}

func newLimitedOutput(maxBytes int64) *limitedOutput {
	if maxBytes <= 0 {
		maxBytes = DefaultShellOutputBytes
	}
	return &limitedOutput{remaining: maxBytes}
}

func (output *limitedOutput) stdoutWriter() io.Writer {
	return limitedStreamWriter{parent: output}
}

func (output *limitedOutput) stderrWriter() io.Writer {
	return limitedStreamWriter{parent: output, stderr: true}
}

func (writer limitedStreamWriter) Write(payload []byte) (int, error) {
	writer.parent.mu.Lock()
	defer writer.parent.mu.Unlock()
	if writer.parent.remaining <= 0 {
		writer.parent.truncated = true
		return len(payload), nil
	}
	toWrite := payload
	if int64(len(toWrite)) > writer.parent.remaining {
		toWrite = toWrite[:writer.parent.remaining]
		writer.parent.truncated = true
	}
	writer.parent.remaining -= int64(len(toWrite))
	if writer.stderr {
		_, _ = writer.parent.stderr.Write(toWrite)
	} else {
		_, _ = writer.parent.stdout.Write(toWrite)
	}
	return len(payload), nil
}

func (output *limitedOutput) result() (string, string, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.stdout.String(), output.stderr.String(), output.truncated
}

func directoryHasChildren(realPath string, includeHidden bool) bool {
	items, err := os.ReadDir(realPath)
	if err != nil {
		return false
	}
	for _, item := range items {
		name := item.Name()
		if name == "." || name == ".." || (!includeHidden && strings.HasPrefix(name, ".")) {
			continue
		}
		info, err := item.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() || info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func revision(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envString(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func requestID(r *http.Request) string {
	for _, key := range []string{"X-Request-Id", "X-Request-ID", "X-Correlation-Id"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func upsertEnv(env []string, key string, value string) []string {
	prefix := key + "="
	for index, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func killProcessGroup(process *os.Process) {
	if process == nil {
		return
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil {
		_ = process.Kill()
	}
}
