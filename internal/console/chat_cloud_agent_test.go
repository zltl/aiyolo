package console

import (
	"strings"
	"testing"
)

func TestConsoleCloudAgentCurrentPromptIsInteractive(t *testing.T) {
	prompt := consoleCloudAgentCurrentPrompt("请修改这里，但不确定用哪个方案", nil)

	for _, expected := range []string{
		"interactive collaboration, not a one-shot completion",
		"ask a concise clarification question and stop",
		"The user will answer in the AIYolo chat input",
		"Latest user message:\n请修改这里，但不确定用哪个方案",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("cloud agent prompt is missing %q: %s", expected, prompt)
		}
	}
}

func TestConsoleCloudAgentInitialPromptKeepsTranscriptAndInteractiveContract(t *testing.T) {
	prompt := consoleCloudAgentInitialPrompt([]consoleChatMessageView{
		{Role: "user", Content: "先看一下这个仓库"},
		{Role: "assistant", Content: "需要确认目标文件。"},
	}, "目标文件是 README.md", nil)

	for _, expected := range []string{
		"interactive collaboration, not a one-shot completion",
		"Previous conversation:\nUser:\n先看一下这个仓库",
		"Assistant:\n需要确认目标文件。",
		"Latest user message:\n目标文件是 README.md",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("initial cloud agent prompt is missing %q: %s", expected, prompt)
		}
	}
}
