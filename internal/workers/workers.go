package workers

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/zltl/aiyolo/internal/domain"
)

type ProbeResult struct {
	OSName           string    `json:"osName"`
	UbuntuVersion    string    `json:"ubuntuVersion"`
	DockerInstalled  bool      `json:"dockerInstalled"`
	DockerVersion    string    `json:"dockerVersion,omitempty"`
	ProxyReachable   bool      `json:"proxyReachable"`
	ProxyEndpoint    string    `json:"proxyEndpoint,omitempty"`
	ProxyError       string    `json:"proxyError,omitempty"`
	DataRootWritable bool      `json:"dataRootWritable"`
	LSBLKJSON        string    `json:"lsblkJSON,omitempty"`
	MountsJSON       string    `json:"mountsJSON,omitempty"`
	CheckedAt        time.Time `json:"checkedAt"`
}

type BootstrapPlan struct {
	ProxyEnv map[string]string `json:"proxyEnv,omitempty"`
	Script   string            `json:"script"`
	Summary  string            `json:"summary"`
}

func ExecuteBootstrap(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, plan BootstrapPlan) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return "", err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return "", err
	}
	client, err := dialSSH(worker, key)
	if err != nil {
		return "", err
	}
	defer client.Close()
	output, err := runSSHScript(client, "bash -seu", plan.Script)
	if err != nil {
		return strings.TrimSpace(output), fmt.Errorf("execute bootstrap: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func Probe(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile) (ProbeResult, error) {
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return ProbeResult{}, err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return ProbeResult{}, err
	}
	client, err := dialSSH(worker, key)
	if err != nil {
		return ProbeResult{}, err
	}
	defer client.Close()

	result := ProbeResult{CheckedAt: time.Now().UTC(), ProxyReachable: true}
	osRelease, err := runSSHCommand(client, "cat /etc/os-release")
	if err != nil {
		return result, fmt.Errorf("read /etc/os-release: %w", err)
	}
	osValues := parseOSRelease(osRelease)
	result.OSName = firstNonEmpty(osValues["PRETTY_NAME"], osValues["NAME"])
	result.UbuntuVersion = strings.TrimSpace(osValues["VERSION_ID"])

	lsblkJSON, err := runSSHCommand(client, "lsblk -J -o NAME,KNAME,PATH,SIZE,FSTYPE,MOUNTPOINT,TYPE,RO,RM")
	if err != nil {
		return result, fmt.Errorf("run lsblk probe: %w", err)
	}
	result.LSBLKJSON = strings.TrimSpace(lsblkJSON)

	mountsJSON, err := runSSHCommand(client, "findmnt -J")
	if err != nil {
		mountsJSON, err = runSSHCommand(client, "cat /proc/mounts")
		if err != nil {
			return result, fmt.Errorf("read mount table: %w", err)
		}
	}
	result.MountsJSON = strings.TrimSpace(mountsJSON)

	dockerVersion, err := runSSHCommand(client, `bash -lc 'if command -v docker >/dev/null 2>&1; then docker version --format "{{.Server.Version}}"; fi'`)
	if err != nil {
		return result, fmt.Errorf("probe docker: %w", err)
	}
	result.DockerVersion = strings.TrimSpace(dockerVersion)
	result.DockerInstalled = result.DockerVersion != ""

	writable, err := runSSHCommand(client, bashCommand(fmt.Sprintf("probe_path=%s; if [ -e \"$probe_path\" ]; then target=\"$probe_path\"; else target=$(dirname \"$probe_path\"); fi; if [ -w \"$target\" ]; then printf true; else printf false; fi", shellQuote(worker.DataRoot))))
	if err != nil {
		return result, fmt.Errorf("probe data root writability: %w", err)
	}
	result.DataRootWritable = strings.EqualFold(strings.TrimSpace(writable), "true")

	if proxy.Type != domain.ProxyTypeDirect && strings.TrimSpace(proxy.Endpoint) != "" {
		result.ProxyEndpoint = strings.TrimSpace(proxy.Endpoint)
		reachable, probeErr := probeProxyReachability(client, proxy)
		result.ProxyReachable = reachable
		if probeErr != nil {
			result.ProxyError = probeErr.Error()
		}
	}

	return result, nil
}

func BuildBootstrapPlan(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) BootstrapPlan {
	worker, _ = domain.NormalizeWorkerServer(worker)
	proxyEnv := RenderProxyEnv(proxy)
	lines := []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
	}
	if len(proxyEnv) > 0 {
		keys := make([]string, 0, len(proxyEnv))
		for key := range proxyEnv {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("export %s=%s", key, shellQuote(proxyEnv[key])))
		}
		lines = append(lines,
			"install -d -m 0755 /etc/systemd/system/docker.service.d",
			"cat > /etc/systemd/system/docker.service.d/http-proxy.conf <<'EOF'",
			"[Service]",
		)
		for _, key := range keys {
			if key == strings.ToUpper(key) {
				lines = append(lines, fmt.Sprintf("Environment=\"%s=%s\"", key, proxyEnv[key]))
			}
		}
		lines = append(lines, "EOF")
	}
	lines = append(lines,
		"if ! command -v docker >/dev/null 2>&1; then",
		"  apt-get update",
		"  apt-get install -y ca-certificates curl gnupg",
		"  install -d -m 0755 /etc/apt/keyrings",
		"  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg",
		"  chmod a+r /etc/apt/keyrings/docker.gpg",
		"  . /etc/os-release",
		"  echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable\" > /etc/apt/sources.list.d/docker.list",
		"  apt-get update",
		"  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"fi",
		fmt.Sprintf("install -d -m 0755 %s", shellQuote(worker.DataRoot)),
	)
	if len(disks) == 0 {
		lines = append(lines, fmt.Sprintf("# no explicit data disks selected; workspace remains under %s", worker.DataRoot))
	} else {
		for _, disk := range disks {
			lines = append(lines,
				fmt.Sprintf("# explicit data disk selection: %s -> %s", disk.DevicePath, disk.MountPath),
				fmt.Sprintf("install -d -m 0755 %s", shellQuote(path.Clean(disk.MountPath))),
			)
		}
	}
	lines = append(lines,
		"systemctl daemon-reload || true",
		"systemctl enable --now docker || true",
		"# TODO: upload and enable the local-only aiyolo-workerd runtime service.",
	)
	return BootstrapPlan{
		ProxyEnv: proxyEnv,
		Script:   strings.Join(lines, "\n") + "\n",
		Summary:  fmt.Sprintf("Bootstrap plan prepared for %s with %d explicit data disk selection(s).", worker.ID, len(disks)),
	}
}

