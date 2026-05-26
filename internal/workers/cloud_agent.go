package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

const (
	defaultCloudAgentImage               = "aiyolo/local-cloud-agent:ubuntu-24.04"
	defaultCloudAgentWorkspaceSubdir     = "cloud-agents"
	defaultCloudAgentUbuntuRelease       = "noble"
	defaultCloudAgentUbuntuSeries        = "24.04"
	defaultCloudAgentUbuntuMirror        = "https://mirrors.aliyun.com/ubuntu"
	defaultCloudAgentChromeDEBURL        = "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb"
	defaultCloudAgentRootFSIndexURL      = "https://mirrors.aliyun.com/ubuntu-cdimage/ubuntu-base/releases/24.04/release"
	defaultCloudAgentContainerVNCPort    = 5900
	defaultCloudAgentContainerChromePort = 9222
	defaultCloudAgentHostVNCBasePort     = 15000
	defaultCloudAgentHostChromeBasePort  = 19000
	defaultCloudAgentHostPortSpan        = 1000
	defaultCloudAgentDisplay             = ":99"
	defaultCloudAgentSHMSize             = "2g"
	defaultCloudAgentDockerStorageDriver = "vfs"
)

type CloudAgentStartOptions struct {
	UserID               string
	AgentType            string
	Image                string
	ContainerName        string
	WorkspaceRoot        string
	WorkspacePath        string
	DockerDataRoot       string
	APIBaseURL           string
	ConsoleBaseURL       string
	APIKey               string
	DefaultModel         string
	AllowedModels        []string
	OpenURL              string
	EnableDisplay        bool
	EnableDockerd        bool
	AutoStartChrome      bool
	Display              string
	ContainerVNCPort     int
	HostVNCPort          int
	ContainerChromePort  int
	HostChromePort       int
	SHMSize              string
	DockerRegistryMirror string
	DockerStorageDriver  string
	RootFSURL            string
	RootFSIndexURL       string
	UbuntuRelease        string
	UbuntuSeries         string
	UbuntuMirror         string
	ChromeDEBURL         string
}

type CloudAgentInstance struct {
	Status            string   `json:"status"`
	WorkerID          string   `json:"worker_id"`
	ContainerID       string   `json:"container_id"`
	ContainerName     string   `json:"container_name"`
	Image             string   `json:"image"`
	WorkspaceRoot     string   `json:"workspace_root"`
	WorkspacePath     string   `json:"workspace_path"`
	DockerDataRoot    string   `json:"docker_data_root"`
	VNCAddress        string   `json:"vnc,omitempty"`
	ChromeDevtoolsURL string   `json:"chrome_devtools,omitempty"`
	DefaultModel      string   `json:"default_model,omitempty"`
	AllowedModels     []string `json:"allowed_models,omitempty"`
	ConsoleURL        string   `json:"console_url,omitempty"`
	APIBaseURL        string   `json:"api_base_url,omitempty"`
	LastStartedAt     string   `json:"last_started_at,omitempty"`
}

type cloudAgentRemotePayload struct {
	WorkerID             string            `json:"worker_id"`
	UserID               string            `json:"user_id"`
	AgentType            string            `json:"agent_type"`
	Image                string            `json:"image"`
	ContainerName        string            `json:"container_name"`
	WorkspaceRoot        string            `json:"workspace_root"`
	WorkspacePath        string            `json:"workspace_path"`
	DockerDataRoot       string            `json:"docker_data_root"`
	APIBaseURL           string            `json:"api_base_url"`
	ConsoleBaseURL       string            `json:"console_base_url"`
	DefaultModel         string            `json:"default_model"`
	AllowedModels        []string          `json:"allowed_models,omitempty"`
	OpenURL              string            `json:"open_url,omitempty"`
	EnableDisplay        bool              `json:"enable_display"`
	EnableDockerd        bool              `json:"enable_dockerd"`
	AutoStartChrome      bool              `json:"auto_start_chrome"`
	Display              string            `json:"display"`
	ContainerVNCPort     int               `json:"container_vnc_port"`
	HostVNCPort          int               `json:"host_vnc_port"`
	ContainerChromePort  int               `json:"container_chrome_port"`
	HostChromePort       int               `json:"host_chrome_port"`
	SHMSize              string            `json:"shm_size"`
	DockerStorageDriver  string            `json:"docker_storage_driver"`
	DockerRegistryMirror string            `json:"docker_registry_mirror,omitempty"`
	ProxyEnv             map[string]string `json:"proxy_env,omitempty"`
	ContainerEnv         map[string]string `json:"container_env,omitempty"`
	UbuntuRelease        string            `json:"ubuntu_release"`
	UbuntuSeries         string            `json:"ubuntu_series"`
	UbuntuMirror         string            `json:"ubuntu_mirror"`
	ChromeDEBURL         string            `json:"chrome_deb_url"`
	RootFSURL            string            `json:"rootfs_url,omitempty"`
	RootFSIndexURL       string            `json:"rootfs_index_url"`
	Files                map[string]string `json:"files"`
	StartedAt            string            `json:"started_at"`
	APIKey               string            `json:"api_key"`
}

