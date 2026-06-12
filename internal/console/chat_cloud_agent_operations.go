package console

import (
	"fmt"
	"regexp"
	"strings"
)

var consoleCloudAgentHTTPURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

type consoleChatStreamOperation struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
	Category      string `json:"category,omitempty"`
	URL           string `json:"url,omitempty"`
	ScreenshotURL string `json:"screenshotUrl,omitempty"`
}

func consoleCloudAgentBuildToolUseOperation(block map[string]any) consoleChatStreamOperation {
	name := strings.TrimSpace(stringValue(block["name"]))
	if name == "" {
		name = "tool"
	}
	id := strings.TrimSpace(stringValue(block["id"]))
	if id == "" {
		id = fmt.Sprintf("tool-%s", name)
	}
	input, _ := block["input"].(map[string]any)
	detail := claudeToolInputSummary(input)
	category := consoleCloudAgentOperationCategory(name, input)
	targetURL := consoleCloudAgentOperationURL(name, input, detail)
	return consoleChatStreamOperation{
		ID:       id,
		Name:     name,
		Status:   "started",
		Detail:   detail,
		Category: category,
		URL:      targetURL,
	}
}

func consoleCloudAgentBuildToolResultOperation(block map[string]any, toolNames map[string]string) consoleChatStreamOperation {
	id := strings.TrimSpace(stringValue(block["tool_use_id"]))
	name := "tool"
	if id != "" && toolNames != nil {
		name = firstNonEmpty(strings.TrimSpace(toolNames[id]), name)
	}
	status := "completed"
	if isTrueValue(block["is_error"]) {
		status = "failed"
	}
	summary := strings.TrimSpace(claudeToolResultSummary(block["content"]))
	if id == "" {
		id = fmt.Sprintf("tool-result-%s", name)
	}
	return consoleChatStreamOperation{
		ID:       id,
		Name:     name,
		Status:   status,
		Detail:   summary,
		Category: consoleCloudAgentOperationCategory(name, nil),
	}
}

func consoleCloudAgentOperationCategory(name string, input map[string]any) string {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(nameLower, "browser"), nameLower == "webfetch", nameLower == "web_fetch":
		return "browser"
	case nameLower == "bash", nameLower == "shell":
		command := strings.ToLower(stringValue(input["command"]))
		if strings.Contains(command, "curl ") ||
			strings.Contains(command, "wget ") ||
			strings.Contains(command, "open-chrome") ||
			strings.Contains(command, "chromium") ||
			strings.Contains(command, "firefox") ||
			consoleCloudAgentExtractHTTPURL(command) != "" {
			return "browser"
		}
		return "shell"
	case nameLower == "read", nameLower == "write", nameLower == "edit", nameLower == "glob", nameLower == "grep", nameLower == "notebookedit":
		return "file"
	case strings.Contains(nameLower, "search"):
		return "search"
	default:
		return "tool"
	}
}

func consoleCloudAgentOperationURL(name string, input map[string]any, detail string) string {
	if len(input) > 0 {
		for _, key := range []string{"url", "uri", "href", "link"} {
			if target := consoleCloudAgentNormalizeURL(stringValue(input[key])); target != "" {
				return target
			}
		}
		if command := strings.TrimSpace(stringValue(input["command"])); command != "" {
			if target := consoleCloudAgentExtractHTTPURL(command); target != "" {
				return target
			}
		}
	}
	if target := consoleCloudAgentExtractHTTPURL(detail); target != "" {
		return target
	}
	_ = name
	return ""
}

func consoleCloudAgentExtractHTTPURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	match := consoleCloudAgentHTTPURLPattern.FindString(value)
	return consoleCloudAgentNormalizeURL(match)
}

func consoleCloudAgentNormalizeURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, ".,;)]}\"'")
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}
