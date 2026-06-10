package console

import (
	"fmt"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

const (
	consoleChatImageGenNone      = ""
	consoleChatImageGenImagesAPI = "images_api"
	consoleChatImageGenChatAPI   = "chat_api"
)

func consoleChatImageModelID(route domain.ModelRoute) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmpty(route.UpstreamModel, route.PublicName)))
}

func consoleChatIsChatCompletionImageModelID(modelID string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return false
	}
	patterns := []string{
		"flux",
		"riverflow",
		"recraft/",
		"mai-image",
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3-flash-image",
		"gpt-5.4-image",
		"gpt-5-image",
		"/image-preview",
		"/image-generation",
		"-image-preview",
	}
	for _, pattern := range patterns {
		if strings.Contains(modelID, pattern) {
			return true
		}
	}
	return false
}

func consoleChatImageGenerationKind(route domain.ModelRoute) string {
	modelID := consoleChatImageModelID(route)
	switch {
	case strings.Contains(modelID, "gpt-image-2") || strings.Contains(modelID, "chatgpt-image-2"):
		return consoleChatImageGenImagesAPI
	case consoleChatIsChatCompletionImageModelID(modelID):
		return consoleChatImageGenChatAPI
	default:
		return consoleChatImageGenNone
	}
}

func consoleChatIsImageGenerationModel(route domain.ModelRoute) bool {
	return consoleChatImageGenerationKind(route) != ""
}

func consoleChatImageGenerationModalities(route domain.ModelRoute) []string {
	modelID := consoleChatImageModelID(route)
	imageOnlyPatterns := []string{"flux", "riverflow", "recraft/", "mai-image", "sourceful/"}
	for _, pattern := range imageOnlyPatterns {
		if strings.Contains(modelID, pattern) {
			return []string{"image"}
		}
	}
	return []string{"image", "text"}
}

func consoleChatCompletionImageURL(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if nested, ok := typed["image_url"].(map[string]any); ok {
			return strings.TrimSpace(consoleValueString(nested["url"]))
		}
		return strings.TrimSpace(consoleValueString(typed["url"]))
	default:
		return ""
	}
}

func consoleOpenAIChatCompletionImageOutput(message map[string]any) string {
	if message == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if images, ok := message["images"].([]any); ok {
		for index, item := range images {
			if url := consoleChatCompletionImageURL(item); url != "" {
				parts = append(parts, fmt.Sprintf("![Generated image %d](%s)", index+1, url))
			}
		}
	}
	if content, ok := message["content"].([]any); ok {
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType := strings.ToLower(consoleValueString(block["type"]))
			switch blockType {
			case "image_url", "output_image", "image":
				if url := consoleChatCompletionImageURL(block); url != "" {
					parts = append(parts, fmt.Sprintf("![Generated image %d](%s)", len(parts)+1, url))
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func consoleChatAssistantOutputWithImages(textOutput string, message map[string]any) string {
	textOutput = strings.TrimSpace(textOutput)
	imageOutput := strings.TrimSpace(consoleOpenAIChatCompletionImageOutput(message))
	switch {
	case imageOutput != "" && textOutput != "" && textOutput != consoleChatEmptyOutput:
		return textOutput + "\n\n" + imageOutput
	case imageOutput != "":
		return imageOutput
	default:
		return textOutput
	}
}

func consoleChatStreamImageDelta(value any) string {
	switch typed := value.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		for index, item := range typed {
			if url := consoleChatCompletionImageURL(item); url != "" {
				parts = append(parts, fmt.Sprintf("![Generated image %d](%s)", index+1, url))
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}
