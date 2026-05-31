package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatWorkspaceTreePath       = "/console/chat/workspace/tree"
	consoleChatWorkspaceFilePath       = "/console/chat/workspace/file"
	consoleChatWorkspaceDownloadPath   = "/console/chat/workspace/download"
	consoleChatWorkspaceUploadPath     = "/console/chat/workspace/upload"
	consoleChatWorkspaceDirectoryPath  = "/console/chat/workspace/directory"
	consoleChatWorkspaceCopyPath       = "/console/chat/workspace/copy"
	consoleChatWorkspaceRenamePath     = "/console/chat/workspace/rename"
	consoleChatWorkspaceDeletePath     = "/console/chat/workspace/path"
	consoleChatWorkspaceMaxFileBytes   = 512 * 1024
	consoleChatWorkspaceMaxUploadBytes = 20 * 1024 * 1024
)

type consoleChatWorkspaceTarget struct {
	SessionID     string
	Environment   string
	WorkerID      string
	ContainerName string
	WorkspacePath string
	Worker        domain.WorkerServer
	Key           domain.WorkerSSHKey
	Account       domain.CloudAgentAccount
	CloudSession  domain.CloudAgentSession
}

type consoleChatWorkspaceEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	Size        int64  `json:"size,omitempty"`
	ModifiedAt  string `json:"modifiedAt,omitempty"`
	HasChildren bool   `json:"hasChildren,omitempty"`
}

type consoleChatWorkspaceTreeResult struct {
	Path     string                                 `json:"path,omitempty"`
	Entries  []consoleChatWorkspaceEntry            `json:"entries,omitempty"`
	Children map[string][]consoleChatWorkspaceEntry `json:"children,omitempty"`
}

type consoleChatWorkspaceTreeResponse struct {
	Status        string                                 `json:"status"`
	SessionID     string                                 `json:"sessionId,omitempty"`
	Environment   string                                 `json:"environment,omitempty"`
	WorkerID      string                                 `json:"workerId,omitempty"`
	ContainerName string                                 `json:"containerName,omitempty"`
	WorkspacePath string                                 `json:"workspacePath,omitempty"`
	Path          string                                 `json:"path,omitempty"`
	Entries       []consoleChatWorkspaceEntry            `json:"entries,omitempty"`
	Children      map[string][]consoleChatWorkspaceEntry `json:"children,omitempty"`
	Error         string                                 `json:"error,omitempty"`
}

type consoleChatWorkspaceFileResult struct {
	Path       string `json:"path,omitempty"`
	Name       string `json:"name,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Kind       string `json:"kind,omitempty"`
	MediaType  string `json:"mediaType,omitempty"`
	Content    string `json:"content,omitempty"`
	Payload    []byte `json:"-"`
	PreviewURL string `json:"previewURL,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
}

type consoleChatWorkspaceCopyResult struct {
	SourcePath string
	Path       string
}

type consoleChatWorkspaceRenameResult struct {
	OldPath string
	Path    string
}

type consoleChatWorkspaceFileResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Path          string `json:"path,omitempty"`
	Size          int64  `json:"size,omitempty"`
	Kind          string `json:"kind,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	Content       string `json:"content,omitempty"`
	PreviewURL    string `json:"previewURL,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatWorkspaceFileSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Create  bool   `json:"create"`
	MkdirP  bool   `json:"mkdir_p"`
}

type consoleChatWorkspaceDirectoryCreateRequest struct {
	Path   string `json:"path"`
	MkdirP bool   `json:"mkdir_p"`
}

type consoleChatWorkspaceRenameRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type consoleChatWorkspacePathRequest struct {
	Path string `json:"path"`
}

type consoleChatWorkspaceCopyRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type consoleChatWorkspaceDirectoryResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Path          string `json:"path,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatWorkspaceRenameResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	OldPath       string `json:"oldPath,omitempty"`
	Path          string `json:"path,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatWorkspaceCopyResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	SourcePath    string `json:"sourcePath,omitempty"`
	Path          string `json:"path,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatWorkspaceDeleteResponse struct {
	Status        string `json:"status"`
	SessionID     string `json:"sessionId,omitempty"`
	Environment   string `json:"environment,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Path          string `json:"path,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (handler *Handler) resolveConsoleChatWorkspaceTarget(ctx context.Context, r *http.Request, ensureRuntime bool) (consoleChatWorkspaceTarget, error) {
	var worker domain.WorkerServer
	var key domain.WorkerSSHKey
	var account domain.CloudAgentAccount
	var cloudSession domain.CloudAgentSession
	var err error
	if ensureRuntime {
		worker, key, account, cloudSession, err = handler.resolveConsoleChatCloudAgentRuntimeTarget(ctx, r, r.URL.Query().Get("session"))
	} else {
		worker, key, account, cloudSession, err = handler.resolveConsoleChatCloudAgentTarget(ctx, r, r.URL.Query().Get("session"))
	}
	if err != nil {
		return consoleChatWorkspaceTarget{}, err
	}
	sessionID := firstNonEmpty(strings.TrimSpace(cloudSession.ChatSessionID), strings.TrimSpace(r.URL.Query().Get("session")))
	workspacePath := firstNonEmpty(strings.TrimSpace(cloudSession.WorkspacePath), strings.TrimSpace(account.WorkspacePath), domain.DefaultCloudAgentWorkspacePath)
	return consoleChatWorkspaceTarget{
		SessionID:     sessionID,
		Environment:   consoleChatEnvironmentValue(worker.ID),
		WorkerID:      worker.ID,
		ContainerName: account.ContainerName,
		WorkspacePath: workspacePath,
		Worker:        worker,
		Key:           key,
		Account:       account,
		CloudSession:  cloudSession,
	}, nil
}

func (handler *Handler) chatWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceTreeResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, r.URL.Query().Get("path"), true)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceTreeResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         err.Error(),
		})
		return
	}
	result, err := handler.listConsoleChatWorkspace(r.Context(), target, relativePath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.listConsoleChatWorkspace(r.Context(), target, relativePath)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceTreeResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceTreeResponse{
		Status:        "ready",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Entries:       result.Entries,
		Children:      result.Children,
	})
}

func (handler *Handler) chatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, r.URL.Query().Get("path"), false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         err.Error(),
		})
		return
	}
	result, err := handler.readConsoleChatWorkspaceFile(r.Context(), target, relativePath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.readConsoleChatWorkspaceFile(r.Context(), target, relativePath)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
		Status:        "ready",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Size:          result.Size,
		Kind:          result.Kind,
		MediaType:     result.MediaType,
		Content:       result.Content,
		PreviewURL:    result.PreviewURL,
	})
}

func (handler *Handler) downloadChatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", Error: err.Error()})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, r.URL.Query().Get("path"), false)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: err.Error()})
		return
	}
	result, err := handler.downloadConsoleChatWorkspaceFile(r.Context(), target, relativePath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.downloadConsoleChatWorkspaceFile(r.Context(), target, relativePath)
		}
	}
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: relativePath, Error: err.Error()})
		return
	}
	filename := strings.TrimSpace(result.Name)
	if filename == "" {
		filename = path.Base(result.Path)
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "download"
	}
	mediaType := strings.TrimSpace(result.MediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Payload)
}

func (handler *Handler) saveChatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	var request consoleChatWorkspaceFileSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message := handler.requestText(r, "工作区文件保存请求无效。", "The workspace save request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         message,
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, request.Path, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         err.Error(),
		})
		return
	}
	if len([]byte(request.Content)) > consoleChatWorkspaceMaxFileBytes {
		message := handler.requestText(r, "文件超过编辑器允许的大小上限。", "The file exceeds the editor size limit.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         message,
		})
		return
	}
	writeFile := func(target consoleChatWorkspaceTarget) (consoleChatWorkspaceFileResult, error) {
		if request.Create {
			return handler.createConsoleChatWorkspaceFile(r.Context(), target, relativePath, request.Content, request.MkdirP)
		}
		return handler.writeConsoleChatWorkspaceFile(r.Context(), target, relativePath, request.Content)
	}
	result, err := writeFile(target)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = writeFile(target)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         err.Error(),
		})
		return
	}
	status := "saved"
	notice := handler.requestText(r, "文件已保存。", "File saved.")
	if request.Create {
		status = "created"
		notice = handler.requestText(r, "文件已创建。", "File created.")
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
		Status:        status,
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Bytes:         result.Bytes,
		Notice:        notice,
	})
}

