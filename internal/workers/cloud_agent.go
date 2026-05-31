package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

const (
	defaultCloudAgentImage               = "aiyolo/local-cloud-agent:ubuntu-24.04-v3"
	defaultCloudAgentWorkspaceSubdir     = "cloud-agents"
	defaultCloudAgentUbuntuRelease       = "noble"
	defaultCloudAgentUbuntuSeries        = "24.04"
	defaultCloudAgentUbuntuMirror        = "https://mirrors.aliyun.com/ubuntu"
	defaultCloudAgentChromeDEBURL        = "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb"
	defaultCloudAgentRootFSIndexURL      = "https://mirrors.aliyun.com/ubuntu-cdimage/ubuntu-base/releases/24.04/release"
	defaultCloudAgentContainerVNCPort    = 5900
	defaultCloudAgentContainerChromePort = 9222
	defaultCloudAgentContainerASSPort    = 17811
	defaultCloudAgentHostVNCBasePort     = 15000
	defaultCloudAgentHostASSBasePort     = 18000
	defaultCloudAgentHostChromeBasePort  = 19000
	defaultCloudAgentHostPortSpan        = 1000
	defaultCloudAgentDisplay             = ":99"
	defaultCloudAgentSHMSize             = "2g"
	defaultCloudAgentDockerStorageDriver = "vfs"
	defaultCloudAgentClaudeUser          = "aiyolo"
	defaultCloudAgentClaudeHome          = "/workspace"
	cloudAgentImageBuildRevisionLabel    = "aiyolo.cloud_agent.build_revision"
	cloudAgentImageASSSHA256Label        = "aiyolo.ass.sha256"
)

const CloudAgentASSArtifactObjectKey = "linux-amd64/aiyolo-ass"

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
	ContainerASSPort     int
	HostASSPort          int
	SHMSize              string
	DockerRegistryMirror string
	DockerStorageDriver  string
	RootFSURL            string
	RootFSIndexURL       string
	UbuntuRelease        string
	UbuntuSeries         string
	UbuntuMirror         string
	ChromeDEBURL         string
	ASSDownloadURL       string
	ASSSHA256URL         string
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
	ASSBaseURL        string   `json:"ass_base_url,omitempty"`
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
	ContainerASSPort     int               `json:"container_ass_port"`
	HostASSPort          int               `json:"host_ass_port"`
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
	ASSDownloadURL       string            `json:"ass_download_url"`
	ASSSHA256            string            `json:"ass_sha256"`
	BuildRevision        string            `json:"build_revision"`
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
	resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	assSHA256, err := resolveCloudAgentASSSHA256(resolveCtx, options.ASSSHA256URL)
	if err != nil {
		return CloudAgentInstance{}, err
	}
	buildRevision := cloudAgentBuildRevision(options, cloudAgentBuildContextFiles, assSHA256)
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
		ContainerASSPort:     options.ContainerASSPort,
		HostASSPort:          options.HostASSPort,
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
		ASSDownloadURL:       options.ASSDownloadURL,
		ASSSHA256:            assSHA256,
		BuildRevision:        buildRevision,
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
	options.ContainerName = normalizeCloudAgentContainerName(options.ContainerName, safeUser)
	if options.ContainerName == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent container name is required")
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
	if options.ContainerASSPort <= 0 {
		options.ContainerASSPort = defaultCloudAgentContainerASSPort
	}
	if options.HostVNCPort <= 0 {
		options.HostVNCPort = cloudAgentHostPort(options.UserID, worker.ID, defaultCloudAgentHostVNCBasePort)
	}
	if options.HostChromePort <= 0 {
		options.HostChromePort = cloudAgentHostPort(options.UserID, worker.ID+"-chrome", defaultCloudAgentHostChromeBasePort)
	}
	if options.HostASSPort <= 0 {
		options.HostASSPort = cloudAgentHostPort(options.UserID, worker.ID+"-ass", defaultCloudAgentHostASSBasePort)
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
	options.ASSDownloadURL = strings.TrimSpace(options.ASSDownloadURL)
	if options.ASSDownloadURL == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent aiyolo-ass download url is required")
	}
	options.ASSSHA256URL = strings.TrimSpace(options.ASSSHA256URL)
	if options.ASSSHA256URL == "" {
		return CloudAgentStartOptions{}, fmt.Errorf("cloud agent aiyolo-ass sha256 url is required")
	}
	return options, nil
}