func EnsureCloudAgent(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options CloudAgentStartOptions) (CloudAgentInstance, error) {
	select {
	case <-ctx.Done():
		return CloudAgentInstance{}, ctx.Err()
	default:
	}
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return CloudAgentInstance{}, err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return CloudAgentInstance{}, err
	}
	options, err = normalizeCloudAgentStartOptions(worker, options)
	if err != nil {
		return CloudAgentInstance{}, err
	}
	proxyEnv := RenderProxyEnv(proxy)
	containerEnv := cloudAgentContainerEnv(options)
	for key, value := range proxyEnv {
		containerEnv[key] = value
	}
	payload, err := json.Marshal(cloudAgentRemotePayload{
		WorkerID:             worker.ID,
		UserID:               options.UserID,
		AgentType:            options.AgentType,
		Image:                options.Image,
		ContainerName:        options.ContainerName,
		WorkspaceRoot:        options.WorkspaceRoot,
		WorkspacePath:        options.WorkspacePath,
		DockerDataRoot:       options.DockerDataRoot,
		APIBaseURL:           options.APIBaseURL,
		ConsoleBaseURL:       options.ConsoleBaseURL,
		DefaultModel:         options.DefaultModel,
		AllowedModels:        append([]string(nil), options.AllowedModels...),
		OpenURL:              options.OpenURL,
		EnableDisplay:        options.EnableDisplay,
		EnableDockerd:        options.EnableDockerd,
		AutoStartChrome:      options.AutoStartChrome,
		Display:              options.Display,
		ContainerVNCPort:     options.ContainerVNCPort,
		HostVNCPort:          options.HostVNCPort,
		ContainerChromePort:  options.ContainerChromePort,
		HostChromePort:       options.HostChromePort,
		SHMSize:              options.SHMSize,
		DockerStorageDriver:  options.DockerStorageDriver,
		DockerRegistryMirror: options.DockerRegistryMirror,
		ProxyEnv:             proxyEnv,
		ContainerEnv:         containerEnv,
		UbuntuRelease:        options.UbuntuRelease,
		UbuntuSeries:         options.UbuntuSeries,
		UbuntuMirror:         options.UbuntuMirror,
		ChromeDEBURL:         options.ChromeDEBURL,
		RootFSURL:            options.RootFSURL,
		RootFSIndexURL:       options.RootFSIndexURL,
		Files:                cloudAgentBuildContextFiles,
		StartedAt:            time.Now().UTC().Format(time.RFC3339),
		APIKey:               options.APIKey,
	})
	if err != nil {
		return CloudAgentInstance{}, err
	}
	client, err := dialSSH(worker, key)
	if err != nil {
		return CloudAgentInstance{}, err
	}
	defer client.Close()
	command := buildCloudAgentRemoteCommand(string(payload))
	output, err := runSSHCommand(client, command)
	if err != nil {
		return CloudAgentInstance{}, fmt.Errorf("ensure cloud agent on %s: %w", worker.ID, err)
	}
	var instance CloudAgentInstance
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &instance); err != nil {
		return CloudAgentInstance{}, fmt.Errorf("parse cloud agent response: %w", err)
	}
	return instance, nil
}