func (handler *Handler) uploadChatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", Error: err.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, consoleChatWorkspaceMaxUploadBytes+1024*1024)
	if err := r.ParseMultipartForm(consoleChatWorkspaceMaxUploadBytes); err != nil {
		message := handler.requestText(r, "工作区上传请求无效。", "The workspace upload request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         message,
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, r.FormValue("path"), false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         err.Error(),
		})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		message := handler.requestText(r, "请选择要上传的文件。", "Choose a file to upload.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         message,
		})
		return
	}
	defer file.Close()
	if header != nil && header.Size > consoleChatWorkspaceMaxUploadBytes {
		message := handler.requestText(r, "上传文件超过大小上限。", "The uploaded file exceeds the size limit.")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: relativePath, Error: message})
		return
	}
	payload, err := io.ReadAll(io.LimitReader(file, consoleChatWorkspaceMaxUploadBytes+1))
	if err != nil {
		message := handler.requestText(r, "读取上传文件失败。", "Failed to read the uploaded file.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: relativePath, Error: message})
		return
	}
	if len(payload) > consoleChatWorkspaceMaxUploadBytes {
		message := handler.requestText(r, "上传文件超过大小上限。", "The uploaded file exceeds the size limit.")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: relativePath, Error: message})
		return
	}
	overwrite := strings.EqualFold(strings.TrimSpace(r.FormValue("overwrite")), "true") || strings.TrimSpace(r.FormValue("overwrite")) == "1"
	uploadFile := func(target consoleChatWorkspaceTarget) (consoleChatWorkspaceFileResult, error) {
		return handler.uploadConsoleChatWorkspaceFile(r.Context(), target, relativePath, payload, true, overwrite)
	}
	result, err := uploadFile(target)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = uploadFile(target)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceFileResponse{
		Status:        "uploaded",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Bytes:         result.Bytes,
		Notice:        handler.requestText(r, "文件已上传。", "File uploaded."),
	})
}

func (handler *Handler) createChatWorkspaceDirectory(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDirectoryResponse{Status: "error", Error: err.Error()})
		return
	}
	var request consoleChatWorkspaceDirectoryCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message := handler.requestText(r, "工作区目录创建请求无效。", "The workspace directory create request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDirectoryResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         message,
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, request.Path, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDirectoryResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         err.Error(),
		})
		return
	}
	result, err := handler.createConsoleChatWorkspaceDirectory(r.Context(), target, relativePath, request.MkdirP)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.createConsoleChatWorkspaceDirectory(r.Context(), target, relativePath, request.MkdirP)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDirectoryResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Path:          relativePath,
			Error:         err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDirectoryResponse{
		Status:        "created",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Notice:        handler.requestText(r, "目录已创建。", "Directory created."),
	})
}

func (handler *Handler) copyChatWorkspacePath(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "error", Error: err.Error()})
		return
	}
	var request consoleChatWorkspaceCopyRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message := handler.requestText(r, "工作区复制请求无效。", "The workspace copy request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: message})
		return
	}
	oldPath, err := handler.consoleChatWorkspaceRelativePath(r, request.Path, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: err.Error()})
		return
	}
	newPath, err := handler.consoleChatWorkspaceRelativePath(r, request.NewPath, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, SourcePath: oldPath, Error: err.Error()})
		return
	}
	result, err := handler.copyConsoleChatWorkspacePath(r.Context(), target, oldPath, newPath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.copyConsoleChatWorkspacePath(r.Context(), target, oldPath, newPath)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, SourcePath: oldPath, Path: newPath, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceCopyResponse{Status: "copied", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, SourcePath: result.SourcePath, Path: result.Path, Notice: handler.requestText(r, "已复制。", "Copied.")})
}