func resolveCloudAgentASSSHA256(ctx context.Context, checksumURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(checksumURL), nil)
	if err != nil {
		return "", fmt.Errorf("build cloud agent aiyolo-ass checksum request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("download cloud agent aiyolo-ass checksum: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return "", fmt.Errorf("download cloud agent aiyolo-ass checksum: unexpected status %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 512))
	if err != nil {
		return "", fmt.Errorf("read cloud agent aiyolo-ass checksum: %w", err)
	}
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return "", fmt.Errorf("cloud agent aiyolo-ass checksum is empty")
	}
	checksum := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(checksum) != sha256.Size*2 {
		return "", fmt.Errorf("cloud agent aiyolo-ass checksum must be %d hex chars", sha256.Size*2)
	}
	if _, err := hex.DecodeString(checksum); err != nil {
		return "", fmt.Errorf("cloud agent aiyolo-ass checksum is invalid: %w", err)
	}
	return checksum, nil
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

func normalizeCloudAgentContainerName(value string, safeUser string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "aiyolo-cloud-agent-" + firstNonEmpty(strings.TrimSpace(safeUser), "default")
	}
	if isValidDockerContainerName(value) {
		return value
	}
	sanitized := sanitizeCloudAgentSegment(value)
	if sanitized == "default" {
		return "aiyolo-cloud-agent-" + firstNonEmpty(strings.TrimSpace(safeUser), "default")
	}
	return sanitized
}

func isValidDockerContainerName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for index, ch := range value {
		if index == 0 && !isCloudAgentContainerNameAlnum(ch) {
			return false
		}
		if !isCloudAgentContainerNameAlnum(ch) && ch != '-' && ch != '_' && ch != '.' {
			return false
		}
	}
	return true
}

func isCloudAgentContainerNameAlnum(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func cloudAgentHostPort(userID, discriminator string, base int) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSpace(userID) + ":" + strings.TrimSpace(discriminator)))
	return base + int(hash.Sum32()%defaultCloudAgentHostPortSpan)
}

func cloudAgentContainerEnv(options CloudAgentStartOptions) map[string]string {
	apiRoot := strings.TrimRight(options.ConsoleBaseURL, "/")
	defaultModel := strings.TrimSpace(options.DefaultModel)
	containerASSPort := options.ContainerASSPort
	if containerASSPort <= 0 {
		containerASSPort = defaultCloudAgentContainerASSPort
	}
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
		"AIYOLO_ASS_WORKSPACE_ROOT":            options.WorkspacePath,
		"AIYOLO_ASS_USER":                      defaultCloudAgentClaudeUser,
		"AIYOLO_ASS_HOME":                      defaultCloudAgentClaudeHome,
		"AIYOLO_ASS_SOCKET_PATH":               cloudAgentASSSocketPath,
		"AIYOLO_ASS_HTTP_ADDR":                 fmt.Sprintf("0.0.0.0:%d", containerASSPort),
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
	script := cloudAgentAssetString("cloud-agent/remote_ensure.py.tmpl")
	script = strings.ReplaceAll(script, "\t", "    ")
	script = strings.ReplaceAll(script, "__PAYLOAD_JSON__", strconv.Quote(payloadJSON))
	return "python3 - <<'PY'\n" + script + "\nPY"
}

var cloudAgentBuildContextFiles = map[string]string{
	"Dockerfile":                        cloudAgentAssetString("cloud-agent/Dockerfile"),
	"cloud-agent-base.json":             cloudAgentAssetString("cloud-agent/cloud-agent-base.json"),
	"aiyolo-cloud-agent-entrypoint":     cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-entrypoint"),
	"aiyolo-cloud-agent-info":           cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-info"),
	"aiyolo-cloud-agent-start-display":  cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-start-display"),
	"aiyolo-cloud-agent-open-chrome":    cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-open-chrome"),
	"aiyolo-cloud-agent-start-docker":   cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-start-docker"),
	"aiyolo-cloud-agent-start-services": cloudAgentAssetString("cloud-agent/aiyolo-cloud-agent-start-services"),
}

func cloudAgentBuildRevision(options CloudAgentStartOptions, files map[string]string, assSHA256 string) string {
	hash := sha256.New()
	write := func(label string, value string) {
		_, _ = hash.Write([]byte(label))
		_, _ = hash.Write([]byte("\n"))
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte("\n"))
	}
	write("ubuntu_release", options.UbuntuRelease)
	write("ubuntu_series", options.UbuntuSeries)
	write("ubuntu_mirror", options.UbuntuMirror)
	write("chrome_deb_url", options.ChromeDEBURL)
	write("rootfs_url", options.RootFSURL)
	write("rootfs_index_url", options.RootFSIndexURL)
	write("ass_download_url", options.ASSDownloadURL)
	write("ass_sha256", assSHA256)
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		write("file:"+key, files[key])
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
