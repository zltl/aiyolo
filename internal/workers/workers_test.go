package workers

import (
	"strings"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestRenderProxyEnvIncludesAuthMaterial(t *testing.T) {
	env := RenderProxyEnv(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com:8080", Auth: "user:pass"})
	if env["HTTPS_PROXY"] != "http://user:pass@proxy.example.com:8080" {
		t.Fatalf("unexpected https proxy env: %q", env["HTTPS_PROXY"])
	}
	if env["all_proxy"] == "" {
		t.Fatal("expected lowercase all_proxy")
	}
}

func TestBuildBootstrapPlanMentionsSelectedDisks(t *testing.T) {
	plan := BuildBootstrapPlan(
		domain.WorkerServer{ID: "worker-1", SSHHost: "10.0.0.5", SSHUsername: "ubuntu", SSHKeyID: "key-1"},
		[]domain.WorkerDataDisk{{DevicePath: "/dev/vdb", MountPath: "/srv/aiyolo"}},
		domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com:8080"},
	)
	if !strings.Contains(plan.VarsJSON, `"device_path": "/dev/vdb"`) || !strings.Contains(plan.VarsJSON, `"mount_path": "/srv/aiyolo"`) {
		t.Fatalf("bootstrap vars missing disk selection: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"HTTPS_PROXY": "http://proxy.example.com:8080"`) {
		t.Fatalf("bootstrap vars missing proxy env: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_workspace_root": "/var/lib/aiyolo-agent/workspace"`) {
		t.Fatalf("bootstrap vars missing workspace root: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_docker_data_root": "/var/lib/aiyolo-agent/docker"`) {
		t.Fatalf("bootstrap vars missing docker data root: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_runtime_service_name": "aiyolo-workerd"`) {
		t.Fatalf("bootstrap vars missing runtime service name: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.Playbook, "Configure Docker proxy environment") {
		t.Fatalf("bootstrap playbook missing docker proxy task: %s", plan.Playbook)
	}
	if !strings.Contains(plan.Playbook, "Initialize and mount declared worker data disks") || !strings.Contains(plan.Playbook, "Persist Docker daemon data root") || !strings.Contains(plan.Playbook, "Ensure worker runtime service is enabled and restarted") {
		t.Fatalf("bootstrap playbook missing storage or runtime tasks: %s", plan.Playbook)
	}
	if !strings.Contains(plan.Inventory, "ansible_host=10.0.0.5") {
		t.Fatalf("bootstrap inventory missing host: %s", plan.Inventory)
	}
	if !strings.Contains(plan.Summary, "Ansible bootstrap plan prepared") || !strings.Contains(plan.Summary, "1 explicit data disk selection") || !strings.Contains(plan.Summary, "post-init health verification") {
		t.Fatalf("unexpected summary: %s", plan.Summary)
	}
}

func TestInstallProxyEndpointAddressCanonicalizesBareSocks5Endpoint(t *testing.T) {
	host, port, err := installProxyEndpointAddress(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeSOCKS5, Endpoint: "127.0.0.1:10808"})
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 10808 {
		t.Fatalf("host=%q port=%d", host, port)
	}
}

func TestInstallProxyEndpointAddressDefaultsHTTPPort(t *testing.T) {
	host, port, err := installProxyEndpointAddress(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if host != "proxy.example.com" || port != 80 {
		t.Fatalf("host=%q port=%d", host, port)
	}
}

func TestCloudAgentContainerEnvUsesConsoleBaseURLAndCustomModel(t *testing.T) {
	env := cloudAgentContainerEnv(CloudAgentStartOptions{
		UserID:         "user-1",
		AgentType:      domain.CloudAgentTypeClaudeCode,
		APIBaseURL:     "https://aiyolo.quant67.com/v1",
		ConsoleBaseURL: "https://aiyolo.quant67.com",
		APIKey:         "test-key",
		DefaultModel:   "deepseek-v4-pro",
		AllowedModels:  []string{"deepseek-v4-pro"},
	})
	if env["ANTHROPIC_BASE_URL"] != "https://aiyolo.quant67.com" || env["ANTHROPIC_API_URL"] != "https://aiyolo.quant67.com" {
		t.Fatalf("unexpected anthropic urls: %+v", env)
	}
	if env["ANTHROPIC_MODEL"] != "deepseek-v4-pro" {
		t.Fatalf("unexpected anthropic model: %+v", env)
	}
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != "deepseek-v4-pro" {
		t.Fatalf("missing custom model option: %+v", env)
	}
	if env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] != "deepseek-v4-pro" {
		t.Fatalf("unexpected custom model option name: %+v", env)
	}
}

func TestCloudAgentContainerEnvSkipsCustomModelOptionForClaudeModel(t *testing.T) {
	env := cloudAgentContainerEnv(CloudAgentStartOptions{
		UserID:         "user-1",
		AgentType:      domain.CloudAgentTypeClaudeCode,
		APIBaseURL:     "https://aiyolo.quant67.com/v1",
		ConsoleBaseURL: "https://aiyolo.quant67.com",
		APIKey:         "test-key",
		DefaultModel:   "claude-sonnet-4-6",
		AllowedModels:  []string{"claude-sonnet-4-6"},
	})
	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4-6" {
		t.Fatalf("unexpected anthropic model: %+v", env)
	}
	if _, ok := env["ANTHROPIC_CUSTOM_MODEL_OPTION"]; ok {
		t.Fatalf("did not expect custom model option for claude model: %+v", env)
	}
}

func TestBuildCloudAgentClaudeCodeRemoteScriptIncludesSessionRecoveryAndFlags(t *testing.T) {
	script := buildCloudAgentClaudeCodeRemoteScript("aiyolo-cloud-agent-user", "/workspace", CloudAgentClaudeCodeOptions{
		SessionID:     "550e8400-e29b-41d4-a716-446655440000",
		Prompt:        "continue from the current state",
		InitialPrompt: "reconstruct the chat transcript first",
		Model:         "deepseek-v4-pro",
		Stream:        true,
	})
	if !strings.Contains(script, `session_args=(--resume "$session_id")`) {
		t.Fatalf("script should resume an existing claude session: %s", script)
	}
	if !strings.Contains(script, `session_args=(--session-id "$session_id")`) {
		t.Fatalf("script should bootstrap a new claude session when missing: %s", script)
	}
	if !strings.Contains(script, `cmd+=(--output-format stream-json --verbose --include-partial-messages)`) {
		t.Fatalf("script should enable stream-json partial output: %s", script)
	}
	if !strings.Contains(script, `-u 'aiyolo'`) || !strings.Contains(script, `-e HOME='/home/aiyolo'`) {
		t.Fatalf("script should run claude as the non-root cloud-agent user: %s", script)
	}
	if !strings.Contains(script, `cmd+=(--model "$model")`) {
		t.Fatalf("script should forward the selected model: %s", script)
	}
	if strings.Contains(script, `--append-system-prompt`) || strings.Contains(script, `system_prompt=`) {
		t.Fatalf("script should not inject a system prompt into claude code: %s", script)
	}
	if !strings.Contains(script, `cmd=(claude -p "$prompt_to_send" --dangerously-skip-permissions)`) {
		t.Fatalf("script should keep claude permission bypass enabled for agent tool use: %s", script)
	}
	if !strings.Contains(script, `re.sub(r'[^0-9A-Za-z]', '-', os.getcwd())`) {
		t.Fatalf("script should derive the claude project key from cwd: %s", script)
	}
}
