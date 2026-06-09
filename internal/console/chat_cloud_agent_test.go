package console

import (
	"context"
	"strings"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
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

func TestConsoleCloudAgentInitialPromptUsesClaudeSessionState(t *testing.T) {
	prompt := consoleCloudAgentInitialPrompt([]consoleChatMessageView{
		{Role: "user", Content: "先看一下这个仓库"},
		{Role: "assistant", Content: "需要确认目标文件。"},
	}, "目标文件是 README.md", nil)

	for _, expected := range []string{
		"interactive collaboration, not a one-shot completion",
		"Latest user message:\n目标文件是 README.md",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("initial cloud agent prompt is missing %q: %s", expected, prompt)
		}
	}
	for _, unexpected := range []string{"Previous conversation", "先看一下这个仓库", "需要确认目标文件。"} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("initial cloud agent prompt should rely on Claude session state, found %q in %s", unexpected, prompt)
		}
	}
}

func TestConsoleCloudAgentCodexParserConsumesJSONLines(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"550e8400-e29b-41d4-a716-446655440000"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Claude Code finished."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
	}, "\n")

	if err := parser.consumeJSONResult(raw); err != nil {
		t.Fatal(err)
	}
	if parser.sessionID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("sessionID=%q", parser.sessionID)
	}
	if got := parser.resultText(); got != "Claude Code finished." {
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

func TestConsoleCloudAgentParserStreamsClaudeAssistantText(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var deltas []string
	err := parser.consumeLine(`{"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"I'll inspect the repo."}],"usage":{"input_tokens":4,"output_tokens":5}}}`, consoleCloudAgentStreamHandlers{
		OnDelta: func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "I'll inspect the repo." {
		t.Fatalf("deltas=%q", got)
	}
	if got := parser.resultText(); got != "I'll inspect the repo." {
		t.Fatalf("resultText=%q", got)
	}
	if parser.inputTokens != 4 || parser.outputTokens != 5 || parser.totalTokens != 9 {
		t.Fatalf("unexpected usage input=%d output=%d total=%d", parser.inputTokens, parser.outputTokens, parser.totalTokens)
	}
}

func TestConsoleCloudAgentParserStreamsClaudeToolSteps(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var reasoningChunks []string
	handlers := consoleCloudAgentStreamHandlers{
		OnReasoning: func(reasoning string) error {
			reasoningChunks = append(reasoningChunks, reasoning)
			return nil
		},
	}
	if err := parser.consumeLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"rg claude internal/console"}}]}}`, handlers); err != nil {
		t.Fatal(err)
	}
	if err := parser.consumeLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"internal/console/chat_cloud_agent.go: cmd=(claude -p)"}]}}`, handlers); err != nil {
		t.Fatal(err)
	}
	reasoning := strings.Join(reasoningChunks, "")
	for _, expected := range []string{"Tool Bash", "rg claude internal/console", "completed", "chat_cloud_agent.go"} {
		if !strings.Contains(reasoning, expected) {
			t.Fatalf("reasoning missing %q: %s", expected, reasoning)
		}
	}
	if got := parser.resultText(); got != "" {
		t.Fatalf("tool events should not become assistant output: %q", got)
	}
}

func TestConsoleCloudAgentCodexParserConsumesResponsesCompleted(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var deltas []string
	err := parser.consumeLine(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","finish_reason":"stop","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer"}]},{"type":"reasoning","summary":[{"type":"summary_text","text":"analysis"}]}]}}`, consoleCloudAgentStreamHandlers{
		OnDelta: func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "final answer" {
		t.Fatalf("deltas=%q", got)
	}
	if got := parser.resultText(); got != "final answer" {
		t.Fatalf("resultText=%q", got)
	}
	if got := parser.reasoningText(); got != "analysis" {
		t.Fatalf("reasoningText=%q", got)
	}
	if parser.inputTokens != 3 || parser.outputTokens != 2 || parser.totalTokens != 5 {
		t.Fatalf("unexpected usage input=%d output=%d total=%d", parser.inputTokens, parser.outputTokens, parser.totalTokens)
	}
}

func TestConsoleCloudAgentCodexParserConsumesResponsesOutputDelta(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var deltas []string
	err := parser.consumeLine(`{"type":"response.output_text.delta","delta":"hello"}`, consoleCloudAgentStreamHandlers{
		OnDelta: func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(deltas, ""); got != "hello" {
		t.Fatalf("deltas=%q", got)
	}
	if got := parser.resultText(); got != "hello" {
		t.Fatalf("resultText=%q", got)
	}
}

func TestConsoleCloudAgentCodexParserConsumesResponsesOutputItemDone(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	err := parser.consumeLine(`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done text"}]}}`, consoleCloudAgentStreamHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	if got := parser.resultText(); got != "done text" {
		t.Fatalf("resultText=%q", got)
	}
}

func TestConsoleCloudAgentCodexParserConsumesResponsesContentPartDone(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	err := parser.consumeLine(`{"type":"response.content_part.done","part":{"type":"output_text","text":"part text"}}`, consoleCloudAgentStreamHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	if got := parser.resultText(); got != "part text" {
		t.Fatalf("resultText=%q", got)
	}
}

func TestConsoleCloudAgentClaudeSessionIDRequiresUUID(t *testing.T) {
	if got := consoleCloudAgentClaudeSessionID("chatcmpl-local-response"); got != "" {
		t.Fatalf("local response id should not be treated as codex thread id: %q", got)
	}
	if got := consoleCloudAgentClaudeSessionID("550e8400-e29b-41d4-a716-446655440000"); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("claude session id=%q", got)
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

func TestConsoleCloudAgentRunExecutePassesPreviousResponseID(t *testing.T) {
	const previousResponseID = "550e8400-e29b-41d4-a716-446655440000"
	var capturedPreviousResponseID string
	handler := NewHandler(Config{SecretKey: "test-secret"}, storage.NewMemoryStore())
	handler.runCloudAgentChat = func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.CloudAgentAccount, _ domain.CloudAgentSession, request consoleCloudAgentChatRequest) (consoleChatExecution, error) {
		capturedPreviousResponseID = request.PreviousResponseID
		return consoleChatExecution{}, nil
	}
	run := &consoleCloudAgentRun{
		handler:   handler,
		registry:  handler.cloudAgentRuns,
		key:       "test-run",
		locale:    "zh-CN",
		userID:    "user@test",
		sessionID: "session-test",
		request: consoleCloudAgentChatRequest{
			PreviousResponseID: previousResponseID,
		},
	}
	run.execute()
	if capturedPreviousResponseID != previousResponseID {
		t.Fatalf("PreviousResponseID = %q, want %q", capturedPreviousResponseID, previousResponseID)
	}
}