func normalizeCloudAgentStartOptions(worker domain.WorkerServer, options CloudAgentStartOptions) (CloudAgentStartOptions, error) {
	options.UserID = strings.TrimSpace(options.UserID)
	if options.UserID == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent user id is required")
	}
	options.AgentType = strings.TrimSpace(options.AgentType)
	if options.AgentType == "" {
		options.AgentType = domain.CloudAgentTypeClaudeCode
	}
	options.Image = strings.TrimSpace(options.Image)
	if options.Image == "" {
		options.Image = defaultCloudAgentImage
	}
	options.WorkspacePath = path.Clean(strings.TrimSpace(options.WorkspacePath))
	if options.WorkspacePath == "" || options.WorkspacePath == "." {
		options.WorkspacePath = domain.DefaultCloudAgentWorkspacePath
	}
	if !strings.HasPrefix(options.WorkspacePath, "/") {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent workspace path must be absolute")
	}
	safeUser := sanitizeCloudAgentSegment(options.UserID)
	if strings.TrimSpace(options.ContainerName) == "" {
		options.ContainerName = "aiyolo-cloud-agent-" + safeUser
	}
	if strings.TrimSpace(options.WorkspaceRoot) == "" {
		options.WorkspaceRoot = path.Join(worker.DataRoot, defaultCloudAgentWorkspaceSubdir, safeUser, "workspace")
	}
	if strings.TrimSpace(options.DockerDataRoot) == "" {
		options.DockerDataRoot = path.Join(worker.DataRoot, defaultCloudAgentWorkspaceSubdir, safeUser, "docker")
	}
	options.APIBaseURL = strings.TrimRight(strings.TrimSpace(options.APIBaseURL), "/")
	if options.APIBaseURL == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent API base URL is required")
	}
	options.ConsoleBaseURL = strings.TrimRight(strings.TrimSpace(options.ConsoleBaseURL), "/")
	if options.ConsoleBaseURL == "" {
		options.ConsoleBaseURL = strings.TrimSuffix(options.APIBaseURL, "/v1")
	}
	options.APIKey = strings.TrimSpace(options.APIKey)
	if options.APIKey == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent API key is required")
	}
	options.DefaultModel = strings.TrimSpace(options.DefaultModel)
	if options.DefaultModel == "" && len(options.AllowedModels) > 0 {
		options.DefaultModel = strings.TrimSpace(options.AllowedModels[0])
	}
	options.AllowedModels = normalizeCloudAgentAllowedModels(options.AllowedModels, options.DefaultModel)
	if strings.TrimSpace(options.OpenURL) == "" {
		options.OpenURL = options.ConsoleBaseURL + "/console/chat"
	}
	if !options.EnableDisplay {
		options.EnableDisplay = true
	}
	if !options.EnableDockerd {
		options.EnableDockerd = true
	}
	if !options.AutoStartChrome {
		options.AutoStartChrome = true
	}
	if strings.TrimSpace(options.Display) == "" {
		options.Display = defaultCloudAgentDisplay
	}
	if options.ContainerVNCPort <= 0 {
		options.ContainerVNCPort = defaultCloudAgentContainerVNCPort
	}
	if options.ContainerChromePort <= 0 {
		options.ContainerChromePort = defaultCloudAgentContainerChromePort
	}
	if options.HostVNCPort <= 0 {
		options.HostVNCPort = cloudAgentHostPort(options.UserID, worker.ID, defaultCloudAgentHostVNCBasePort)
	}
	if options.HostChromePort <= 0 {
		options.HostChromePort = cloudAgentHostPort(options.UserID, worker.ID+"-chrome", defaultCloudAgentHostChromeBasePort)
	}
	if strings.TrimSpace(options.SHMSize) == "" {
		options.SHMSize = defaultCloudAgentSHMSize
	}
	if strings.TrimSpace(options.DockerStorageDriver) == "" {
		options.DockerStorageDriver = defaultCloudAgentDockerStorageDriver
	}
	if strings.TrimSpace(options.UbuntuRelease) == "" {
		options.UbuntuRelease = defaultCloudAgentUbuntuRelease
	}
	if strings.TrimSpace(options.UbuntuSeries) == "" {
		options.UbuntuSeries = defaultCloudAgentUbuntuSeries
	}
	if strings.TrimSpace(options.UbuntuMirror) == "" {
		options.UbuntuMirror = defaultCloudAgentUbuntuMirror
	}
	if strings.TrimSpace(options.ChromeDEBURL) == "" {
		options.ChromeDEBURL = defaultCloudAgentChromeDEBURL
	}
	if strings.TrimSpace(options.RootFSIndexURL) == "" {
		options.RootFSIndexURL = defaultCloudAgentRootFSIndexURL
	}
	options.RootFSURL = strings.TrimSpace(options.RootFSURL)
	return options, nil
}

func normalizeCloudAgentAllowedModels(values []string, defaultModel string) []string {
	seen := make(map[string]struct{}, len(values)+1)
	result := make([]string, 0, len(values)+1)
	for _, value := range append([]string{defaultModel}, values...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func sanitizeCloudAgentSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var builder strings.Builder
	lastDash := false
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
			lastDash = false
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch + ('a' - 'A'))
			lastDash = false
		case ch == '-', ch == '_':
			builder.WriteRune(ch)
			lastDash = false
		default:
			if lastDash || builder.Len() == 0 {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-_")
	if result == "" {
		return "default"
	}
	return result
}

func cloudAgentHostPort(userID, discriminator string, base int) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSpace(userID) + ":" + strings.TrimSpace(discriminator)))
	return base + int(hash.Sum32()%defaultCloudAgentHostPortSpan)
}