func (handler *Handler) renameChatWorkspacePath(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{Status: "error", Error: err.Error()})
		return
	}
	var request consoleChatWorkspaceRenameRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message := handler.requestText(r, "工作区重命名请求无效。", "The workspace rename request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			Error:         message,
		})
		return
	}
	oldPath, err := handler.consoleChatWorkspaceRelativePath(r, request.Path, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: err.Error()})
		return
	}
	newPath, err := handler.consoleChatWorkspaceRelativePath(r, request.NewPath, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, OldPath: oldPath, Error: err.Error()})
		return
	}
	result, err := handler.renameConsoleChatWorkspacePath(r.Context(), target, oldPath, newPath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.renameConsoleChatWorkspacePath(r.Context(), target, oldPath, newPath)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{
			Status:        "error",
			SessionID:     target.SessionID,
			Environment:   target.Environment,
			WorkerID:      target.WorkerID,
			ContainerName: target.ContainerName,
			WorkspacePath: target.WorkspacePath,
			OldPath:       oldPath,
			Path:          newPath,
			Error:         err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceRenameResponse{
		Status:        "renamed",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		OldPath:       result.OldPath,
		Path:          result.Path,
		Notice:        handler.requestText(r, "已重命名。", "Renamed."),
	})
}

func (handler *Handler) deleteChatWorkspacePath(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDeleteResponse{Status: "error", Error: err.Error()})
		return
	}
	var request consoleChatWorkspacePathRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message := handler.requestText(r, "工作区删除请求无效。", "The workspace delete request is invalid.")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDeleteResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: message})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, request.Path, false)
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDeleteResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Error: err.Error()})
		return
	}
	result, err := handler.deleteConsoleChatWorkspacePath(r.Context(), target, relativePath)
	if err != nil {
		if retryTarget, retried, retryErr := handler.restoreConsoleChatWorkspaceRuntime(r.Context(), r, target, err); retryErr != nil {
			err = retryErr
		} else if retried {
			target = retryTarget
			result, err = handler.deleteConsoleChatWorkspacePath(r.Context(), target, relativePath)
		}
	}
	if err != nil {
		w.WriteHeader(consoleChatWorkspaceErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDeleteResponse{Status: "error", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: relativePath, Error: err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatWorkspaceDeleteResponse{Status: "deleted", SessionID: target.SessionID, Environment: target.Environment, WorkerID: target.WorkerID, ContainerName: target.ContainerName, WorkspacePath: target.WorkspacePath, Path: result.Path, Notice: handler.requestText(r, "已永久删除。", "Deleted permanently.")})
}

func (handler *Handler) listConsoleChatWorkspace(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceTreeResult, error) {
	result, err := handler.listCloudAgentWorkspaceTree(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceTreeResult{}, err
	}
	return consoleChatWorkspaceTreeResult{Path: result.Path, Entries: consoleChatWorkspaceEntries(result.Entries), Children: consoleChatWorkspaceChildren(result.Children)}, nil
}

func (handler *Handler) readConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.readCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	kind := strings.TrimSpace(result.Kind)
	if kind == "" {
		kind = "text"
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Size: result.Size, Kind: kind, MediaType: strings.TrimSpace(result.MediaType), Content: result.Content, PreviewURL: strings.TrimSpace(result.PreviewURL)}, nil
}

func (handler *Handler) downloadConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.downloadCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Name: strings.TrimSpace(result.Name), Size: result.Size, MediaType: strings.TrimSpace(result.MediaType), Payload: result.Content}, nil
}

func (handler *Handler) writeConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, content string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.writeCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath, content)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Bytes: result.Bytes}, nil
}

func (handler *Handler) createConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, content string, mkdirP bool) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.createCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath, content, mkdirP)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Bytes: result.Bytes}, nil
}

func (handler *Handler) uploadConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, content []byte, mkdirP bool, overwrite bool) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.uploadCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath, content, mkdirP, overwrite)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Bytes: result.Bytes}, nil
}

func (handler *Handler) createConsoleChatWorkspaceDirectory(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, mkdirP bool) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.createCloudAgentWorkspaceDir(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath, mkdirP)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path}, nil
}

func (handler *Handler) copyConsoleChatWorkspacePath(ctx context.Context, target consoleChatWorkspaceTarget, oldPath string, newPath string) (consoleChatWorkspaceCopyResult, error) {
	result, err := handler.copyCloudAgentWorkspacePath(ctx, target.Worker, target.Key, target.Account, target.CloudSession, oldPath, newPath)
	if err != nil {
		return consoleChatWorkspaceCopyResult{}, err
	}
	return consoleChatWorkspaceCopyResult{SourcePath: result.SourcePath, Path: result.Path}, nil
}

