package ass

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
	DefaultMaxUploadBytes     = int64(20 * 1024 * 1024)
	DefaultMaxTreeEntries     = 1000
	DefaultMaxExecOutputBytes = int64(1024 * 1024)
	DefaultShellTimeout       = 30 * time.Second
	DefaultShellOutputBytes   = int64(256 * 1024)
	DefaultTreeLimit          = 200
	DefaultTreeChildLimit     = 80
	DefaultTreePrefetchLimit  = 32
)

type Config struct {
	WorkspaceRoot     string
	ExecUser          string
	ExecHome          string
	MaxFileBytes      int64
	MaxUploadBytes    int64
	MaxTreeEntries    int
	MaxExecOutputByte int64
}

type Server struct {
	workspaceRoot     string
	workspaceRootReal string
	execUser          string
	execHome          string
	maxFileBytes      int64
	maxUploadBytes    int64
	maxTreeEntries    int
	maxExecOutputByte int64
	shellSessions     *shellSessionRegistry
	jobs              *jobRegistry
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
		MaxUploadBytes:    envInt64("AIYOLO_ASS_MAX_UPLOAD_BYTES", DefaultMaxUploadBytes),
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
	maxUploadBytes := cfg.MaxUploadBytes
	if maxUploadBytes <= 0 {
		maxUploadBytes = DefaultMaxUploadBytes
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
		maxUploadBytes:    maxUploadBytes,
		maxTreeEntries:    maxTreeEntries,
		maxExecOutputByte: maxExecOutputBytes,
		shellSessions:     newShellSessionRegistry(),
		jobs:              newJobRegistry(),
	}, nil
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", server.handleHealthz)
	mux.HandleFunc("GET /v1/fs/tree", server.handleFSTree)
	mux.HandleFunc("GET /v1/fs/file", server.handleReadFile)
	mux.HandleFunc("GET /v1/fs/download", server.handleDownloadFile)
	mux.HandleFunc("PUT /v1/fs/file", server.handleWriteFile)
	mux.HandleFunc("PUT /v1/fs/upload", server.handleUploadFile)
	mux.HandleFunc("PUT /v1/fs/directory", server.handleCreateDirectory)
	mux.HandleFunc("POST /v1/fs/copy", server.handleCopyPath)
	mux.HandleFunc("POST /v1/fs/rename", server.handleRenamePath)
	mux.HandleFunc("DELETE /v1/fs/path", server.handleDeletePath)
	mux.HandleFunc("POST /v1/shell/exec", server.handleShellExec)
	mux.HandleFunc("/v1/shell/sessions", server.handleShellSessionsCollection)
	mux.HandleFunc("/v1/shell/sessions/", server.handleShellSessionItem)
	mux.HandleFunc("/v1/jobs", server.handleJobsCollection)
	mux.HandleFunc("/v1/jobs/", server.handleJobItem)
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
	limit, apiErr := parseFSTreeLimit(r.URL.Query().Get("limit"), DefaultTreeLimit, server.maxTreeEntries, "tree limit must be a positive integer")
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	childLimit, apiErr := parseFSTreeLimit(r.URL.Query().Get("child_limit"), DefaultTreeChildLimit, server.maxTreeEntries, "tree child limit must be a positive integer")
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	prefetchLimit, apiErr := parseFSTreeLimit(r.URL.Query().Get("prefetch_limit"), DefaultTreePrefetchLimit, server.maxTreeEntries, "tree prefetch limit must be a positive integer")
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
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
	var children map[string][]fsEntry
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("prefetch")), "children") || parseBool(r.URL.Query().Get("prefetch_children")) {
		children = server.prefetchDirectoryChildren(realPath, entries, childLimit, prefetchLimit, includeHidden)
	}
	server.writeOK(w, r, fsTreeData{Path: relativePath, Entries: entries, Truncated: truncated, Children: children})
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
	payload, apiErr := server.readWorkspaceFile(relativePath, realPath, info)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, payload)
}

func (server *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	relativePath, realPath, info, apiErr := server.resolveExisting(r.URL.Query().Get("path"), false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	if !info.Mode().IsRegular() {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "path_not_file", "workspace path is not a file"))
		return
	}
	payload, apiErr := server.readWorkspaceDownload(relativePath, realPath, info)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, payload)
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

