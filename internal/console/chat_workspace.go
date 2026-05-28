package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

const (
	consoleChatWorkspaceTreePath     = "/console/chat/workspace/tree"
	consoleChatWorkspaceFilePath     = "/console/chat/workspace/file"
	consoleChatWorkspaceMaxFileBytes = 512 * 1024
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
	Path    string                      `json:"path,omitempty"`
	Entries []consoleChatWorkspaceEntry `json:"entries,omitempty"`
}

type consoleChatWorkspaceTreeResponse struct {
	Status        string                      `json:"status"`
	SessionID     string                      `json:"sessionId,omitempty"`
	Environment   string                      `json:"environment,omitempty"`
	WorkerID      string                      `json:"workerId,omitempty"`
	ContainerName string                      `json:"containerName,omitempty"`
	WorkspacePath string                      `json:"workspacePath,omitempty"`
	Path          string                      `json:"path,omitempty"`
	Entries       []consoleChatWorkspaceEntry `json:"entries,omitempty"`
	Error         string                      `json:"error,omitempty"`
}

type consoleChatWorkspaceFileResult struct {
	Path    string `json:"path,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Content string `json:"content,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
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
	Bytes         int64  `json:"bytes,omitempty"`
	Content       string `json:"content,omitempty"`
	Notice        string `json:"notice,omitempty"`
	Error         string `json:"error,omitempty"`
}

type consoleChatWorkspaceFileSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (handler *Handler) resolveConsoleChatWorkspaceTarget(ctx context.Context, r *http.Request) (consoleChatWorkspaceTarget, error) {
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentRuntimeTarget(ctx, r, r.URL.Query().Get("session"))
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
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r)
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
	})
}

func (handler *Handler) chatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r)
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
		Content:       result.Content,
	})
}

func (handler *Handler) saveChatWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	target, err := handler.resolveConsoleChatWorkspaceTarget(r.Context(), r)
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
	result, err := handler.writeConsoleChatWorkspaceFile(r.Context(), target, relativePath, request.Content)
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
		Status:        "saved",
		SessionID:     target.SessionID,
		Environment:   target.Environment,
		WorkerID:      target.WorkerID,
		ContainerName: target.ContainerName,
		WorkspacePath: target.WorkspacePath,
		Path:          result.Path,
		Bytes:         result.Bytes,
		Notice:        handler.requestText(r, "文件已保存。", "File saved."),
	})
}

func (handler *Handler) listConsoleChatWorkspace(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceTreeResult, error) {
	result, err := handler.listCloudAgentWorkspaceTree(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceTreeResult{}, err
	}
	return consoleChatWorkspaceTreeResult{Path: result.Path, Entries: consoleChatWorkspaceEntries(result.Entries)}, nil
}

func (handler *Handler) readConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.readCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Size: result.Size, Content: result.Content}, nil
}

func (handler *Handler) writeConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, content string) (consoleChatWorkspaceFileResult, error) {
	result, err := handler.writeCloudAgentWorkspaceFile(ctx, target.Worker, target.Key, target.Account, target.CloudSession, relativePath, content)
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return consoleChatWorkspaceFileResult{Path: result.Path, Bytes: result.Bytes}, nil
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
	if strings.Contains(message, "missing chat session") || strings.Contains(message, "workspace") || strings.Contains(message, "choose a file") {
		return http.StatusBadRequest
	}
	if strings.Contains(message, "not active") || strings.Contains(message, "not ready") || strings.Contains(message, "does not match") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
