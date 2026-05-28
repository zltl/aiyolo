package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/storage"
)

const consoleChatAttachmentTreePath = "/console/chat/attachments/tree"

type consoleChatAttachmentTreeEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ModifiedAt  string `json:"modifiedAt,omitempty"`
	HasChildren bool   `json:"hasChildren,omitempty"`
}

type consoleChatAttachmentTreeResult struct {
	RootLabel string                          `json:"rootLabel,omitempty"`
	Path      string                          `json:"path,omitempty"`
	Entries   []consoleChatAttachmentTreeEntry `json:"entries,omitempty"`
}

type consoleChatAttachmentTreeResponse struct {
	Status    string                          `json:"status"`
	Bucket    string                          `json:"bucket,omitempty"`
	Prefix    string                          `json:"prefix,omitempty"`
	RootLabel string                          `json:"rootLabel,omitempty"`
	Path      string                          `json:"path,omitempty"`
	Entries   []consoleChatAttachmentTreeEntry `json:"entries,omitempty"`
	Error     string                          `json:"error,omitempty"`
}

func (handler *Handler) chatAttachmentTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !handler.cfg.ChatAttachments.CanList() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
			Status: "error",
			Error:  handler.requestText(r, "当前没有配置可浏览 chat 附件目录的对象存储。", "Chat attachment storage is not configured for directory browsing."),
		})
		return
	}
	subjectPrefix, err := handler.consoleChatAttachmentSubjectPrefix(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
			Status: "error",
			Error:  err.Error(),
		})
		return
	}
	relativePath, err := handler.consoleChatWorkspaceRelativePath(r, r.URL.Query().Get("path"), true)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
			Status:    "error",
			Bucket:    strings.TrimSpace(handler.cfg.ChatAttachments.S3.Bucket),
			Prefix:    artifacts.NormalizeObjectKey(handler.cfg.ChatAttachments.S3.Prefix),
			RootLabel: consoleChatAttachmentTreeRootLabel(handler.cfg.ChatAttachments),
			Error:     err.Error(),
		})
		return
	}
	catalogPath, displayPath, err := consoleChatAttachmentTreeScopePath(relativePath, subjectPrefix)
	if err != nil {
		w.WriteHeader(consoleChatAttachmentTreeErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
			Status:    "error",
			Bucket:    strings.TrimSpace(handler.cfg.ChatAttachments.S3.Bucket),
			Prefix:    artifacts.NormalizeObjectKey(handler.cfg.ChatAttachments.S3.Prefix),
			RootLabel: consoleChatAttachmentTreeRootLabel(handler.cfg.ChatAttachments),
			Path:      artifacts.NormalizeObjectKey(relativePath),
			Error:     err.Error(),
		})
		return
	}
	result, err := handler.listConsoleChatAttachmentTree(r.Context(), catalogPath, displayPath)
	if err != nil {
		w.WriteHeader(consoleChatAttachmentTreeErrorStatus(err))
		_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
			Status:    "error",
			Bucket:    strings.TrimSpace(handler.cfg.ChatAttachments.S3.Bucket),
			Prefix:    artifacts.NormalizeObjectKey(handler.cfg.ChatAttachments.S3.Prefix),
			RootLabel: consoleChatAttachmentTreeRootLabel(handler.cfg.ChatAttachments),
			Path:      result.Path,
			Error:     err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(consoleChatAttachmentTreeResponse{
		Status:    "ready",
		Bucket:    strings.TrimSpace(handler.cfg.ChatAttachments.S3.Bucket),
		Prefix:    artifacts.NormalizeObjectKey(handler.cfg.ChatAttachments.S3.Prefix),
		RootLabel: result.RootLabel,
		Path:      result.Path,
		Entries:   result.Entries,
	})
}

func (handler *Handler) listConsoleChatAttachmentTree(ctx context.Context, catalogPath string, displayPath string) (consoleChatAttachmentTreeResult, error) {
	reader, err := handler.newChatAttachmentCatalogReader(handler.cfg.ChatAttachments)
	if err != nil {
		return consoleChatAttachmentTreeResult{}, err
	}
	catalog, err := reader.Catalog(ctx, catalogPath)
	if err != nil {
		return consoleChatAttachmentTreeResult{}, err
	}
	catalog = filterConsoleChatAttachmentCatalog(catalog, catalogPath)
	return consoleChatAttachmentTreeResult{
		RootLabel: consoleChatAttachmentTreeRootLabel(handler.cfg.ChatAttachments),
		Path:      artifacts.NormalizeObjectKey(displayPath),
		Entries:   consoleChatAttachmentTreeEntries(handler.cfg.ChatAttachments, catalog, displayPath),
	}, nil
}

