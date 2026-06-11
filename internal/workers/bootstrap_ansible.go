package workers

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/zltl/aiyolo/internal/domain"
)

const (
	workerdServiceName = "aiyolo-workerd"
	workerdRuntimeUser = "aiyolo-workerd"
	workerdListenHost  = "127.0.0.1"
	workerdListenPort  = 17810
	workerdConfigPath  = "/etc/aiyolo/workerd.json"
	workerdScriptPath  = "/usr/local/lib/aiyolo/aiyolo_workerd.py"
	workerdDockerSocket = "/var/run/docker.sock"
)

type BootstrapPlan struct {
	ProxyEnv  map[string]string `json:"proxyEnv,omitempty"`
	Script    string            `json:"script"`
	Summary   string            `json:"summary"`
	Inventory string            `json:"inventory,omitempty"`
	VarsJSON  string            `json:"varsJSON,omitempty"`
	Playbook  string            `json:"playbook,omitempty"`
}

type BootstrapHealth struct {
	Status             string `json:"status"`
	WorkerID           string `json:"worker_id"`
	DataRoot           string `json:"data_root"`
	WorkspaceRoot      string `json:"workspace_root"`
	DockerSocketExists bool   `json:"docker_socket_exists"`
}

type bootstrapDisk struct {
	DevicePath string `json:"device_path"`
	MountPath  string `json:"mount_path"`
}

type bootstrapVars struct {
	WorkerID                    string            `json:"worker_id"`
	WorkerExpectedUbuntuVersion string            `json:"worker_expected_ubuntu_version,omitempty"`
	WorkerDataRoot              string            `json:"worker_data_root"`
	WorkerDockerDataRoot        string            `json:"worker_docker_data_root"`
	WorkerWorkspaceRoot         string            `json:"worker_workspace_root"`
	WorkerRuntimeStateRoot      string            `json:"worker_runtime_state_root"`
	WorkerRuntimeServiceName    string            `json:"worker_runtime_service_name"`
	WorkerRuntimeUser           string            `json:"worker_runtime_user"`
	WorkerRuntimeListenHost     string            `json:"worker_runtime_listen_host"`
	WorkerRuntimeListenPort     int               `json:"worker_runtime_listen_port"`
	WorkerRuntimeConfigPath     string            `json:"worker_runtime_config_path"`
	WorkerRuntimeScriptPath     string            `json:"worker_runtime_script_path"`
	WorkerRuntimeDockerSocket   string            `json:"worker_runtime_docker_socket_path"`
	WorkerRuntimeHealthURL      string            `json:"worker_runtime_health_url"`
	WorkerMountPaths            []string          `json:"worker_mount_paths,omitempty"`
	WorkerDataDisks             []bootstrapDisk   `json:"worker_data_disks,omitempty"`
	WorkerProxyEnv              map[string]string `json:"worker_proxy_env,omitempty"`
	DockerProxyEnvKeys          []string          `json:"docker_proxy_env_keys,omitempty"`
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
	if strings.TrimSpace(plan.Inventory) == "" || strings.TrimSpace(plan.VarsJSON) == "" || strings.TrimSpace(plan.Playbook) == "" {
		return "", fmt.Errorf("bootstrap plan is missing ansible assets")
	}
	tempDir, err := os.MkdirTemp("", "aiyolo-worker-ansible-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	if err := materializeAnsibleAssets(tempDir); err != nil {
		return "", err
	}
	keyPayload, err := ansiblePrivateKeyPEM(key)
	if err != nil {
		return "", err
	}
	inventoryPath := filepath.Join(tempDir, "inventory.ini")
	varsPath := filepath.Join(tempDir, "vars.json")
	playbookPath := filepath.Join(tempDir, "worker-bootstrap.yml")
	keyPath := filepath.Join(tempDir, "worker.key")
	for _, file := range []struct {
		path    string
		payload string
		mode    os.FileMode
	}{
		{path: inventoryPath, payload: ensureTrailingNewline(plan.Inventory), mode: 0o644},
		{path: varsPath, payload: ensureTrailingNewline(plan.VarsJSON), mode: 0o644},
		{path: playbookPath, payload: ensureTrailingNewline(plan.Playbook), mode: 0o644},
		{path: keyPath, payload: string(keyPayload), mode: 0o600},
	} {
		if err := os.WriteFile(file.path, []byte(file.payload), file.mode); err != nil {
			return "", err
		}
	}
	command := exec.CommandContext(
		ctx,
		"ansible-playbook",
		"-i", inventoryPath,
		playbookPath,
		"-u", worker.SSHUsername,
		"--private-key", keyPath,
		"--ssh-common-args", "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		"-e", "@"+varsPath,
	)
	command.Dir = tempDir
	command.Env = append(os.Environ(),
		"ANSIBLE_CONFIG="+filepath.Join(tempDir, "ansible.cfg"),
		"ANSIBLE_FORCE_COLOR=False",
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"PY_COLORS=0",
	)
	output, err := command.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", fmt.Errorf("execute ansible bootstrap: %w", err)
		}
		return trimmed, fmt.Errorf("execute ansible bootstrap: %w", err)
	}
	return trimmed, nil
}

