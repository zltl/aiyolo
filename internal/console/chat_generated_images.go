package console

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/domain"
)

var consoleChatMarkdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

func consoleChatGeneratedImageObjectKey(subject string, index int, mediaType string) string {
	extension := consoleChatGeneratedImageExtension(mediaType)
	return path.Join(
		"chat",
		sanitizeConsoleChatAttachmentPart(subject),
		"generated",
		time.Now().UTC().Format("2006/01/02"),
		fmt.Sprintf("%s-%d%s", newConsoleID("img"), index+1, extension),
	)
}

func consoleChatGeneratedImageExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/svg+xml":
		return ".svg"
	default:
		return ".png"
	}
}

func decodeConsoleChatDataImageURL(raw string) (string, []byte, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return "", nil, false
	}
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return "", nil, false
	}
	header := strings.TrimSpace(raw[5:comma])
	data := strings.TrimSpace(raw[comma+1:])
	if data == "" || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", nil, false
	}
	mediaType := strings.TrimSuffix(strings.TrimSpace(header), ";base64")
	if mediaType == "" {
		mediaType = "image/png"
	}
	payload, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", nil, false
	}
	return mediaType, payload, true
}

func (handler *Handler) consoleChatImageURLAlreadyPersisted(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return true
	}
	cfg := handler.cfg.ChatAttachments
	publicBase := strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	if publicBase != "" && strings.HasPrefix(rawURL, publicBase+"/") {
		return true
	}
	if publicBase := strings.TrimRight(strings.TrimSpace(cfg.PublicBase()), "/"); publicBase != "" && strings.HasPrefix(rawURL, publicBase+"/") {
		return true
	}
	proxyBase := strings.TrimRight(strings.TrimSpace(cfg.NormalizedProxyBasePath()), "/")
	if proxyBase != "" && (strings.HasPrefix(rawURL, proxyBase+"/") || strings.Contains(rawURL, proxyBase+"/")) {
		return true
	}
	return false
}

func (handler *Handler) loadGeneratedChatImagePayload(ctx context.Context, rawURL string) ([]byte, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, "", fmt.Errorf("image url is empty")
	}
	if mediaType, payload, ok := decodeConsoleChatDataImageURL(rawURL); ok {
		if mediaType == "" {
			mediaType = http.DetectContentType(payload)
		}
		return payload, mediaType, nil
	}
	parsed, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: 45 * time.Second}
	response, err := client.Do(parsed)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("download generated image: %s", response.Status)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, int64(consoleChatAttachmentMaxBytes)+1))
	if err != nil {
		return nil, "", err
	}
	if len(payload) == 0 {
		return nil, "", fmt.Errorf("downloaded generated image is empty")
	}
	if len(payload) > consoleChatAttachmentMaxBytes {
		return nil, "", fmt.Errorf("downloaded generated image exceeds %d MiB", consoleChatAttachmentMaxBytes>>20)
	}
	mediaType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = http.DetectContentType(payload)
	}
	if semicolon := strings.Index(mediaType, ";"); semicolon >= 0 {
		mediaType = strings.TrimSpace(mediaType[:semicolon])
	}
	return payload, mediaType, nil
}

func (handler *Handler) persistGeneratedChatImageOutput(ctx context.Context, subject string, output string) string {
	if !handler.cfg.ChatAttachments.CanUpload() {
		return output
	}
	subject = sanitizeConsoleChatAttachmentPart(subject)
	output = strings.TrimSpace(output)
	if subject == "" || output == "" || output == consoleChatEmptyOutput {
		return output
	}
	matches := consoleChatMarkdownImagePattern.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return output
	}
	publisher, err := handler.newChatAttachmentPublisher(handler.cfg.ChatAttachments)
	if err != nil {
		log.Printf("console chat generated image persist skipped subject=%s err=%v", subject, err)
		return output
	}

	result := output
	uploaded := 0
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		rawURL := strings.TrimSpace(output[match[4]:match[5]])
		if rawURL == "" || handler.consoleChatImageURLAlreadyPersisted(rawURL) {
			continue
		}
		payload, mediaType, err := handler.loadGeneratedChatImagePayload(ctx, rawURL)
		if err != nil {
			log.Printf("console chat generated image load_failed subject=%s url=%q err=%v", subject, truncateConsoleChatImageURL(rawURL), err)
			continue
		}
		objectKey := consoleChatGeneratedImageObjectKey(subject, uploaded, mediaType)
		published, err := publisher.UploadBytes(ctx, payload, objectKey, mediaType)
		if err != nil {
			log.Printf("console chat generated image upload_failed subject=%s object_key=%s err=%v", subject, objectKey, err)
			continue
		}
		browserURL := consoleChatAttachmentBrowserURL(handler.cfg.ChatAttachments, objectKey)
		if browserURL == "" {
			browserURL = strings.TrimSpace(published.PublicURL)
		}
		if browserURL == "" {
			log.Printf("console chat generated image upload_missing_url subject=%s object_key=%s", subject, objectKey)
			continue
		}
		result = strings.Replace(result, rawURL, browserURL, 1)
		uploaded++
	}
	return result
}

func rewriteChatAssetMarkdownURLs(cfg artifacts.Config, output string) string {
	publicBase := strings.TrimRight(cfg.PublicBase(), "/")
	if publicBase == "" {
		return output
	}
	matches := consoleChatMarkdownImagePattern.FindAllStringSubmatchIndex(output, -1)
	if len(matches) == 0 {
		return output
	}
	result := output
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		rawURL := strings.TrimSpace(output[match[4]:match[5]])
		if rawURL == "" || strings.HasPrefix(rawURL, "/") {
			continue
		}
		if !strings.HasPrefix(rawURL, publicBase+"/") {
			continue
		}
		objectKey := artifacts.NormalizeObjectKey(strings.TrimPrefix(rawURL, publicBase+"/"))
		browserURL := consoleChatAttachmentBrowserURL(cfg, objectKey)
		if browserURL == "" {
			continue
		}
		result = strings.Replace(result, rawURL, browserURL, 1)
	}
	return result
}

func truncateConsoleChatImageURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "data:") {
		if len(rawURL) <= 48 {
			return rawURL
		}
		return rawURL[:48] + "..."
	}
	return rawURL
}

type consoleChatPersistedStreamOutput struct {
	value string
	set   bool
}

func (handler *Handler) applyGeneratedChatImagePersistence(ctx context.Context, route domain.ModelRoute, subject string, stream bool, onDelta func(string) error) (func(string) error, *consoleChatPersistedStreamOutput) {
	if !consoleChatIsImageGenerationModel(route) || !handler.cfg.ChatAttachments.CanUpload() || strings.TrimSpace(subject) == "" {
		return onDelta, nil
	}
	holder := &consoleChatPersistedStreamOutput{}
	wrappedDelta := onDelta
	if stream && onDelta != nil {
		wrappedDelta = func(delta string) error {
			holder.value = handler.persistGeneratedChatImageOutput(ctx, subject, delta)
			holder.set = true
			return onDelta(holder.value)
		}
	}
	return wrappedDelta, holder
}

func (handler *Handler) finalizeGeneratedChatImageOutput(ctx context.Context, route domain.ModelRoute, subject string, execution *consoleChatExecution, streamedOutput *consoleChatPersistedStreamOutput) {
	if execution == nil || !consoleChatIsImageGenerationModel(route) {
		return
	}
	if streamedOutput != nil && streamedOutput.set {
		execution.Result.Output = streamedOutput.value
		return
	}
	execution.Result.Output = handler.persistGeneratedChatImageOutput(ctx, subject, execution.Result.Output)
}