func (handler *Handler) renameConsoleChatWorkspacePath(ctx context.Context, target consoleChatWorkspaceTarget, oldPath string, newPath string) (consoleChatWorkspaceRenameResult, error) {
	result, err := handler.renameCloudAgentWorkspacePath(ctx, target.Worker, target.Key, target.Account, target.CloudSession, oldPath, newPath)
	if err != nil {
		return consoleChatWorkspaceRenameResult{}, err
	}
	return consoleChatWorkspaceRenameResult{OldPath: result.OldPath, Path: result.Path}, nil
}

func (handler *Handler) deleteConsoleChatWorkspacePath(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.deleteCloudAgentWorkspacePath(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path}, nil
}

func (handler *Handler) restoreConsoleChatWorkspaceRuntime(ctx context.Context, r *http.Request, target consoleChatWorkspaceTarget, cause error) (consoleChatWorkspaceTarget, bool, error) {
	if !consoleChatWorkspaceRuntimeUnavailable(cause) {
		return target, false, nil
	}
	refreshed, err := handler.resolveConsoleChatWorkspaceTarget(ctx, r, true)
	if err != nil {
		return target, false, err
	}
	return refreshed, true, nil
}

func consoleChatWorkspaceEntries(entries []workerops.CloudAgentWorkspaceEntry) []consoleChatWorkspaceEntry {
	if len(entries) == 0 {
		return nil
	}
	result := make([]consoleChatWorkspaceEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, consoleChatWorkspaceEntry{
			Name:        entry.Name,
			Path:        entry.Path,
			Type:        entry.Type,
			Size:        entry.Size,
			ModifiedAt:  entry.ModifiedAt,
			HasChildren: entry.HasChildren,
		})
	}
	return result
}

func consoleChatWorkspaceChildren(children map[string][]workerops.CloudAgentWorkspaceEntry) map[string][]consoleChatWorkspaceEntry {
	if len(children) == 0 {
		return nil
	}
	result := make(map[string][]consoleChatWorkspaceEntry, len(children))
	for childPath, entries := range children {
		childPath = strings.TrimSpace(childPath)
		if childPath == "" {
			continue
		}
		result[childPath] = consoleChatWorkspaceEntries(entries)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func consoleChatWorkspaceRuntimeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return (strings.Contains(message, "cloud agent container") && strings.Contains(message, "not available")) ||
		(strings.Contains(message, "aiyolo-ass socket") && strings.Contains(message, "not available")) ||
		(strings.Contains(message, "aiyolo-ass endpoint") && strings.Contains(message, "not available")) ||
		strings.Contains(message, "no such container") ||
		strings.Contains(message, "connect aiyolo-ass") ||
		(strings.Contains(message, "could not connect") && strings.Contains(message, "aiyolo-ass"))
}

func (handler *Handler) consoleChatWorkspaceRelativePath(r *http.Request, raw string, allowRoot bool) (string, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if strings.ContainsRune(raw, '\x00') {
		return "", errors.New(handler.requestText(r, "工作区路径无效。", "The workspace path is invalid."))
	}
	if raw == "" || raw == "." || raw == "/" {
		if allowRoot {
			return "", nil
		}
		return "", errors.New(handler.requestText(r, "请选择一个文件。", "Choose a file first."))
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if cleaned == "." || cleaned == "" {
		if allowRoot {
			return "", nil
		}
		return "", errors.New(handler.requestText(r, "请选择一个文件。", "Choose a file first."))
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", errors.New(handler.requestText(r, "工作区路径超出了根目录。", "The workspace path escapes the workspace root."))
	}
	return cleaned, nil
}

func consoleChatWorkspaceShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func consoleChatWorkspaceErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "path_exists") || strings.Contains(message, "already exists") {
		return http.StatusConflict
	}
	if strings.Contains(message, "file_too_large") || strings.Contains(message, "too large") || strings.Contains(message, "exceeds the size limit") {
		return http.StatusRequestEntityTooLarge
	}
	if strings.Contains(message, "missing chat session") || strings.Contains(message, "workspace") || strings.Contains(message, "choose a file") {
		return http.StatusBadRequest
	}
	if strings.Contains(message, "not active") || strings.Contains(message, "not ready") || strings.Contains(message, "does not match") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