func cloudAgentContainerEnv(options CloudAgentStartOptions) map[string]string {
	apiRoot := strings.TrimRight(options.ConsoleBaseURL, "/")
	defaultModel := strings.TrimSpace(options.DefaultModel)
	env := map[string]string{
		"AIYOLO_USER_ID":                       options.UserID,
		"AIYOLO_AGENT_TYPE":                    options.AgentType,
		"AIYOLO_API_BASE_URL":                  options.APIBaseURL,
		"AIYOLO_API_ROOT_URL":                  apiRoot,
		"AIYOLO_API_KEY":                       options.APIKey,
		"AIYOLO_DEFAULT_MODEL":                 defaultModel,
		"AIYOLO_ALLOWED_MODELS":                strings.Join(options.AllowedModels, ","),
		"AIYOLO_CONSOLE_BASE_URL":              apiRoot,
		"OPENAI_BASE_URL":                      options.APIBaseURL,
		"OPENAI_API_BASE":                      options.APIBaseURL,
		"OPENAI_API_KEY":                       options.APIKey,
		"ANTHROPIC_API_KEY":                    options.APIKey,
		"ANTHROPIC_BASE_URL":                   apiRoot,
		"ANTHROPIC_API_URL":                    apiRoot,
		"AIYOLO_CLOUD_AGENT_ENABLE_DISPLAY":    boolString(options.EnableDisplay),
		"AIYOLO_CLOUD_AGENT_ENABLE_DOCKERD":    boolString(options.EnableDockerd),
		"AIYOLO_CLOUD_AGENT_AUTO_START_CHROME": boolString(options.AutoStartChrome),
		"AIYOLO_CLOUD_AGENT_CHROME_URL":        options.OpenURL,
		"AIYOLO_DISPLAY":                       options.Display,
		"AIYOLO_VNC_PORT":                      strconv.Itoa(options.ContainerVNCPort),
		"AIYOLO_CHROME_REMOTE_DEBUGGING_PORT":  strconv.Itoa(options.ContainerChromePort),
		"AIYOLO_DOCKER_STORAGE_DRIVER":         options.DockerStorageDriver,
	}
	if defaultModel != "" {
		env["ANTHROPIC_MODEL"] = defaultModel
		if requiresAnthropicCustomModelOption(defaultModel) {
			env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = defaultModel
			env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] = defaultModel
			env["ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION"] = "AIYolo gateway default model"
		}
	}
	if strings.TrimSpace(options.DockerRegistryMirror) != "" {
		env["AIYOLO_DOCKER_REGISTRY_MIRROR"] = strings.TrimSpace(options.DockerRegistryMirror)
	}
	return env
}