func (server *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	var request fsUploadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, server.maxUploadBytes*2+1024*64)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "file upload request is invalid"))
		return
	}
	if int64(len(request.Content)) > server.maxUploadBytes {
		server.writeError(w, r, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace upload exceeds the size limit"))
		return
	}
	relativePath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.writeBinaryFile(relativePath, request.Content, request.MkdirP, request.Overwrite)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

func (server *Server) handleCreateDirectory(w http.ResponseWriter, r *http.Request) {
	var request fsDirectoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "directory create request is invalid"))
		return
	}
	relativePath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.createDirectory(relativePath, request.MkdirP)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

func (server *Server) handleCopyPath(w http.ResponseWriter, r *http.Request) {
	var request fsCopyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "path copy request is invalid"))
		return
	}
	oldPath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	newPath, apiErr := server.normalizeWorkspacePath(request.NewPath, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.copyPath(oldPath, newPath)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

func (server *Server) handleRenamePath(w http.ResponseWriter, r *http.Request) {
	var request fsRenameRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "path rename request is invalid"))
		return
	}
	oldPath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	newPath, apiErr := server.normalizeWorkspacePath(request.NewPath, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.renamePath(oldPath, newPath)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	server.writeOK(w, r, result)
}

func (server *Server) handleDeletePath(w http.ResponseWriter, r *http.Request) {
	var request fsPathRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "path delete request is invalid"))
		return
	}
	relativePath, apiErr := server.normalizeWorkspacePath(request.Path, false)
	if apiErr != nil {
		server.writeError(w, r, apiErr)
		return
	}
	result, apiErr := server.deletePath(relativePath)
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
	Path      string               `json:"path"`
	Entries   []fsEntry            `json:"entries"`
	Truncated bool                 `json:"truncated"`
	Children  map[string][]fsEntry `json:"children,omitempty"`
}

