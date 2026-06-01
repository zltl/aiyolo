package workers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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
		WorkspacePath:  domain.DefaultCloudAgentWorkspacePath,
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
	if env["AIYOLO_ASS_WORKSPACE_ROOT"] != "/workspace" || env["AIYOLO_ASS_USER"] != "aiyolo" || env["AIYOLO_ASS_SOCKET_PATH"] != cloudAgentASSSocketPath {
		t.Fatalf("missing aiyolo-ass container env: %+v", env)
	}
	if env["AIYOLO_ASS_HTTP_ADDR"] != "0.0.0.0:17811" {
		t.Fatalf("unexpected aiyolo-ass http addr: %+v", env)
	}
}

func TestCloudAgentRemoteEnsurePublishesASSOnWorkerLoopback(t *testing.T) {
	script := cloudAgentAssetString("cloud-agent/remote_ensure.py.tmpl")

	if !strings.Contains(script, `"ass_base_url"`) || !strings.Contains(script, `payload['host_ass_port']`) {
		t.Fatalf("cloud-agent summary should report the ASS loopback URL: %s", script)
	}
	if !strings.Contains(script, `"-p", f"127.0.0.1:{payload['host_ass_port']}:{payload['container_ass_port']}"`) {
		t.Fatalf("cloud-agent container should publish ASS only on worker loopback: %s", script)
	}
	if !strings.Contains(script, `wait_for_tcp("127.0.0.1", payload["host_ass_port"], 30)`) {
		t.Fatalf("cloud-agent ensure should wait for the ASS loopback listener: %s", script)
	}
}

func TestCloudAgentDockerfileInstallsASS(t *testing.T) {
	dockerfile := cloudAgentAssetString("cloud-agent/Dockerfile")
	if !strings.Contains(dockerfile, "COPY aiyolo-ass /usr/local/bin/aiyolo-ass") {
		t.Fatalf("cloud-agent Dockerfile should copy aiyolo-ass: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, cloudAgentImageASSSHA256Label) || !strings.Contains(dockerfile, cloudAgentImageBuildRevisionLabel) {
		t.Fatalf("cloud-agent Dockerfile should label aiyolo-ass builds: %s", dockerfile)
	}
	startServicesScript := cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-start-services")
	if !strings.Contains(startServicesScript, "/usr/local/bin/aiyolo-ass") {
		t.Fatalf("cloud-agent services should start aiyolo-ass: %s", startServicesScript)
	}
	if _, ok := cloudAgentBuildContextFiles["aiyolo-ass"]; ok {
		t.Fatal("cloud-agent build context should no longer embed the aiyolo-ass fallback script")
	}
}

func TestCloudAgentDockerfileDefaultsToAiyoloWithPasswordlessSudo(t *testing.T) {
	dockerfile := cloudAgentAssetString("cloud-agent/Dockerfile")
	if !strings.Contains(dockerfile, "USER ${AIYOLO_CLAUDE_USER}") {
		t.Fatalf("cloud-agent Dockerfile should default to the aiyolo user: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "NOPASSWD:ALL") || !strings.Contains(dockerfile, "/etc/sudoers.d/aiyolo") {
		t.Fatalf("cloud-agent Dockerfile should allow passwordless sudo for aiyolo: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "usermod -aG sudo,docker") {
		t.Fatalf("cloud-agent Dockerfile should add aiyolo to sudo and docker groups: %s", dockerfile)
	}
}

func TestCloudAgentDockerfileInstallsCommonDeveloperTools(t *testing.T) {
	dockerfile := cloudAgentAssetString("cloud-agent/Dockerfile")
	requiredPackages := []string{
		"build-essential",
		"cmake",
		"dnsutils",
		"fd-find",
		"gdb",
		"git",
		"git-lfs",
		"gnupg",
		"htop",
		"lsof",
		"nano",
		"net-tools",
		"openssh-client",
		"pkg-config",
		"ripgrep",
		"strace",
		"tmux",
		"tree",
		"vim",
	}
	for _, packageName := range requiredPackages {
		if !dockerfileInstallsPackage(dockerfile, packageName) {
			t.Fatalf("cloud-agent Dockerfile should install %s: %s", packageName, dockerfile)
		}
	}

	var manifest struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal([]byte(cloudAgentAssetString("cloud-agent/cloud-agent-base.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	features := make(map[string]bool, len(manifest.Features))
	for _, feature := range manifest.Features {
		features[feature] = true
	}
	for _, packageName := range requiredPackages {
		if !features[packageName] {
			t.Fatalf("cloud-agent manifest should include %s: %+v", packageName, manifest.Features)
		}
	}

	infoScript := cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-info")
	requiredInfoCommands := []string{
		"git --version",
		"git lfs version",
		"vim --version",
		"nano --version",
		"ssh -V",
		"gcc --version",
		"g++ --version",
		"make --version",
		"cmake --version",
		"pkg-config --version",
		"gdb --version",
		"rg --version",
		"fd --version",
		"tree --version",
		"tmux -V",
		"htop --version",
		"strace -V",
	}
	for _, command := range requiredInfoCommands {
		if !strings.Contains(infoScript, command) {
			t.Fatalf("cloud-agent info script should report %s: %s", command, infoScript)
		}
	}
}

func dockerfileInstallsPackage(dockerfile string, packageName string) bool {
	normalized := strings.NewReplacer("\\", " ", "\n", " ", "\t", " ").Replace(dockerfile)
	for _, token := range strings.Fields(normalized) {
		if token == packageName {
			return true
		}
	}
	return false
}

func TestResolveCloudAgentASSSHA256(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/linux-amd64/aiyolo-ass.sha256" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  aiyolo-ass\n"))
	}))
	defer server.Close()

	checksum, err := resolveCloudAgentASSSHA256(context.Background(), server.URL+"/linux-amd64/aiyolo-ass.sha256")
	if err != nil {
		t.Fatal(err)
	}
	if checksum != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("checksum=%q", checksum)
	}
}