func VerifyBootstrap(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey) (BootstrapHealth, error) {
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return BootstrapHealth{}, err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return BootstrapHealth{}, err
	}
	command := fmt.Sprintf(`python3 - <<'PY'
import json
import urllib.request

with urllib.request.urlopen(%q, timeout=5) as response:
    payload = json.load(response)

if payload.get("status") != "ready":
    raise SystemExit(json.dumps(payload, sort_keys=True))
if payload.get("worker_id") != %q:
    raise SystemExit(json.dumps(payload, sort_keys=True))
print(json.dumps(payload, sort_keys=True))
PY`, workerdHealthURL(), worker.ID)
	var output string
	if WorkerIsLocal(worker) {
		output, err = runLocalCommand(bashCommand(command))
	} else {
		client, dialErr := dialSSH(worker, key)
		if dialErr != nil {
			return BootstrapHealth{}, dialErr
		}
		defer client.Close()
		output, err = runSSHCommand(client, bashCommand(command))
	}
	if err != nil {
		return BootstrapHealth{}, fmt.Errorf("verify bootstrap health: %w", err)
	}
	var health BootstrapHealth
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &health); err != nil {
		return BootstrapHealth{}, fmt.Errorf("parse bootstrap health: %w", err)
	}
	return health, nil
}

func BuildBootstrapPlan(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) BootstrapPlan {
	worker, _ = domain.NormalizeWorkerServer(worker)
	disks, _ = domain.NormalizeWorkerDisks(disks)
	sort.Slice(disks, func(i, j int) bool {
		if disks[i].MountPath != disks[j].MountPath {
			return disks[i].MountPath < disks[j].MountPath
		}
		return disks[i].DevicePath < disks[j].DevicePath
	})
	proxyEnv := RenderProxyEnv(proxy)
	playbook := mustReadAnsibleAsset("worker-bootstrap.yml")
	inventory := renderBootstrapInventory(worker)
	varsJSON := renderBootstrapVarsJSON(worker, disks, proxyEnv)
	return BootstrapPlan{
		ProxyEnv:  proxyEnv,
		Inventory: inventory,
		VarsJSON:  varsJSON,
		Playbook:  playbook,
		Script:    renderBootstrapPreview(inventory, varsJSON, playbook),
		Summary:   fmt.Sprintf("Ansible bootstrap plan prepared for %s with %d explicit data disk selection(s), runtime %s, and post-init health verification.", worker.ID, len(disks), workerdServiceName),
	}
}

func mustReadAnsibleAsset(name string) string {
	payload, err := readAnsibleAsset(name)
	if err != nil {
		panic(err)
	}
	return payload
}

func readAnsibleAsset(name string) (string, error) {
	payload, err := ansibleAssets.ReadFile(path.Join("ansible", name))
	if err != nil {
		return "", fmt.Errorf("read ansible asset %s: %w", name, err)
	}
	return string(payload), nil
}

func materializeAnsibleAssets(rootDir string) error {
	return fs.WalkDir(ansibleAssets, "ansible", func(assetPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if assetPath == "ansible" {
			return nil
		}
		relativePath := strings.TrimPrefix(assetPath, "ansible/")
		targetPath := filepath.Join(rootDir, filepath.FromSlash(relativePath))
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		payload, err := ansibleAssets.ReadFile(assetPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, payload, 0o644)
	})
}

