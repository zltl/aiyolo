package console

import (
	"strings"
	"testing"
)

func TestConsoleCloudAgentBuildToolUseOperation(t *testing.T) {
	op := consoleCloudAgentBuildToolUseOperation(map[string]any{
		"id":   "toolu_1",
		"name": "Bash",
		"input": map[string]any{
			"command": "curl -s https://example.com/docs",
		},
	})
	if op.ID != "toolu_1" || op.Name != "Bash" || op.Status != "started" {
		t.Fatalf("unexpected operation: %+v", op)
	}
	if op.Category != "browser" {
		t.Fatalf("category=%q, want browser", op.Category)
	}
	if op.URL != "https://example.com/docs" {
		t.Fatalf("url=%q", op.URL)
	}
}

func TestConsoleCloudAgentBuildToolResultOperation(t *testing.T) {
	toolNames := map[string]string{"toolu_1": "Read"}
	op := consoleCloudAgentBuildToolResultOperation(map[string]any{
		"tool_use_id": "toolu_1",
		"content":     "file contents",
	}, toolNames)
	if op.Name != "Read" || op.Status != "completed" {
		t.Fatalf("unexpected operation: %+v", op)
	}
	if op.Category != "file" {
		t.Fatalf("category=%q, want file", op.Category)
	}
}

func TestConsoleCloudAgentParserEmitsOperationEvents(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var operations []consoleChatStreamOperation
	handlers := consoleCloudAgentStreamHandlers{
		OnOperation: func(operation consoleChatStreamOperation) error {
			operations = append(operations, operation)
			return nil
		},
	}
	if err := parser.consumeLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"WebFetch","input":{"url":"https://docs.example.com"}}]}}`, handlers); err != nil {
		t.Fatal(err)
	}
	if err := parser.consumeLine(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}`, handlers); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 {
		t.Fatalf("operations=%d, want 2", len(operations))
	}
	if operations[0].Category != "browser" || operations[0].URL != "https://docs.example.com" {
		t.Fatalf("start operation=%+v", operations[0])
	}
	if operations[1].Status != "completed" {
		t.Fatalf("result operation=%+v", operations[1])
	}
}

func TestConsoleCloudAgentParserOperationEventsKeepReasoning(t *testing.T) {
	parser := &consoleCloudAgentStreamParser{}
	var reasoningChunks []string
	handlers := consoleCloudAgentStreamHandlers{
		OnReasoning: func(reasoning string) error {
			reasoningChunks = append(reasoningChunks, reasoning)
			return nil
		},
		OnOperation: func(operation consoleChatStreamOperation) error {
			return nil
		},
	}
	if err := parser.consumeLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Grep","input":{"pattern":"browser"}}]}}`, handlers); err != nil {
		t.Fatal(err)
	}
	reasoning := strings.Join(reasoningChunks, "")
	if !strings.Contains(reasoning, "Tool Grep") {
		t.Fatalf("reasoning missing tool step: %s", reasoning)
	}
}