func requiresAnthropicCustomModelOption(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	return !strings.HasPrefix(model, "claude") && !strings.HasPrefix(model, "anthropic")
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func buildCloudAgentRemoteCommand(payloadJSON string) string {
	script := strings.ReplaceAll(cloudAgentRemotePythonTemplate, "__PAYLOAD_JSON__", strconv.Quote(payloadJSON))
	return "python3 - <<'PY'\n" + script + "\nPY"
}

const cloudAgentRemotePythonTemplate = `import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import urllib.request

payload = json.loads(__PAYLOAD_JSON__)


def run(args, env=None):
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env)


def ensure_dirs():
    pathlib.Path(payload["workspace_root"]).mkdir(parents=True, exist_ok=True)
    pathlib.Path(payload["docker_data_root"]).mkdir(parents=True, exist_ok=True)


def write_workspace_files():
    metadata = {
        "user_id": payload["user_id"],
        "agent_type": payload["agent_type"],
        "worker_id": payload["worker_id"],
        "workspace_path": payload["workspace_path"],
        "created_by": "console-chat",
        "image": payload["image"],
        "api_base_url": payload["api_base_url"],
        "console_base_url": payload.get("console_base_url", ""),
        "default_model": payload.get("default_model", ""),
        "allowed_models": payload.get("allowed_models", []),
        "open_url": payload.get("open_url", ""),
        "last_started_at": payload.get("started_at", ""),
    }
    metadata_path = pathlib.Path(payload["workspace_root"]) / ".aiyolo-cloud-agent.json"
    metadata_path.write_text(json.dumps(metadata, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
    readme_path = pathlib.Path(payload["workspace_root"]) / "README.aiyolo-cloud-agent.txt"
    readme_path.write_text(
        "AIYolo cloud-agent is running for this workspace.\n"
        "\n"
        "Container runtime exports these variables inside the container:\n"
        "- OPENAI_BASE_URL / OPENAI_API_BASE\n"
        "- OPENAI_API_KEY\n"
        "- AIYOLO_API_BASE_URL / AIYOLO_API_ROOT_URL\n"
        "- AIYOLO_DEFAULT_MODEL / AIYOLO_ALLOWED_MODELS\n"
        "\n"
        "Open the browser inside the container at:\n"
        f"{payload.get('open_url', '')}\n",
        encoding="utf-8",
    )


def resolve_rootfs_url():
    if payload.get("rootfs_url"):
        return payload["rootfs_url"].strip()
    with urllib.request.urlopen(payload["rootfs_index_url"], timeout=30) as response:
        index_html = response.read().decode("utf-8", errors="replace")
    matches = re.findall(r"ubuntu-base-" + re.escape(payload["ubuntu_series"]) + r"\.[0-9]+-base-amd64\.tar\.gz", index_html)
    if not matches:
        raise SystemExit("unable to resolve Ubuntu base rootfs from index")
    matches = sorted(set(matches), key=lambda item: tuple(int(part) for part in re.findall(r"\d+", item)))
    return payload["rootfs_index_url"].rstrip("/") + "/" + matches[-1]


def ensure_image():
    try:
        run(["docker", "image", "inspect", payload["image"]])
        return
    except subprocess.CalledProcessError:
        pass
    build_env = os.environ.copy()
    for key, value in (payload.get("proxy_env") or {}).items():
        build_env[key] = value
    temp_root = pathlib.Path(tempfile.mkdtemp(prefix="aiyolo-cloud-agent-build-"))
    try:
        for relative_path, content in (payload.get("files") or {}).items():
            target_path = temp_root / relative_path
            target_path.parent.mkdir(parents=True, exist_ok=True)
            target_path.write_text(content, encoding="utf-8")
        rootfs_url = resolve_rootfs_url()
        rootfs_path = temp_root / "rootfs.tar.gz"
        with urllib.request.urlopen(rootfs_url, timeout=60) as response, open(rootfs_path, "wb") as handle:
            shutil.copyfileobj(response, handle)
        build_args = [
            "docker", "build", "--pull",
            "--build-arg", f"UBUNTU_RELEASE={payload['ubuntu_release']}",
            "--build-arg", f"APT_MIRROR={payload['ubuntu_mirror']}",
            "--build-arg", f"CHROME_DEB_URL={payload['chrome_deb_url']}",
        ]
        for key in sorted((payload.get("proxy_env") or {}).keys()):
            build_args.extend(["--build-arg", f"{key}={payload['proxy_env'][key]}"])
        build_args.extend(["-t", payload["image"], "-f", str(temp_root / "Dockerfile"), str(temp_root)])
        run(build_args, env=build_env)
    finally:
        shutil.rmtree(temp_root, ignore_errors=True)


def inspect_container():
    result = run(["docker", "inspect", payload["container_name"]])
    return json.loads(result.stdout)[0]


def container_summary(inspected):
    summary = {
        "status": "running" if inspected["State"].get("Running") else "stopped",
        "worker_id": payload["worker_id"],
        "container_id": inspected["Id"],
        "container_name": payload["container_name"],
        "image": inspected["Config"].get("Image", payload["image"]),
        "workspace_root": payload["workspace_root"],
        "workspace_path": payload["workspace_path"],
        "docker_data_root": payload["docker_data_root"],
        "default_model": payload.get("default_model", ""),
        "allowed_models": payload.get("allowed_models", []),
        "console_url": payload.get("open_url", ""),
        "api_base_url": payload.get("api_base_url", ""),
        "last_started_at": payload.get("started_at", ""),
    }
    if payload.get("enable_display"):
        summary["vnc"] = f"127.0.0.1:{payload['host_vnc_port']}"
    if payload.get("enable_display") and payload.get("auto_start_chrome"):
        summary["chrome_devtools"] = f"http://127.0.0.1:{payload['host_chrome_port']}/json/version"
    return summary


def wait_for(command, timeout_seconds):
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        try:
            subprocess.run(command, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            return
        except subprocess.CalledProcessError:
            time.sleep(1)
    raise SystemExit("timed out waiting for container service")


def ensure_container():
    existing = run(["docker", "ps", "-a", "--filter", f"name=^/{payload['container_name']}$", "--format", "{{.ID}}"])
    existing_id = existing.stdout.strip()
    if existing_id:
        inspected = inspect_container()
        labels = inspected.get("Config", {}).get("Labels") or {}
        if inspected.get("State", {}).get("Running") and inspected.get("Config", {}).get("Image") == payload["image"] and labels.get("aiyolo.user_id") == payload["user_id"]:
            return container_summary(inspected)
        run(["docker", "rm", "-f", payload["container_name"]])
    args = [
        "docker", "run", "-d",
        "--name", payload["container_name"],
        "--hostname", payload["container_name"],
        "--restart", "unless-stopped",
        "--privileged",
        "--shm-size", payload["shm_size"],
        "--label", f"aiyolo.user_id={payload['user_id']}",
        "--label", f"aiyolo.agent_type={payload['agent_type']}",
        "--label", f"aiyolo.workspace_path={payload['workspace_path']}",
    ]
    for key in sorted((payload.get("container_env") or {}).keys()):
        args.extend(["-e", f"{key}={payload['container_env'][key]}"])
    args.extend(["-v", f"{payload['workspace_root']}:{payload['workspace_path']}"])
    args.extend(["-v", f"{payload['docker_data_root']}:/var/lib/docker"])
    args.extend(["-w", payload["workspace_path"]])
    if payload.get("enable_display"):
        args.extend(["-p", f"127.0.0.1:{payload['host_vnc_port']}:{payload['container_vnc_port']}"])
    if payload.get("enable_display") and payload.get("auto_start_chrome"):
        args.extend(["-p", f"127.0.0.1:{payload['host_chrome_port']}:{payload['container_chrome_port']}"])
    args.append(payload["image"])
    run(args)
    if payload.get("enable_display"):
        wait_for(["docker", "exec", payload["container_name"], "bash", "-lc", f"nc -z 127.0.0.1 {payload['container_vnc_port']}"] , 30)
    if payload.get("enable_dockerd"):
        wait_for(["docker", "exec", payload["container_name"], "bash", "-lc", "docker info >/dev/null 2>&1"], 60)
    if payload.get("enable_display") and payload.get("auto_start_chrome"):
        wait_for(["docker", "exec", payload["container_name"], "bash", "-lc", f"nc -z 127.0.0.1 {payload['container_chrome_port']}"] , 60)
    return container_summary(inspect_container())


if __name__ == "__main__":
    import time

    ensure_dirs()
    write_workspace_files()
    ensure_image()
    print(json.dumps(ensure_container(), ensure_ascii=True, sort_keys=True))`

var cloudAgentBuildContextFiles = map[string]string{
	"Dockerfile":                        cloudAgentDockerfile,
	"cloud-agent-base.json":             cloudAgentBaseJSON,
	"aiyolo-cloud-agent-entrypoint":     cloudAgentEntrypointScript,
	"aiyolo-cloud-agent-info":           cloudAgentInfoScript,
	"aiyolo-cloud-agent-start-display":  cloudAgentStartDisplayScript,
	"aiyolo-cloud-agent-open-chrome":    cloudAgentOpenChromeScript,
	"aiyolo-cloud-agent-start-docker":   cloudAgentStartDockerScript,
	"aiyolo-cloud-agent-start-services": cloudAgentStartServicesScript,
}

const cloudAgentDockerfile = `FROM scratch

ADD rootfs.tar.gz /

ARG UBUNTU_RELEASE=noble
ARG APT_MIRROR=https://mirrors.aliyun.com/ubuntu
ARG CHROME_DEB_URL=https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
ARG NODE_VERSION=20.20.2
ARG CLAUDE_CODE_VERSION=2.1.150

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV DISPLAY=:99
ENV XDG_RUNTIME_DIR=/tmp/runtime-root
ENV CHROME_BIN=/usr/bin/google-chrome-stable

RUN set -eux; \
    mirror="${APT_MIRROR%/}"; \
    if [ -f /etc/apt/sources.list ]; then \
      sed -i \
        -e "s|http://archive.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|http://security.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|https://archive.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|https://security.ubuntu.com/ubuntu|${mirror}|g" \
        /etc/apt/sources.list; \
    else \
      printf 'deb %s %s main restricted universe multiverse\ndeb %s %s-updates main restricted universe multiverse\ndeb %s %s-security main restricted universe multiverse\n' \
        "${mirror}" "${UBUNTU_RELEASE}" "${mirror}" "${UBUNTU_RELEASE}" "${mirror}" "${UBUNTU_RELEASE}" >/etc/apt/sources.list; \
    fi; \
    printf 'Acquire::Retries "5";\nAcquire::http::No-Cache "true";\nAcquire::https::No-Cache "true";\n' >/etc/apt/apt.conf.d/99aiyolo

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash bash-completion ca-certificates curl dbus-x11 file fluxbox fonts-dejavu-core fonts-liberation \
    git iproute2 iptables iputils-ping jq less libasound2t64 libatk-bridge2.0-0 libcups2t64 libgbm1 libgtk-3-0 \
    libnss3 libu2f-udev libxss1 libxtst6 mesa-utils netcat-openbsd procps psmisc python3 python3-dev python3-pip \
    python3-venv rsync sudo tini tzdata uidmap unzip wget x11vnc xauth xdg-utils xterm xvfb zip \
    docker.io fuse-overlayfs \
 && install -d -m 0755 /workspace /etc/aiyolo /usr/local/bin /tmp/runtime-root /opt \
 && wget -nv --tries=3 --timeout=30 -O /tmp/google-chrome.deb "${CHROME_DEB_URL}" \
 && apt-get install -y /tmp/google-chrome.deb \
 && rm -f /tmp/google-chrome.deb \
 && curl -fsSL --retry 5 --connect-timeout 30 "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.gz" -o /tmp/node.tar.gz \
 && tar -xzf /tmp/node.tar.gz -C /opt \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/node" /usr/local/bin/node \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/npm" /usr/local/bin/npm \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/npx" /usr/local/bin/npx \
 && npm install -g --prefix /usr/local "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" --cache /tmp/.npm-cache --fund=false --audit=false \
 && node --version \
 && npm --version \
 && claude --version \
 && rm -f /tmp/node.tar.gz \
 && rm -rf /tmp/.npm-cache \
 && rm -rf /var/lib/apt/lists/*

COPY cloud-agent-base.json /etc/aiyolo/cloud-agent-base.json
COPY aiyolo-cloud-agent-entrypoint /usr/local/bin/aiyolo-cloud-agent-entrypoint
COPY aiyolo-cloud-agent-info /usr/local/bin/aiyolo-cloud-agent-info
COPY aiyolo-cloud-agent-start-display /usr/local/bin/aiyolo-cloud-agent-start-display
COPY aiyolo-cloud-agent-open-chrome /usr/local/bin/aiyolo-cloud-agent-open-chrome
COPY aiyolo-cloud-agent-start-docker /usr/local/bin/aiyolo-cloud-agent-start-docker
COPY aiyolo-cloud-agent-start-services /usr/local/bin/aiyolo-cloud-agent-start-services

RUN chmod 0755 \
    /usr/local/bin/aiyolo-cloud-agent-entrypoint \
    /usr/local/bin/aiyolo-cloud-agent-info \
    /usr/local/bin/aiyolo-cloud-agent-start-display \
    /usr/local/bin/aiyolo-cloud-agent-open-chrome \
    /usr/local/bin/aiyolo-cloud-agent-start-docker \
    /usr/local/bin/aiyolo-cloud-agent-start-services

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/aiyolo-cloud-agent-entrypoint"]
CMD ["/usr/local/bin/aiyolo-cloud-agent-start-services"]
`

const cloudAgentBaseJSON = `{
  "base_os": "ubuntu",
  "base_version": "24.04",
  "image_flavor": "local-cloud-agent",
  "features": [
    "git",
		"nodejs",
		"npm",
    "python3",
    "jq",
		"claude-code",
    "xvfb",
    "fluxbox",
    "x11vnc",
    "desktop-vnc",
    "google-chrome",
    "docker.io",
    "docker-in-docker",
    "workspace-volume"
  ]
}
`

const cloudAgentEntrypointScript = `#!/usr/bin/env bash
set -euo pipefail

install -d -m 0755 /workspace "${XDG_RUNTIME_DIR:-/tmp/runtime-root}" /tmp/.X11-unix
chmod 1777 /tmp/.X11-unix

if [[ $# -eq 0 ]]; then
  set -- /usr/local/bin/aiyolo-cloud-agent-start-services
fi

exec "$@"
`

const cloudAgentInfoScript = `#!/usr/bin/env bash
set -euo pipefail

cat /etc/os-release
printf '\n--- manifest ---\n'
cat /etc/aiyolo/cloud-agent-base.json
printf '\n--- toolchain ---\n'
git --version
node --version
npm --version
claude --version
python3 --version
jq --version
docker --version
dockerd --version
google-chrome-stable --version
dpkg-query -W -f='xvfb ${Version}\n' xvfb 2>/dev/null || true
dpkg-query -W -f='fluxbox ${Version}\n' fluxbox 2>/dev/null || true
dpkg-query -W -f='x11vnc ${Version}\n' x11vnc 2>/dev/null || true
printf '\n--- services ---\n'
ps -ef | grep -E 'Xvfb|fluxbox|x11vnc|dockerd|google-chrome' | grep -v grep || true
if docker info >/tmp/aiyolo-docker-info.log 2>&1; then
  cat /tmp/aiyolo-docker-info.log
else
  cat /tmp/aiyolo-docker-info.log
fi
printf '\n--- workspace ---\n'
pwd
ls -la /workspace | sed -n '1,80p'
`

const cloudAgentStartDisplayScript = `#!/usr/bin/env bash
set -euo pipefail

display="${AIYOLO_DISPLAY:-:99}"
screen="${AIYOLO_SCREEN:-1440x900x24}"
vnc_port="${AIYOLO_VNC_PORT:-5900}"

mkdir -p /tmp/.X11-unix "${XDG_RUNTIME_DIR:-/tmp/runtime-root}"
chmod 1777 /tmp/.X11-unix
rm -f "/tmp/.X11-unix/X${display#:}" "/tmp/.X${display#:}-lock"

Xvfb "$display" -screen 0 "$screen" -nolisten tcp >/tmp/aiyolo-xvfb.log 2>&1 &
sleep 1
DISPLAY="$display" fluxbox >/tmp/aiyolo-fluxbox.log 2>&1 &
DISPLAY="$display" x11vnc -display "$display" -forever -shared -nopw -rfbport "$vnc_port" >/tmp/aiyolo-x11vnc.log 2>&1 &

wait -n
`

const cloudAgentOpenChromeScript = `#!/usr/bin/env bash
set -euo pipefail

display="${AIYOLO_DISPLAY:-:99}"
remote_debug_port="${AIYOLO_CHROME_REMOTE_DEBUGGING_PORT:-9222}"
if [[ $# -eq 0 ]]; then
  set -- about:blank
fi

extra_flags=()
if [[ -n "${AIYOLO_CHROME_EXTRA_FLAGS:-}" ]]; then
  read -r -a extra_flags <<<"${AIYOLO_CHROME_EXTRA_FLAGS}"
fi

export DISPLAY="$display"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/runtime-root}"

exec google-chrome-stable \
  --no-sandbox \
  --disable-dev-shm-usage \
  --disable-gpu \
  --no-first-run \
  --no-default-browser-check \
  --user-data-dir=/tmp/aiyolo-chrome-profile \
  --remote-debugging-address=0.0.0.0 \
  --remote-debugging-port="$remote_debug_port" \
  "${extra_flags[@]}" \
  "$@"
`

const cloudAgentStartDockerScript = `#!/usr/bin/env bash
set -euo pipefail

config_path=/etc/docker/daemon.json
data_root="${AIYOLO_DOCKER_DATA_ROOT:-/var/lib/docker}"
storage_driver="${AIYOLO_DOCKER_STORAGE_DRIVER:-vfs}"
registry_mirror="${AIYOLO_DOCKER_REGISTRY_MIRROR:-}"
dns_servers="${AIYOLO_DOCKER_DNS_SERVERS:-}"
host="${AIYOLO_DOCKER_HOST:-unix:///var/run/docker.sock}"

install -d -m 0755 /etc/docker "$data_root" /var/run

CONFIG_PATH="$config_path" \
REGISTRY_MIRROR="$registry_mirror" \
DNS_SERVERS="$dns_servers" \
STORAGE_DRIVER="$storage_driver" \
python3 <<'PY'
import json
import os

payload = {
    "features": {"buildkit": True},
    "iptables": True,
    "storage-driver": os.environ["STORAGE_DRIVER"],
}

registry_mirror = os.environ.get("REGISTRY_MIRROR", "").strip()
if registry_mirror:
    payload["registry-mirrors"] = [registry_mirror]

dns_servers = [item.strip() for item in os.environ.get("DNS_SERVERS", "").split(",") if item.strip()]
if dns_servers:
    payload["dns"] = dns_servers

with open(os.environ["CONFIG_PATH"], "w", encoding="utf-8") as handle:
    json.dump(payload, handle, ensure_ascii=True, indent=2)
    handle.write("\n")
PY

exec dockerd --host "$host" --data-root "$data_root" --exec-root /var/run/docker --pidfile /var/run/docker.pid
`

const cloudAgentStartServicesScript = `#!/usr/bin/env bash
set -euo pipefail

enable_display="${AIYOLO_CLOUD_AGENT_ENABLE_DISPLAY:-1}"
enable_dockerd="${AIYOLO_CLOUD_AGENT_ENABLE_DOCKERD:-1}"
auto_start_chrome="${AIYOLO_CLOUD_AGENT_AUTO_START_CHROME:-1}"
chrome_url="${AIYOLO_CLOUD_AGENT_CHROME_URL:-about:blank}"
display="${AIYOLO_DISPLAY:-:99}"

declare -a pids=()

if [[ "$enable_dockerd" == "1" ]]; then
  /usr/local/bin/aiyolo-cloud-agent-start-docker >/tmp/aiyolo-dockerd.log 2>&1 &
  pids+=("$!")
fi

if [[ "$enable_display" == "1" ]]; then
  /usr/local/bin/aiyolo-cloud-agent-start-display >/tmp/aiyolo-display.log 2>&1 &
  pids+=("$!")
fi

if [[ "$enable_display" == "1" && "$auto_start_chrome" == "1" ]]; then
  for _ in $(seq 1 30); do
    if nc -z 127.0.0.1 "${AIYOLO_VNC_PORT:-5900}" >/dev/null 2>&1 || [[ -S "/tmp/.X11-unix/X${display#:}" ]]; then
      break
    fi
    sleep 1
  done
  /usr/local/bin/aiyolo-cloud-agent-open-chrome "$chrome_url" >/tmp/aiyolo-chrome.log 2>&1 &
fi

if [[ ${#pids[@]} -eq 0 ]]; then
  exec sleep infinity
fi

cleanup() {
  local pid
  for pid in "${pids[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
}

trap cleanup EXIT INT TERM

wait -n "${pids[@]}"
`
