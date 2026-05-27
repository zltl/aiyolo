package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
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
	worker, key, account, cloudSession, err := handler.resolveConsoleChatCloudAgentTarget(ctx, r, r.URL.Query().Get("session"))
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
	output, err := handler.runCloudAgentCommand(ctx, target.Worker, target.Key, target.Account, target.CloudSession, buildConsoleChatWorkspaceTreeScript(relativePath))
	if err != nil {
		return consoleChatWorkspaceTreeResult{}, err
	}
	var result consoleChatWorkspaceTreeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return consoleChatWorkspaceTreeResult{}, err
	}
	return result, nil
}

func (handler *Handler) readConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string) (consoleChatWorkspaceFileResult, error) {
	output, err := handler.runCloudAgentCommand(ctx, target.Worker, target.Key, target.Account, target.CloudSession, buildConsoleChatWorkspaceReadScript(relativePath))
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	var result consoleChatWorkspaceFileResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return result, nil
}

func (handler *Handler) writeConsoleChatWorkspaceFile(ctx context.Context, target consoleChatWorkspaceTarget, relativePath string, content string) (consoleChatWorkspaceFileResult, error) {
	output, err := handler.runCloudAgentCommand(ctx, target.Worker, target.Key, target.Account, target.CloudSession, buildConsoleChatWorkspaceWriteScript(relativePath, content))
	if err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	var result consoleChatWorkspaceFileResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return consoleChatWorkspaceFileResult{}, err
	}
	return result, nil
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

func buildConsoleChatWorkspaceTreeScript(relativePath string) string {
	return fmt.Sprintf(`export AIOYOLO_WORKSPACE_TARGET=%s
python3 - <<'PY'
import datetime
import json
import os
import sys

root = os.path.realpath(os.getcwd())
relative = os.environ.get("AIOYOLO_WORKSPACE_TARGET", "").strip()

def resolve_within_workspace(raw: str) -> str:
    if raw in ("", ".", "/"):
        return root
    candidate = os.path.realpath(os.path.join(root, raw))
    if candidate != root and not candidate.startswith(root + os.sep):
        raise ValueError("workspace path escapes root")
    return candidate

try:
    target = resolve_within_workspace(relative)
    if not os.path.isdir(target):
        raise ValueError("workspace path is not a directory")
    entries = []
    with os.scandir(target) as iterator:
        for entry in iterator:
            if entry.name in (".", ".."):
                continue
            try:
                stat_result = entry.stat(follow_symlinks=False)
            except OSError:
                continue
            entry_type = "directory" if entry.is_dir(follow_symlinks=False) else "file"
            has_children = False
            if entry_type == "directory":
                try:
                    with os.scandir(entry.path) as nested:
                        has_children = any(True for _ in nested)
                except OSError:
                    has_children = False
            rel_path = os.path.relpath(entry.path, root)
            if rel_path == ".":
                rel_path = ""
            entries.append({
                "name": entry.name,
                "path": rel_path,
                "type": entry_type,
                "size": int(stat_result.st_size),
                "modifiedAt": datetime.datetime.fromtimestamp(stat_result.st_mtime, datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
                "hasChildren": has_children,
            })
    entries.sort(key=lambda item: (0 if item["type"] == "directory" else 1, item["name"].lower()))
    print(json.dumps({"path": relative, "entries": entries}, ensure_ascii=False))
except ValueError as exc:
    print(str(exc), file=sys.stderr)
    raise SystemExit(2)
PY
`, consoleChatWorkspaceShellQuote(relativePath))
}

func buildConsoleChatWorkspaceReadScript(relativePath string) string {
	return fmt.Sprintf(`export AIOYOLO_WORKSPACE_TARGET=%s
export AIOYOLO_MAX_FILE_BYTES=%d
python3 - <<'PY'
import json
import os
import sys

root = os.path.realpath(os.getcwd())
relative = os.environ.get("AIOYOLO_WORKSPACE_TARGET", "").strip()
max_bytes = int(os.environ.get("AIOYOLO_MAX_FILE_BYTES", "0") or "0")

def resolve_within_workspace(raw: str) -> str:
    if raw in ("", ".", "/"):
        raise ValueError("workspace file path is required")
    candidate = os.path.realpath(os.path.join(root, raw))
    if candidate != root and not candidate.startswith(root + os.sep):
        raise ValueError("workspace path escapes root")
    return candidate

try:
    target = resolve_within_workspace(relative)
    if not os.path.exists(target):
        raise ValueError("workspace file does not exist")
    if not os.path.isfile(target):
        raise ValueError("workspace path is not a file")
    size = os.path.getsize(target)
    if max_bytes > 0 and size > max_bytes:
        raise ValueError(f"workspace file is too large ({size} bytes)")
    with open(target, "rb") as handle:
        payload = handle.read()
    if b"\x00" in payload:
        raise ValueError("workspace file is binary and cannot be edited here")
    try:
        content = payload.decode("utf-8")
    except UnicodeDecodeError:
        raise ValueError("workspace file is not valid UTF-8")
    print(json.dumps({"path": relative, "size": size, "content": content}, ensure_ascii=False))
except ValueError as exc:
    print(str(exc), file=sys.stderr)
    raise SystemExit(2)
PY
`, consoleChatWorkspaceShellQuote(relativePath), consoleChatWorkspaceMaxFileBytes)
}

func buildConsoleChatWorkspaceWriteScript(relativePath string, content string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf(`export AIOYOLO_WORKSPACE_TARGET=%s
export AIOYOLO_FILE_CONTENT_BASE64=%s
python3 - <<'PY'
import base64
import json
import os
import sys

root = os.path.realpath(os.getcwd())
relative = os.environ.get("AIOYOLO_WORKSPACE_TARGET", "").strip()
payload = os.environ.get("AIOYOLO_FILE_CONTENT_BASE64", "")

def resolve_within_workspace(raw: str) -> str:
    if raw in ("", ".", "/"):
        raise ValueError("workspace file path is required")
    candidate = os.path.realpath(os.path.join(root, raw))
    if candidate != root and not candidate.startswith(root + os.sep):
        raise ValueError("workspace path escapes root")
    return candidate

try:
    target = resolve_within_workspace(relative)
    if not os.path.exists(target):
        raise ValueError("workspace file does not exist")
    if not os.path.isfile(target):
        raise ValueError("workspace path is not a file")
    content = base64.b64decode(payload.encode("ascii"), validate=True)
    with open(target, "wb") as handle:
        handle.write(content)
    print(json.dumps({"path": relative, "bytes": len(content)}, ensure_ascii=False))
except (ValueError, base64.binascii.Error) as exc:
    print(str(exc), file=sys.stderr)
    raise SystemExit(2)
PY
`, consoleChatWorkspaceShellQuote(relativePath), consoleChatWorkspaceShellQuote(encoded))
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