func consoleChatAttachmentTreeScopePath(relativePath string, subjectPrefix string) (string, string, error) {
	subjectPrefix = artifacts.NormalizeObjectKey(subjectPrefix)
	if subjectPrefix == "" {
		return "", "", storage.ErrNotFound
	}
	relativePath = artifacts.NormalizeObjectKey(relativePath)
	if relativePath == "" {
		return subjectPrefix, "", nil
	}
	if relativePath == subjectPrefix || strings.HasPrefix(relativePath, subjectPrefix+"/") {
		return relativePath, relativePath, nil
	}
	return "", "", storage.ErrNotFound
}

func filterConsoleChatAttachmentCatalog(catalog artifacts.Catalog, prefix string) artifacts.Catalog {
	prefix = artifacts.NormalizeObjectKey(prefix)
	if prefix == "" {
		return catalog
	}
	entries := make([]artifacts.CatalogEntry, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		key := artifacts.NormalizeObjectKey(entry.RelativeKey)
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			entries = append(entries, entry)
		}
	}
	catalog.Entries = entries
	return catalog
}

func consoleChatAttachmentTreeEntries(cfg artifacts.Config, catalog artifacts.Catalog, relativePath string) []consoleChatAttachmentTreeEntry {
	basePath := artifacts.NormalizeObjectKey(relativePath)
	prefix := ""
	if basePath != "" {
		prefix = basePath + "/"
	}
	directories := make(map[string]consoleChatAttachmentTreeEntry)
	files := make([]consoleChatAttachmentTreeEntry, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		key := artifacts.NormalizeObjectKey(entry.RelativeKey)
		if key == "" {
			continue
		}
		child := key
		if prefix != "" {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			child = strings.TrimPrefix(key, prefix)
		}
		if child == "" {
			continue
		}
		name := child
		tail := ""
		if index := strings.IndexByte(child, '/'); index >= 0 {
			name = child[:index]
			tail = child[index+1:]
		}
		entryPath := name
		if basePath != "" {
			entryPath = path.Join(basePath, name)
		}
		if tail != "" {
			if _, exists := directories[entryPath]; !exists {
				directories[entryPath] = consoleChatAttachmentTreeEntry{
					Name:        name,
					Path:        entryPath,
					Type:        "directory",
					HasChildren: true,
				}
			}
			continue
		}
		modifiedAt := ""
		if !entry.LastModified.IsZero() {
			modifiedAt = entry.LastModified.UTC().Format(time.RFC3339)
		}
		files = append(files, consoleChatAttachmentTreeEntry{
			Name:       name,
			Path:       entryPath,
			Type:       "file",
			URL:        cfg.ProxyObjectURL(entryPath),
			Size:       entry.SizeBytes,
			ModifiedAt: modifiedAt,
		})
	}
	entries := make([]consoleChatAttachmentTreeEntry, 0, len(directories)+len(files))
	for _, entry := range directories {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		left := strings.ToLower(entries[i].Name)
		right := strings.ToLower(entries[j].Name)
		if left == right {
			return entries[i].Path < entries[j].Path
		}
		return left < right
	})
	sort.Slice(files, func(i, j int) bool {
		left := strings.ToLower(files[i].Name)
		right := strings.ToLower(files[j].Name)
		if left == right {
			return files[i].Path < files[j].Path
		}
		return left < right
	})
	return append(entries, files...)
}

func consoleChatAttachmentTreeRootLabel(cfg artifacts.Config) string {
	bucket := strings.TrimSpace(cfg.S3.Bucket)
	prefix := artifacts.NormalizeObjectKey(cfg.S3.Prefix)
	switch {
	case bucket != "" && prefix != "":
		return bucket + "/" + prefix
	case bucket != "":
		return bucket
	case prefix != "":
		return prefix
	default:
		return "chat attachments"
	}
}

func consoleChatAttachmentTreeErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "required") || strings.Contains(message, "invalid") || strings.Contains(message, "configured") {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}