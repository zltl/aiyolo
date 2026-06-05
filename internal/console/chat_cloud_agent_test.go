package console

import (
	"strings"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
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

func TestConsoleCloudAgentCodexParserConsumesJSONLines(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"550e8400-e29b-41d4-a716-446655440000"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Codex finished."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
	}, "\n")

	if err := parser.consumeJSONResult(raw); err != nil {
		t.Fatal(err)
	}
	if parser.sessionID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("sessionID=%q", parser.sessionID)
	}
	if got := parser.resultText(); got != "Codex finished." {
		t.Fatalf("resultText=%q", got)
	}
	if parser.inputTokens != 11 || parser.outputTokens != 7 || parser.totalTokens != 18 {
		t.Fatalf("unexpected usage input=%d output=%d total=%d", parser.inputTokens, parser.outputTokens, parser.totalTokens)
	}
}

func TestConsoleCloudAgentCodexParserStreamsCompletedMessageWhenNoDelta(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var deltas []string
	err := parser.consumeLine(`{"type":"item.completed","item":{"type":"agent_message","content":[{"type":"output_text","text":"final text"}]}}`, consoleCloudAgentStreamHandlers{
		OnDelta: func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "final text" {
		t.Fatalf("deltas=%q", deltas)
	}
}

func TestConsoleCloudAgentCodexParserStreamsReasoning(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var reasoningChunks []string
	err := parser.consumeLine(`{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"Checking repo layout"}}`, consoleCloudAgentStreamHandlers{
		OnReasoning: func(reasoning string) error {
			reasoningChunks = append(reasoningChunks, reasoning)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(reasoningChunks, "") != "Checking repo layout" {
		t.Fatalf("reasoning chunks=%q", reasoningChunks)
	}
	if got := parser.reasoningText(); got != "Checking repo layout" {
		t.Fatalf("reasoningText=%q", got)
	}
}

func TestConsoleCloudAgentCodexThreadIDRequiresUUID(t *testing.T) {
	if got := consoleCloudAgentCodexThreadID("chatcmpl-local-response"); got != "" {
		t.Fatalf("local response id should not be treated as codex thread id: %q", got)
	}
	if got := consoleCloudAgentCodexThreadID("550e8400-e29b-41d4-a716-446655440000"); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("codex thread id=%q", got)
	}
}

func TestConsoleCloudAgentWorkingDirectoryUsesActiveTerminalCWD(t *testing.T) {
	cloudSession := domain.CloudAgentSession{
		ChatSessionID:  "session-shell",
		WorkspacePath:  "/workspace",
		ShellStateJSON: `{"activeTerminalID":"term-two","instances":[{"terminalID":"term-one","sessionID":"session-shell","meta":{"currentWorkingDirectory":"/workspace/one"}},{"terminalID":"term-two","sessionID":"session-shell","meta":{"currentWorkingDirectory":"/workspace/two"}}]}`,
	}
	account := domain.CloudAgentAccount{
		WorkerID:      "worker-0",
		ContainerName: "aiyolo-cloud-agent-worker-0",
		WorkspacePath: "/workspace",
	}

	if got := consoleCloudAgentWorkingDirectory(account, cloudSession, "term-two", ""); got != "/workspace/two" {
		t.Fatalf("working directory = %q, want active terminal cwd", got)
	}
	if got := consoleCloudAgentWorkingDirectory(account, cloudSession, "term-one", "/workspace/submitted"); got != "/workspace/submitted" {
		t.Fatalf("working directory = %q, want submitted cwd", got)
	}
	if got := consoleCloudAgentWorkingDirectory(account, cloudSession, "term-one", "relative/path"); got != "/workspace/one" {
		t.Fatalf("working directory = %q, want persisted terminal cwd fallback", got)
	}
}