func renderBootstrapInventory(worker domain.WorkerServer) string {
	return fmt.Sprintf("[worker]\nbootstrap-target ansible_host=%s ansible_port=%d\n", worker.SSHHost, worker.SSHPort)
}

func renderBootstrapVarsJSON(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxyEnv map[string]string) string {
	var mountPaths []string
	var dataDisks []bootstrapDisk
	for _, disk := range disks {
		mountPaths = append(mountPaths, strings.TrimSpace(disk.MountPath))
		dataDisks = append(dataDisks, bootstrapDisk{
			DevicePath: strings.TrimSpace(disk.DevicePath),
			MountPath:  strings.TrimSpace(disk.MountPath),
		})
	}
	workspaceRoot := workerdWorkspaceRoot(worker)
	payload, err := json.MarshalIndent(bootstrapVars{
		WorkerID:                    worker.ID,
		WorkerExpectedUbuntuVersion: worker.ExpectedUbuntuVersion,
		WorkerDataRoot:              worker.DataRoot,
		WorkerDockerDataRoot:        workerdDockerDataRoot(worker),
		WorkerWorkspaceRoot:         workspaceRoot,
		WorkerRuntimeStateRoot:      workerdStateRoot(worker),
		WorkerRuntimeServiceName:    workerdServiceName,
		WorkerRuntimeUser:           workerdRuntimeUser,
		WorkerRuntimeListenHost:     workerdListenHost,
		WorkerRuntimeListenPort:     workerdListenPort,
		WorkerRuntimeConfigPath:     workerdConfigPath,
		WorkerRuntimeScriptPath:     workerdScriptPath,
		WorkerRuntimeDockerSocket:   workerdDockerSocket,
		WorkerRuntimeHealthURL:      workerdHealthURL(),
		WorkerMountPaths:            mountPaths,
		WorkerDataDisks:             dataDisks,
		WorkerProxyEnv:              proxyEnv,
		DockerProxyEnvKeys:          uppercaseProxyEnvKeys(proxyEnv),
	}, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func uppercaseProxyEnvKeys(proxyEnv map[string]string) []string {
	if len(proxyEnv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(proxyEnv))
	for key := range proxyEnv {
		if key == strings.ToUpper(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func renderBootstrapPreview(inventory, varsJSON, playbook string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"# ansible inventory\n%s\n\n# ansible vars\n%s\n\n# ansible playbook\n%s\n",
		strings.TrimSpace(inventory),
		strings.TrimSpace(varsJSON),
		strings.TrimSpace(playbook),
	)) + "\n"
}

func ensureTrailingNewline(value string) string {
	if strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func ansiblePrivateKeyPEM(key domain.WorkerSSHKey) ([]byte, error) {
	privateKey := strings.TrimSpace(key.PrivateKey)
	if privateKey == "" {
		return nil, fmt.Errorf("worker ssh key %s does not include a private key", key.ID)
	}
	if strings.TrimSpace(key.PrivateKeyPassphrase) == "" {
		return []byte(ensureTrailingNewline(privateKey)), nil
	}
	parsed, err := ssh.ParseRawPrivateKeyWithPassphrase([]byte(key.PrivateKey), []byte(key.PrivateKeyPassphrase))
	if err != nil {
		return nil, fmt.Errorf("worker ssh private key is invalid: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(parsed)
	if err != nil {
		return nil, fmt.Errorf("marshal worker ssh private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), nil
}

func workerdWorkspaceRoot(worker domain.WorkerServer) string {
	return path.Join(worker.DataRoot, "workspace")
}

func workerdDockerDataRoot(worker domain.WorkerServer) string {
	return path.Join(worker.DataRoot, "docker")
}

func workerdStateRoot(worker domain.WorkerServer) string {
	return path.Join(worker.DataRoot, "workerd")
}

func workerdHealthURL() string {
	return fmt.Sprintf("http://%s:%d/readyz", workerdListenHost, workerdListenPort)
}
