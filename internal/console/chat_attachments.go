package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
)

const (
	consoleChatAttachmentUploadPath = "/console/chat/attachments"
	consoleChatAttachmentMaxBytes   = 20 << 20
	consoleChatAttachmentMaxFiles   = 6
)

type consoleChatAttachmentUploadResponse struct {
	Attachments []consoleChatAttachmentView `json:"attachments,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

func (handler *Handler) uploadChatAttachments(w http.ResponseWriter, r *http.Request) {
	if !handler.cfg.ChatAttachments.CanUpload() {
		handler.writeChatAttachmentResponse(w, http.StatusServiceUnavailable, consoleChatAttachmentUploadResponse{Error: handler.requestText(r, "当前没有配置可上传 chat 附件的对象存储。", "Chat attachment storage is not configured.")})
		return
	}
	maxRequestBytes := int64(consoleChatAttachmentMaxBytes*consoleChatAttachmentMaxFiles) + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(int64(consoleChatAttachmentMaxBytes * consoleChatAttachmentMaxFiles)); err != nil {
		handler.writeChatAttachmentResponse(w, http.StatusBadRequest, consoleChatAttachmentUploadResponse{Error: handler.requestText(r, "附件表单解析失败。", "Failed to parse attachment upload form.")})
		return
	}
	files := collectConsoleChatUploadFiles(r.MultipartForm)
	if len(files) == 0 {
		handler.writeChatAttachmentResponse(w, http.StatusBadRequest, consoleChatAttachmentUploadResponse{Error: handler.requestText(r, "至少选择一个附件。", "Select at least one attachment.")})
		return
	}
	if len(files) > consoleChatAttachmentMaxFiles {
		handler.writeChatAttachmentResponse(w, http.StatusBadRequest, consoleChatAttachmentUploadResponse{Error: fmt.Sprintf(handler.requestText(r, "一次最多上传 %d 个附件。", "Upload at most %d attachments at a time."), consoleChatAttachmentMaxFiles)})
		return
	}
	publisher, err := handler.newChatAttachmentPublisher(handler.cfg.ChatAttachments)
	if err != nil {
		handler.writeChatAttachmentResponse(w, http.StatusInternalServerError, consoleChatAttachmentUploadResponse{Error: err.Error()})
		return
	}
	subject := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	attachments := make([]consoleChatAttachmentView, 0, len(files))
	for _, header := range files {
		attachment, statusCode, err := handler.storeUploadedChatAttachment(r, publisher, subject, header)
		if err != nil {
			handler.writeChatAttachmentResponse(w, statusCode, consoleChatAttachmentUploadResponse{Error: err.Error()})
			return
		}
		attachments = append(attachments, attachment)
	}
	handler.writeChatAttachmentResponse(w, http.StatusOK, consoleChatAttachmentUploadResponse{Attachments: attachments})
}

func (handler *Handler) chatAttachmentFile(w http.ResponseWriter, r *http.Request) {
	if !handler.cfg.ChatAttachments.CanList() {
		http.Error(w, handler.requestText(r, "当前没有配置可读取 chat 附件的对象存储。", "Chat attachment storage is not configured."), http.StatusServiceUnavailable)
		return
	}
	subjectPrefix, err := handler.consoleChatAttachmentSubjectPrefix(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	reader, err := handler.newChatAttachmentReader(handler.cfg.ChatAttachments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	objectKey := strings.TrimPrefix(r.URL.Path, handler.cfg.ChatAttachments.NormalizedProxyBasePath())
	if objectKey == r.URL.Path {
		objectKey = strings.TrimPrefix(r.URL.Path, strings.TrimPrefix(handler.cfg.ChatAttachments.NormalizedProxyBasePath(), "/console"))
	}
	objectKey = strings.TrimLeft(objectKey, "/")
	if objectKey == "" {
		http.Error(w, "attachment path is required", http.StatusNotFound)
		return
	}
	if !consoleChatAttachmentOwnsObject(handler.cfg.ChatAttachments, objectKey, subjectPrefix) {
		http.NotFound(w, r)
		return
	}
	payload, mediaType, err := reader.ReadObject(r.Context(), objectKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if mediaType == "" {
		mediaType = http.DetectContentType(payload)
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(payload)
}

func collectConsoleChatUploadFiles(form *multipart.Form) []*multipart.FileHeader {
	if form == nil {
		return nil
	}
	files := append([]*multipart.FileHeader{}, form.File["files"]...)
	if len(files) == 0 {
		files = append(files, form.File["file"]...)
	}
	return files
}

func (handler *Handler) storeUploadedChatAttachment(r *http.Request, publisher consoleChatAttachmentPublisher, subject string, header *multipart.FileHeader) (consoleChatAttachmentView, int, error) {
	if header == nil {
		return consoleChatAttachmentView{}, http.StatusBadRequest, errors.New(handler.requestText(r, "附件不存在。", "Attachment payload is missing."))
	}
	if header.Size > consoleChatAttachmentMaxBytes {
		return consoleChatAttachmentView{}, http.StatusRequestEntityTooLarge, errors.New(fmt.Sprintf(handler.requestText(r, "附件 %s 超过 %d MiB 限制。", "Attachment %s exceeds the %d MiB limit."), strings.TrimSpace(header.Filename), consoleChatAttachmentMaxBytes>>20))
	}
	file, err := header.Open()
	if err != nil {
		return consoleChatAttachmentView{}, http.StatusBadRequest, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, int64(consoleChatAttachmentMaxBytes)+1))
	if err != nil {
		return consoleChatAttachmentView{}, http.StatusBadRequest, err
	}
	if len(payload) == 0 {
		return consoleChatAttachmentView{}, http.StatusBadRequest, errors.New(handler.requestText(r, "附件内容为空。", "Attachment content is empty."))
	}
	if len(payload) > consoleChatAttachmentMaxBytes {
		return consoleChatAttachmentView{}, http.StatusRequestEntityTooLarge, errors.New(fmt.Sprintf(handler.requestText(r, "附件 %s 超过 %d MiB 限制。", "Attachment %s exceeds the %d MiB limit."), strings.TrimSpace(header.Filename), consoleChatAttachmentMaxBytes>>20))
	}
	mediaType := strings.TrimSpace(header.Header.Get("Content-Type"))
	objectKey := consoleChatAttachmentObjectKey(subject, header.Filename)
	published, err := publisher.UploadBytes(r.Context(), payload, objectKey, mediaType)
	if err != nil {
		return consoleChatAttachmentView{}, http.StatusBadGateway, err
	}
	attachment := consoleChatAttachmentView{
		ID:        newConsoleID("att"),
		Name:      consoleChatAttachmentName(header.Filename, objectKey),
		ObjectKey: path.Clean(strings.TrimPrefix(published.ObjectKey, "/")),
		URL:       strings.TrimSpace(published.PublicURL),
		MediaType: strings.TrimSpace(published.MediaType),
		SizeBytes: published.SizeBytes,
	}
	if normalized, ok := normalizeConsoleChatAttachment(handler.cfg.ChatAttachments, attachment); ok {
		return normalized, http.StatusOK, nil
	}
	return consoleChatAttachmentView{}, http.StatusInternalServerError, errors.New(handler.requestText(r, "附件上传后无法生成可用地址。", "Uploaded attachment does not have a usable public URL."))
}

func consoleChatAttachmentObjectKey(subject, filename string) string {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	if len(extension) > 16 {
		extension = ""
	}
	return path.Join("chat", sanitizeConsoleChatAttachmentPart(subject), time.Now().UTC().Format("2006/01/02"), newConsoleID("upload")+extension)
}

func consoleChatAttachmentName(filename, objectKey string) string {
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename != "" && filename != "." {
		return filename
	}
	return path.Base(strings.TrimSpace(objectKey))
}

func sanitizeConsoleChatAttachmentPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		case char == '-' || char == '_' || char == '.':
			builder.WriteRune(char)
			lastDash = false
		default:
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	cleaned := strings.Trim(builder.String(), "-._")
	if cleaned == "" {
		return "console"
	}
	return cleaned
}

func (handler *Handler) consoleChatAttachmentSubjectPrefix(r *http.Request) (string, error) {
	subject := strings.TrimSpace(currentConsoleSessionSubject(r, handler.cfg.SecretKey))
	if subject == "" {
		return "", errors.New(handler.requestText(r, "当前登录会话无效。", "The current console session is invalid."))
	}
	return sanitizeConsoleChatAttachmentPart(subject), nil
}

func consoleChatAttachmentVisibleKey(cfg artifacts.Config, objectKey string) string {
	normalized := artifacts.NormalizeObjectKey(objectKey)
	prefix := artifacts.NormalizeObjectKey(cfg.S3.Prefix)
	if prefix == "" {
		return normalized
	}
	if normalized == prefix {
		return ""
	}
	if strings.HasPrefix(normalized, prefix+"/") {
		return strings.TrimPrefix(normalized, prefix+"/")
	}
	return normalized
}

func consoleChatAttachmentOwnsObject(cfg artifacts.Config, objectKey string, subjectPrefix string) bool {
	subjectPrefix = artifacts.NormalizeObjectKey(subjectPrefix)
	if subjectPrefix == "" {
		return false
	}
	relativeKey := consoleChatAttachmentVisibleKey(cfg, objectKey)
	if relativeKey == subjectPrefix || strings.HasPrefix(relativeKey, subjectPrefix+"/") {
		return true
	}
	legacyPrefix := path.Join("chat", subjectPrefix)
	return relativeKey == legacyPrefix || strings.HasPrefix(relativeKey, legacyPrefix+"/")
}

func (handler *Handler) writeChatAttachmentResponse(w http.ResponseWriter, statusCode int, response consoleChatAttachmentUploadResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(response)
}