type fsFileData struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Revision   string `json:"revision"`
	Kind       string `json:"kind,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Content    string `json:"content,omitempty"`
	PreviewURL string `json:"preview_url,omitempty"`
}

type fsDownloadData struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
}

type fsWriteRequest struct {
	Path             string `json:"path"`
	Content          string `json:"content"`
	ExpectedRevision string `json:"expected_revision"`
	Create           bool   `json:"create"`
	MkdirP           bool   `json:"mkdir_p"`
}

type fsUploadRequest struct {
	Path      string `json:"path"`
	Content   []byte `json:"content"`
	MkdirP    bool   `json:"mkdir_p"`
	Overwrite bool   `json:"overwrite"`
}

type fsWriteData struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Revision string `json:"revision"`
}

type fsDirectoryRequest struct {
	Path   string `json:"path"`
	MkdirP bool   `json:"mkdir_p"`
}

type fsDirectoryData struct {
	Path string `json:"path"`
}

type fsRenameRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type fsRenameData struct {
	OldPath string `json:"old_path"`
	Path    string `json:"path"`
}

type fsCopyRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type fsCopyData struct {
	SourcePath string `json:"source_path"`
	Path       string `json:"path"`
}

type fsPathRequest struct {
	Path string `json:"path"`
}

type fsDeleteData struct {
	Path string `json:"path"`
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

func (server *Server) prefetchDirectoryChildren(realPath string, entries []fsEntry, childLimit int, prefetchLimit int, includeHidden bool) map[string][]fsEntry {
	if childLimit <= 0 || prefetchLimit <= 0 || len(entries) == 0 {
		return nil
	}
	children := make(map[string][]fsEntry)
	prefetched := 0
	for _, entry := range entries {
		if prefetched >= prefetchLimit {
			break
		}
		if entry.Type != "directory" || !entry.HasChildren || entry.Path == "" {
			continue
		}
		childRealPath := filepath.Join(realPath, entry.Name)
		childEntries, _, apiErr := server.listDirectory(childRealPath, entry.Path, childLimit, includeHidden)
		if apiErr != nil {
			continue
		}
		children[entry.Path] = childEntries
		prefetched++
	}
	if len(children) == 0 {
		return nil
	}
	return children
}

func (server *Server) readWorkspaceFile(relativePath string, realPath string, info os.FileInfo) (fsFileData, *apiError) {
	if info.Size() > server.maxFileBytes {
		return fsFileData{}, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the editable size limit")
	}
	payload, err := os.ReadFile(realPath)
	if err != nil {
		return fsFileData{}, newAPIError(http.StatusInternalServerError, "internal_error", "read workspace file failed")
	}
	if int64(len(payload)) > server.maxFileBytes {
		return fsFileData{}, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the editable size limit")
	}
	mediaType := detectWorkspaceMediaType(relativePath, payload)
	if strings.HasPrefix(mediaType, "image/") {
		return fsFileData{
			Path:       relativePath,
			Size:       int64(len(payload)),
			Revision:   revision(payload),
			Kind:       "image",
			MediaType:  mediaType,
			PreviewURL: workspacePreviewDataURL(mediaType, payload),
		}, nil
	}
	if bytes.Contains(payload, []byte{0}) {
		return fsFileData{}, newAPIError(http.StatusUnsupportedMediaType, "file_binary", "workspace file is binary and cannot be edited")
	}
	if !utf8.Valid(payload) {
		return fsFileData{}, newAPIError(http.StatusUnsupportedMediaType, "encoding_invalid", "workspace file is not valid UTF-8")
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}
	return fsFileData{
		Path:      relativePath,
		Size:      int64(len(payload)),
		Revision:  revision(payload),
		Kind:      "text",
		MediaType: mediaType,
		Content:   string(payload),
	}, nil
}

func (server *Server) readWorkspaceDownload(relativePath string, realPath string, info os.FileInfo) (fsDownloadData, *apiError) {
	if info.Size() > server.maxUploadBytes {
		return fsDownloadData{}, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the download size limit")
	}
	payload, err := os.ReadFile(realPath)
	if err != nil {
		return fsDownloadData{}, newAPIError(http.StatusInternalServerError, "internal_error", "read workspace file failed")
	}
	if int64(len(payload)) > server.maxUploadBytes {
		return fsDownloadData{}, newAPIError(http.StatusRequestEntityTooLarge, "file_too_large", "workspace file exceeds the download size limit")
	}
	mediaType := detectWorkspaceMediaType(relativePath, payload)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return fsDownloadData{Path: relativePath, Name: path.Base(relativePath), Size: int64(len(payload)), MediaType: mediaType, Content: payload}, nil
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

func detectWorkspaceMediaType(relativePath string, payload []byte) string {
	extType := normalizeMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(relativePath))))
	if extType == "image/svg+xml" {
		return extType
	}
	detected := normalizeMediaType(http.DetectContentType(payload))
	if strings.HasPrefix(detected, "image/") {
		return detected
	}
	if strings.HasPrefix(extType, "image/") {
		return extType
	}
	if extType != "" {
		return extType
	}
	return detected
}

func normalizeMediaType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return mediaType
}

func workspacePreviewDataURL(mediaType string, payload []byte) string {
	if strings.TrimSpace(mediaType) == "" || len(payload) == 0 {
		return ""
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(payload)
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

func (server *Server) writeBinaryFile(relativePath string, payload []byte, mkdirP bool, overwrite bool) (fsWriteData, *apiError) {
	realPath := filepath.Join(server.workspaceRootReal, filepath.FromSlash(relativePath))
	info, statErr := os.Stat(realPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fsWriteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace file failed")
	}
	if errors.Is(statErr, os.ErrNotExist) {
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
	if !overwrite {
		return fsWriteData{}, newAPIError(http.StatusConflict, "path_exists", "workspace file already exists")
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

func (server *Server) createDirectory(relativePath string, mkdirP bool) (fsDirectoryData, *apiError) {
	realPath := filepath.Join(server.workspaceRootReal, filepath.FromSlash(relativePath))
	if info, err := os.Stat(realPath); err == nil {
		if info.IsDir() {
			return fsDirectoryData{}, newAPIError(http.StatusConflict, "path_exists", "workspace directory already exists")
		}
		return fsDirectoryData{}, newAPIError(http.StatusConflict, "path_exists", "workspace path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fsDirectoryData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace directory failed")
	}
	parentReal, apiErr := server.resolveWritableParent(relativePath, mkdirP)
	if apiErr != nil {
		return fsDirectoryData{}, apiErr
	}
	realPath = filepath.Join(parentReal, filepath.Base(filepath.FromSlash(relativePath)))
	if !server.pathWithinRoot(realPath) {
		return fsDirectoryData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	if err := os.Mkdir(realPath, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fsDirectoryData{}, newAPIError(http.StatusConflict, "path_exists", "workspace directory already exists")
		}
		return fsDirectoryData{}, newAPIError(http.StatusInternalServerError, "internal_error", "create workspace directory failed")
	}
	return fsDirectoryData{Path: relativePath}, nil
}

func (server *Server) renamePath(oldRelativePath string, newRelativePath string) (fsRenameData, *apiError) {
	if oldRelativePath == newRelativePath {
		return fsRenameData{OldPath: oldRelativePath, Path: newRelativePath}, nil
	}
	oldParentReal, apiErr := server.resolveWritableParent(oldRelativePath, false)
	if apiErr != nil {
		return fsRenameData{}, apiErr
	}
	oldRealPath := filepath.Join(oldParentReal, filepath.Base(filepath.FromSlash(oldRelativePath)))
	if !server.pathWithinRoot(oldRealPath) {
		return fsRenameData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	oldInfo, err := os.Lstat(oldRealPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fsRenameData{}, newAPIError(http.StatusNotFound, "path_not_found", "workspace path does not exist")
		}
		return fsRenameData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace path failed")
	}
	newParentReal, apiErr := server.resolveWritableParent(newRelativePath, false)
	if apiErr != nil {
		return fsRenameData{}, apiErr
	}
	newRealPath := filepath.Join(newParentReal, filepath.Base(filepath.FromSlash(newRelativePath)))
	if !server.pathWithinRoot(newRealPath) {
		return fsRenameData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	if _, err := os.Lstat(newRealPath); err == nil {
		return fsRenameData{}, newAPIError(http.StatusConflict, "path_exists", "workspace path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fsRenameData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace target path failed")
	}
	if oldInfo.IsDir() {
		if relative, err := filepath.Rel(oldRealPath, newRealPath); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fsRenameData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace directory cannot be moved into itself")
		}
	}
	if err := os.Rename(oldRealPath, newRealPath); err != nil {
		return fsRenameData{}, newAPIError(http.StatusInternalServerError, "internal_error", "rename workspace path failed")
	}
	return fsRenameData{OldPath: oldRelativePath, Path: newRelativePath}, nil
}

func (server *Server) copyPath(oldRelativePath string, newRelativePath string) (fsCopyData, *apiError) {
	if oldRelativePath == newRelativePath {
		return fsCopyData{}, newAPIError(http.StatusConflict, "path_exists", "workspace source and target paths are the same")
	}
	oldRealPath, oldInfo, apiErr := server.resolveMutablePath(oldRelativePath)
	if apiErr != nil {
		return fsCopyData{}, apiErr
	}
	newParentReal, apiErr := server.resolveWritableParent(newRelativePath, false)
	if apiErr != nil {
		return fsCopyData{}, apiErr
	}
	newRealPath := filepath.Join(newParentReal, filepath.Base(filepath.FromSlash(newRelativePath)))
	if !server.pathWithinRoot(newRealPath) {
		return fsCopyData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	if _, err := os.Lstat(newRealPath); err == nil {
		return fsCopyData{}, newAPIError(http.StatusConflict, "path_exists", "workspace path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fsCopyData{}, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace target path failed")
	}
	if oldInfo.IsDir() {
		if relative, err := filepath.Rel(oldRealPath, newRealPath); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fsCopyData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace directory cannot be copied into itself")
		}
		if apiErr := server.copyWorkspaceDirectory(oldRealPath, newRealPath, oldInfo); apiErr != nil {
			return fsCopyData{}, apiErr
		}
		return fsCopyData{SourcePath: oldRelativePath, Path: newRelativePath}, nil
	}
	if !oldInfo.Mode().IsRegular() {
		return fsCopyData{}, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path cannot be copied")
	}
	if apiErr := server.copyWorkspaceFile(oldRealPath, newRealPath, oldInfo); apiErr != nil {
		return fsCopyData{}, apiErr
	}
	return fsCopyData{SourcePath: oldRelativePath, Path: newRelativePath}, nil
}

func (server *Server) deletePath(relativePath string) (fsDeleteData, *apiError) {
	realPath, _, apiErr := server.resolveMutablePath(relativePath)
	if apiErr != nil {
		return fsDeleteData{}, apiErr
	}
	if err := os.RemoveAll(realPath); err != nil {
		return fsDeleteData{}, newAPIError(http.StatusInternalServerError, "internal_error", "delete workspace path failed")
	}
	return fsDeleteData{Path: relativePath}, nil
}

func (server *Server) resolveMutablePath(relativePath string) (string, os.FileInfo, *apiError) {
	parentReal, apiErr := server.resolveWritableParent(relativePath, false)
	if apiErr != nil {
		return "", nil, apiErr
	}
	realPath := filepath.Join(parentReal, filepath.Base(filepath.FromSlash(relativePath)))
	if !server.pathWithinRoot(realPath) {
		return "", nil, newAPIError(http.StatusBadRequest, "path_invalid", "workspace path escapes root")
	}
	info, err := os.Lstat(realPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, newAPIError(http.StatusNotFound, "path_not_found", "workspace path does not exist")
		}
		return "", nil, newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace path failed")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil, newAPIError(http.StatusBadRequest, "path_invalid", "workspace symbolic links cannot be modified")
	}
	return realPath, info, nil
}

func (server *Server) copyWorkspaceDirectory(sourcePath string, targetPath string, sourceInfo os.FileInfo) *apiError {
	if err := os.Mkdir(targetPath, sourceInfo.Mode().Perm()); err != nil {
		if errors.Is(err, os.ErrExist) {
			return newAPIError(http.StatusConflict, "path_exists", "workspace path already exists")
		}
		return newAPIError(http.StatusInternalServerError, "internal_error", "copy workspace directory failed")
	}
	server.applyOwnership(targetPath, sourceInfo)
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return newAPIError(http.StatusInternalServerError, "internal_error", "read workspace directory failed")
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return newAPIError(http.StatusInternalServerError, "internal_error", "inspect workspace path failed")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		sourceChild := filepath.Join(sourcePath, entry.Name())
		targetChild := filepath.Join(targetPath, entry.Name())
		if info.IsDir() {
			if apiErr := server.copyWorkspaceDirectory(sourceChild, targetChild, info); apiErr != nil {
				return apiErr
			}
			continue
		}
		if info.Mode().IsRegular() {
			if apiErr := server.copyWorkspaceFile(sourceChild, targetChild, info); apiErr != nil {
				return apiErr
			}
		}
	}
	return nil
}

func (server *Server) copyWorkspaceFile(sourcePath string, targetPath string, sourceInfo os.FileInfo) *apiError {
	source, err := os.Open(sourcePath)
	if err != nil {
		return newAPIError(http.StatusInternalServerError, "internal_error", "open workspace file failed")
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceInfo.Mode().Perm())
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return newAPIError(http.StatusConflict, "path_exists", "workspace path already exists")
		}
		return newAPIError(http.StatusInternalServerError, "internal_error", "create workspace file failed")
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		_ = os.Remove(targetPath)
		return newAPIError(http.StatusInternalServerError, "internal_error", "copy workspace file failed")
	}
	if err := target.Close(); err != nil {
		_ = os.Remove(targetPath)
		return newAPIError(http.StatusInternalServerError, "internal_error", "close workspace file failed")
	}
	server.applyOwnership(targetPath, sourceInfo)
	return nil
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

func parseFSTreeLimit(raw string, fallback int, max int, message string) (int, *apiError) {
	limit := fallback
	if strings.TrimSpace(raw) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || parsed <= 0 {
			return 0, newAPIError(http.StatusBadRequest, "path_invalid", message)
		}
		limit = parsed
	}
	if max > 0 && limit > max {
		limit = max
	}
	return limit, nil
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