func RenderProxyEnv(profile domain.ProxyProfile) map[string]string {
	normalized, err := domain.NormalizeProxyProfile(domain.ProxyProfile{
		ID:                       firstNonEmpty(strings.TrimSpace(profile.ID), "worker-proxy"),
		Name:                     profile.Name,
		Type:                     profile.Type,
		Endpoint:                 profile.Endpoint,
		Auth:                     profile.Auth,
		Region:                   profile.Region,
		TimeoutSeconds:           profile.TimeoutSeconds,
		StreamIdleTimeoutSeconds: profile.StreamIdleTimeoutSeconds,
		HealthCheckURL:           profile.HealthCheckURL,
		Status:                   profile.Status,
	})
	if err != nil || normalized.Type == domain.ProxyTypeDirect || strings.TrimSpace(normalized.Endpoint) == "" {
		return nil
	}
	proxyURL, err := url.Parse(normalized.Endpoint)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(normalized.Auth) != "" && proxyURL.User == nil {
		username, password, ok := strings.Cut(normalized.Auth, ":")
		if ok {
			proxyURL.User = url.UserPassword(username, password)
		} else {
			proxyURL.User = url.User(normalized.Auth)
		}
	}
	endpoint := proxyURL.String()
	return map[string]string{
		"ALL_PROXY":   endpoint,
		"HTTP_PROXY":  endpoint,
		"HTTPS_PROXY": endpoint,
		"all_proxy":   endpoint,
		"http_proxy":  endpoint,
		"https_proxy": endpoint,
	}
}

func dialSSH(worker domain.WorkerServer, key domain.WorkerSSHKey) (*ssh.Client, error) {
	signer, err := parsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:            worker.SSHUsername,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	address := net.JoinHostPort(worker.SSHHost, strconv.Itoa(worker.SSHPort))
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return nil, fmt.Errorf("dial ssh %s: %w", address, err)
	}
	return client, nil
}

func parsePrivateKey(key domain.WorkerSSHKey) (ssh.Signer, error) {
	if strings.TrimSpace(key.PrivateKey) == "" {
		return nil, fmt.Errorf("worker ssh key %s does not include a private key", key.ID)
	}
	if strings.TrimSpace(key.PrivateKeyPassphrase) != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(key.PrivateKey), []byte(key.PrivateKeyPassphrase))
	}
	return ssh.ParsePrivateKey([]byte(key.PrivateKey))
}

func runSSHCommand(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	output, err := session.CombinedOutput(command)
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runSSHScript(client *ssh.Client, command string, script string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(script)
	output, err := session.CombinedOutput(command)
	if err != nil {
		return string(output), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func parseOSRelease(raw string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return values
}

func probeProxyReachability(client *ssh.Client, profile domain.ProxyProfile) (bool, error) {
	host, port, err := installProxyEndpointAddress(profile)
	if err != nil {
		return false, err
	}
	result, err := runSSHCommand(client, bashCommand(fmt.Sprintf("timeout 5 bash -lc 'exec 3<>/dev/tcp/%s/%d' >/dev/null 2>&1 && printf true || printf false", host, port)))
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(result), "true"), nil
}

func installProxyEndpointAddress(profile domain.ProxyProfile) (string, int, error) {
	normalized, err := domain.NormalizeProxyProfile(domain.ProxyProfile{
		ID:                       firstNonEmpty(strings.TrimSpace(profile.ID), "worker-proxy"),
		Name:                     profile.Name,
		Type:                     profile.Type,
		Endpoint:                 profile.Endpoint,
		Auth:                     profile.Auth,
		Region:                   profile.Region,
		TimeoutSeconds:           profile.TimeoutSeconds,
		StreamIdleTimeoutSeconds: profile.StreamIdleTimeoutSeconds,
		HealthCheckURL:           profile.HealthCheckURL,
		Status:                   profile.Status,
	})
	if err != nil {
		return "", 0, fmt.Errorf("invalid install proxy endpoint: %w", err)
	}
	endpoint, err := url.Parse(strings.TrimSpace(normalized.Endpoint))
	if err != nil {
		return "", 0, fmt.Errorf("invalid install proxy endpoint: %w", err)
	}
	host := endpoint.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("invalid install proxy endpoint")
	}
	if endpoint.Port() != "" {
		parsedPort, err := strconv.Atoi(endpoint.Port())
		if err != nil {
			return "", 0, fmt.Errorf("invalid install proxy port: %w", err)
		}
		return host, parsedPort, nil
	}
	switch strings.ToLower(endpoint.Scheme) {
	case "http":
		return host, 80, nil
	case "https":
		return host, 443, nil
	case "socks5":
		return host, 1080, nil
	default:
		return "", 0, fmt.Errorf("install proxy endpoint is missing a port")
	}
}

func bashCommand(script string) string {
	return "bash -lc " + shellQuote(script)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