func TestDecodeCloudAgentASSResponseReportsMissingEndpoint(t *testing.T) {
	err := decodeCloudAgentASSResponse(http.MethodPut, "/v1/fs/directory", http.StatusNotFound, []byte("404 page not found\n"), nil)
	if err == nil {
		t.Fatal("expected missing endpoint error")
	}
	message := err.Error()
	if !strings.Contains(message, "aiyolo-ass endpoint not available") || !strings.Contains(message, "PUT /v1/fs/directory") || !strings.Contains(message, "404 page not found") {
		t.Fatalf("unexpected missing endpoint error: %v", err)
	}
	if strings.Contains(message, "parse aiyolo-ass response") {
		t.Fatalf("missing endpoint should not be reported as a JSON parse failure: %v", err)
	}
}

func TestCreateCloudAgentWorkspaceDirectoryFallsBackToShellWhenEndpointMissing(t *testing.T) {
	var calls []string
	runner := func(_ context.Context, _ domain.WorkerServer, _ domain.WorkerSSHKey, _ domain.CloudAgentAccount, _ domain.CloudAgentSession, method string, endpointPath string, _ url.Values, body any, data any) error {
		calls = append(calls, method+" "+endpointPath)
		switch len(calls) {
		case 1:
			request, ok := body.(struct {
				Path   string `json:"path"`
				MkdirP bool   `json:"mkdir_p"`
			})
			if !ok || request.Path != "src/assets" || !request.MkdirP {
				t.Fatalf("unexpected directory request: %#v", body)
			}
			return errors.New("aiyolo-ass endpoint not available: PUT /v1/fs/directory returned HTTP 404: 404 page not found")
		case 2:
			request, ok := body.(CloudAgentShellExecRequest)
			if !ok {
				t.Fatalf("fallback request type = %T", body)
			}
			if request.Mode != "bash" || !strings.Contains(request.Script, "mkdir -p -- \"$target\"") || !strings.Contains(request.Script, "target='src/assets'") {
				t.Fatalf("unexpected fallback request: %+v", request)
			}
			result, ok := data.(*CloudAgentShellExecResult)
			if !ok {
				t.Fatalf("fallback data type = %T", data)
			}
			*result = CloudAgentShellExecResult{ExitCode: 0}
			return nil
		default:
			t.Fatalf("unexpected extra ASS call: %s %s", method, endpointPath)
			return nil
		}
	}

	result, err := createCloudAgentWorkspaceDirectory(context.Background(), domain.WorkerServer{}, domain.WorkerSSHKey{}, domain.CloudAgentAccount{}, domain.CloudAgentSession{}, "src/assets", true, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "src/assets" {
		t.Fatalf("fallback result path=%q", result.Path)
	}
	if strings.Join(calls, ",") != "PUT /v1/fs/directory,POST /v1/shell/exec" {
		t.Fatalf("unexpected ASS calls: %v", calls)
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

func TestNormalizeCloudAgentStartOptionsSanitizesEmailContainerNames(t *testing.T) {
	options, err := normalizeCloudAgentStartOptions(domain.WorkerServer{
		ID:          "worker-0",
		SSHHost:     "10.0.0.5",
		SSHUsername: "ubuntu",
		SSHKeyID:    "ssh-key-1",
		DataRoot:    "/srv/aiyolo",
	}, CloudAgentStartOptions{
		UserID:         "i@quant67.com",
		ContainerName:  "aiyolo-cloud-agent-i@quant67.com",
		APIBaseURL:     "https://aiyolo.quant67.com/v1",
		ConsoleBaseURL: "https://aiyolo.quant67.com",
		APIKey:         "test-key",
		ASSDownloadURL: "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass",
		ASSSHA256URL:   "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass.sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.ContainerName != "aiyolo-cloud-agent-i-quant67-com" {
		t.Fatalf("ContainerName=%q, want sanitized Docker-safe name", options.ContainerName)
	}
	if !strings.Contains(options.WorkspaceRoot, "/cloud-agents/i-quant67-com/workspace") {
		t.Fatalf("WorkspaceRoot should use sanitized user segment, got %q", options.WorkspaceRoot)
	}
	if options.ContainerASSPort != defaultCloudAgentContainerASSPort || options.HostASSPort != cloudAgentHostPort("i@quant67.com", "worker-0-ass", defaultCloudAgentHostASSBasePort) {
		t.Fatalf("unexpected ASS ports container=%d host=%d", options.ContainerASSPort, options.HostASSPort)
	}
}

func TestNormalizeCloudAgentStartOptionsKeepsValidCustomContainerName(t *testing.T) {
	options, err := normalizeCloudAgentStartOptions(domain.WorkerServer{
		ID:          "worker-0",
		SSHHost:     "10.0.0.5",
		SSHUsername: "ubuntu",
		SSHKeyID:    "ssh-key-1",
		DataRoot:    "/srv/aiyolo",
	}, CloudAgentStartOptions{
		UserID:         "i@quant67.com",
		ContainerName:  "custom.agent_1",
		APIBaseURL:     "https://aiyolo.quant67.com/v1",
		ConsoleBaseURL: "https://aiyolo.quant67.com",
		APIKey:         "test-key",
		ASSDownloadURL: "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass",
		ASSSHA256URL:   "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass.sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.ContainerName != "custom.agent_1" {
		t.Fatalf("ContainerName=%q, want valid custom name preserved", options.ContainerName)
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
	if !strings.Contains(script, `-u 'aiyolo'`) || !strings.Contains(script, `-e HOME='/workspace'`) {
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

func TestBuildCloudAgentShellCommandRunsAsNonRootUser(t *testing.T) {
	script := buildCloudAgentShellCommand("aiyolo-cloud-agent-user", "/workspace", "claude-session-1", "gpt-5.4")

	if !regexp.MustCompile(`(?m)-u .*aiyolo`).MatchString(script) {
		t.Fatalf("shell command should exec as the non-root cloud-agent user: %s", script)
	}
	if regexp.MustCompile(`(?m)-u .*root`).MatchString(script) {
		t.Fatalf("shell command should not exec as root: %s", script)
	}
	if !regexp.MustCompile(`(?m)-e HOME=.*?/workspace`).MatchString(script) || !regexp.MustCompile(`(?m)-e USER=.*?aiyolo`).MatchString(script) {
		t.Fatalf("shell command should export the non-root user environment: %s", script)
	}
	if !strings.Contains(script, `-w "$workspace_path"`) {
		t.Fatalf("shell command should preserve the workspace directory: %s", script)
	}
	if !strings.Contains(script, `exec bash --rcfile "$aiyolo_shell_rc" -i`) {
		t.Fatalf("shell command should launch an interactive bash shell with a color-aware rcfile: %s", script)
	}
	for _, expected := range []string{`-e TERM=xterm-256color`, `-e COLORTERM=truecolor`, `-e CLICOLOR=1`, `-e CLICOLOR_FORCE=1`, `-e FORCE_COLOR=1`, `-e npm_config_color=always`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("shell command should export terminal color setting %s: %s", expected, script)
		}
	}
	for _, expected := range []string{`force_color_prompt=yes`, `dircolors -b`, `ls --color=auto`, `grep --color=auto`, `PS1=`, `\[\e[01;32m\]`} {
		if !strings.Contains(script, expected) {
			t.Fatalf("shell command should install colorful interactive shell defaults %s: %s", expected, script)
		}
	}
	if strings.Contains(script, `claude`) {
		t.Fatalf("shell command should not launch claude directly: %s", script)
	}
	if !strings.Contains(script, `bash is not installed in this container`) {
		t.Fatalf("shell command should surface a clear bash-missing error: %s", script)
	}
}

func TestBuildCloudAgentRemoteCommandSerializesContainerEnsure(t *testing.T) {
	script := buildCloudAgentRemoteCommand(`{"container_name":"aiyolo-cloud-agent-user"}`)

	if strings.Contains(script, "\t") {
		t.Fatal("remote Python command must not contain tab indentation")
	}
	if !strings.Contains(script, "import fcntl") {
		t.Fatalf("remote script should import fcntl for container locking: %s", script)
	}
	if !strings.Contains(script, `def acquire_container_lock():`) || !strings.Contains(script, `fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)`) {
		t.Fatalf("remote script should serialize same-container ensure operations with an exclusive lock: %s", script)
	}
	if !strings.Contains(script, `def remove_container():`) || !strings.Contains(script, `failed to remove stale cloud agent container`) {
		t.Fatalf("remote script should retry and surface clear errors when removing a stale container: %s", script)
	}
	if !strings.Contains(script, `if not exact_container_id():`) {
		t.Fatalf("remote script should tolerate containers disappearing during removal races: %s", script)
	}
	if !strings.Contains(script, `lock_handle = acquire_container_lock()`) {
		t.Fatalf("remote script should hold the container lock around ensure_image and ensure_container: %s", script)
	}
}

func TestBuildCloudAgentRemoteCommandWaitsForReusedContainerRuntime(t *testing.T) {
	script := buildCloudAgentRemoteCommand(`{"container_name":"aiyolo-cloud-agent-user"}`)

	if !strings.Contains(script, `def wait_for_container_runtime():`) {
		t.Fatalf("remote script should factor runtime readiness checks into a helper: %s", script)
	}
	if !regexp.MustCompile(`if inspected is not None and inspected.get\("State", \{\}\).get\("Running"\) and container_matches\(inspected\):\n            wait_for_container_runtime\(\)\n            return container_summary\(inspect_container\(\)\)`).MatchString(script) {
		t.Fatalf("reused containers should wait for runtime readiness before returning: %s", script)
	}
	if !strings.Contains(script, `wait_for(["docker", "exec", payload["container_name"], "bash", "-lc", "test -S ${AIYOLO_ASS_SOCKET_PATH:-/run/aiyolo/ass.sock}"], 30)`) {
		t.Fatalf("remote script should wait for the aiyolo-ass socket: %s", script)
	}
	if !strings.Contains(script, `timeout=probe_timeout`) || !strings.Contains(script, `subprocess.TimeoutExpired`) {
		t.Fatalf("remote runtime readiness probes should have per-attempt timeouts: %s", script)
	}
	if strings.Contains(script, `"docker info >/dev/null 2>&1"`) || strings.Contains(script, `nc -z 127.0.0.1 {payload['container_chrome_port']}`) {
		t.Fatalf("remote runtime readiness should not block chat startup on dockerd or Chrome: %s", script)
	}
}

func TestBuildCloudAgentRemoteCommandDownloadsPublishedASSBinary(t *testing.T) {
	script := buildCloudAgentRemoteCommand(`{"container_name":"aiyolo-cloud-agent-user","ass_download_url":"https://files.example.com/linux-amd64/aiyolo-ass","ass_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","build_revision":"sha256:abc"}`)

	if !strings.Contains(script, `urllib.request.urlopen(payload["ass_download_url"], timeout=60)`) {
		t.Fatalf("remote script should download the published aiyolo-ass binary: %s", script)
	}
	if !strings.Contains(script, `downloaded aiyolo-ass checksum mismatch`) {
		t.Fatalf("remote script should verify the downloaded aiyolo-ass checksum: %s", script)
	}
	if !strings.Contains(script, `AIYOLO_ASS_SHA256=`) || !strings.Contains(script, `AIYOLO_CLOUD_AGENT_BUILD_REVISION=`) {
		t.Fatalf("remote script should pass build metadata into docker build args: %s", script)
	}
	if !strings.Contains(script, cloudAgentImageASSSHA256Label) || !strings.Contains(script, cloudAgentImageBuildRevisionLabel) {
		t.Fatalf("remote script should compare and propagate image/container revision labels: %s", script)
	}
}

func TestBuildCloudAgentRemoteCommandReusesExistingImage(t *testing.T) {
	script := buildCloudAgentRemoteCommand(`{"container_name":"aiyolo-cloud-agent-user","image":"aiyolo/local-cloud-agent:ubuntu-26.04-v4"}`)

	if !strings.Contains(script, `inspect_image()
        return`) {
		t.Fatalf("remote script should reuse an existing image without rebuilding on label drift: %s", script)
	}
	if strings.Contains(script, `image_matches(inspected)`) {
		t.Fatalf("remote script should not rebuild an existing image only because labels differ: %s", script)
	}
}

func TestBuildCloudAgentRemoteCommandResolvesUbuntuBaseWithoutPatchVersion(t *testing.T) {
	script := buildCloudAgentRemoteCommand(`{"container_name":"aiyolo-cloud-agent-user"}`)

	if !strings.Contains(script, `(?:\.[0-9]+)?-base-amd64`) {
		t.Fatalf("remote script should support Ubuntu base archives without patch versions: %s", script)
	}
	if !strings.Contains(script, `rootfs_index_url + "/SHA256SUMS"`) {
		t.Fatalf("remote script should fall back to SHA256SUMS when the index page does not list files: %s", script)
	}
}
