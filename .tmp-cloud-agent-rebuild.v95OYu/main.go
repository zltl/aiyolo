package main

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "net/url"
  "os"
  "sort"
  "strings"
  "time"

  "golang.org/x/crypto/ssh"

  "github.com/zltl/aiyolo/internal/app"
  "github.com/zltl/aiyolo/internal/domain"
  "github.com/zltl/aiyolo/internal/storage"
  workerops "github.com/zltl/aiyolo/internal/workers"
)

const cloudAgentImage = "aiyolo/local-cloud-agent:ubuntu-26.04-v4"

func main() {
  ctx := context.Background()
  v, _, err := app.NewViper("aiyolo.private.yaml")
  if err != nil {
    panic(err)
  }
  cfg, err := app.LoadConfig(v)
  if err != nil {
    panic(err)
  }
  store, err := storage.OpenPostgres(ctx, cfg.DatabaseURL, cfg.SecretKey)
  if err != nil {
    panic(err)
  }
  defer store.Close()

  worker, err := store.GetWorkerServer(ctx, "worker-0")
  if err != nil {
    panic(err)
  }
  key, err := store.GetWorkerSSHKey(ctx, worker.SSHKeyID)
  if err != nil {
    panic(err)
  }
  proxy, err := store.GetProxyProfile(ctx, worker.InstallProxyID)
  if err != nil {
    if errors.Is(err, storage.ErrNotFound) && strings.TrimSpace(worker.InstallProxyID) == domain.ProxyTypeDirect {
      proxy = domain.ProxyProfile{ID: domain.ProxyTypeDirect, Type: domain.ProxyTypeDirect}
    } else {
      panic(err)
    }
  }
  accounts, err := store.ListCloudAgentAccounts(ctx, cfg.AdminEmail, worker.ID)
  if err != nil {
    panic(err)
  }
  if len(accounts) == 0 {
    panic("no cloud agent account found for worker-0")
  }
  sort.Slice(accounts, func(i, j int) bool {
    return accounts[i].UpdatedAt.After(accounts[j].UpdatedAt)
  })
  account := accounts[0]
  for _, candidate := range accounts {
    if strings.TrimSpace(candidate.AgentType) == domain.CloudAgentTypeClaudeCode {
      account = candidate
      break
    }
  }

  defaultModel := firstNonEmpty(account.ModelPublicName, "deepseek-v4-pro")
  workspacePath := firstNonEmpty(account.WorkspacePath, domain.DefaultCloudAgentWorkspacePath)
  openURL := strings.TrimRight(cfg.CodexPublicBaseURL, "/") + "/console/chat"
  sessions, err := store.ListCloudAgentSessions(ctx, cfg.AdminEmail, worker.ID, 10)
  if err == nil {
    for _, session := range sessions {
      if strings.TrimSpace(session.Status) == domain.CloudAgentSessionStatusActive && strings.TrimSpace(session.ChatSessionID) != "" {
        openURL = strings.TrimRight(cfg.CodexPublicBaseURL, "/") + "/console/chat?session=" + url.QueryEscape(strings.TrimSpace(session.ChatSessionID))
        break
      }
    }
  }

  client, err := sshClient(worker, key)
  if err != nil {
    panic(err)
  }
  defer client.Close()

  containerName := firstNonEmpty(account.ContainerName, "aiyolo-cloud-agent-i-quant67-com")
  cleanupScript := strings.Join([]string{
    "set -euo pipefail",
    "docker rm -f " + shellQuote(containerName) + " >/dev/null 2>&1 || true",
    "docker image rm -f " + shellQuote(cloudAgentImage) + " >/dev/null 2>&1 || true",
  }, "\n")
  if output, err := remoteRun(client, cleanupScript); err != nil {
    panic(fmt.Sprintf("cleanup remote cloud-agent: %v\n%s", err, output))
  }

  instance, err := workerops.EnsureCloudAgent(ctx, worker, key, proxy, workerops.CloudAgentStartOptions{
    UserID:         cfg.AdminEmail,
    AgentType:      firstNonEmpty(account.AgentType, domain.CloudAgentTypeClaudeCode),
    Image:          cloudAgentImage,
    ContainerName:  account.ContainerName,
    WorkspacePath:  workspacePath,
    APIBaseURL:     strings.TrimRight(cfg.CodexPublicBaseURL, "/") + "/v1",
    ConsoleBaseURL: strings.TrimRight(cfg.CodexPublicBaseURL, "/"),
    APIKey:         account.Credential,
    DefaultModel:   defaultModel,
    AllowedModels:  []string{defaultModel},
    OpenURL:        openURL,
  })
  if err != nil {
    panic(err)
  }

  verifyScript := strings.Join([]string{
    "set -euo pipefail",
    "docker exec " + shellQuote(instance.ContainerName) + " bash -lc " + shellQuote(strings.Join([]string{
      "set -euo pipefail",
      "cd /workspace",
      "rm -f claude-baked-smoke.txt /tmp/claude-baked-debug.log",
      "printf 'ANTHROPIC_BASE_URL=%s\\n' \"${ANTHROPIC_BASE_URL:-}\"",
      "printf 'ANTHROPIC_MODEL=%s\\n' \"${ANTHROPIC_MODEL:-}\"",
      "printf 'ANTHROPIC_CUSTOM_MODEL_OPTION=%s\\n' \"${ANTHROPIC_CUSTOM_MODEL_OPTION:-}\"",
      "claude --version",
      "claude --bare -p --permission-mode acceptEdits --tools Edit,Read --max-turns 8 --debug api --debug-file /tmp/claude-baked-debug.log \"Use only the file editing tool. Create a file named claude-baked-smoke.txt in the current directory containing exactly CLAUDE_BAKED_OK followed by a newline. After writing the file, reply with exactly DONE.\"",
      "echo === smoke-file",
      "wc -c claude-baked-smoke.txt",
      "printf 'CONTENT='",
      "cat claude-baked-smoke.txt",
      "echo === debug-url",
      "grep -n \"https://aiyolo.quant67.com\\|/v1/messages\" /tmp/claude-baked-debug.log | sed -n \"1,10p\"",
      "echo === debug-model",
      "grep -n 'deepseek-v4-pro' /tmp/claude-baked-debug.log | sed -n \"1,10p\"",
    }, "\n")),
  }, "\n")
  verifyOutput, err := remoteRun(client, verifyScript)
  if err != nil {
    panic(fmt.Sprintf("verify rebuilt cloud-agent: %v\n%s", err, verifyOutput))
  }

  payload := map[string]any{
    "instance": instance,
    "verify":   verifyOutput,
  }
  encoder := json.NewEncoder(os.Stdout)
  encoder.SetIndent("", "  ")
  if err := encoder.Encode(payload); err != nil {
    panic(err)
  }
}

func firstNonEmpty(values ...string) string {
  for _, value := range values {
    if strings.TrimSpace(value) != "" {
      return strings.TrimSpace(value)
    }
  }
  return ""
}

func sshClient(worker domain.WorkerServer, key domain.WorkerSSHKey) (*ssh.Client, error) {
  signer, err := privateKeySigner(key)
  if err != nil {
    return nil, err
  }
  config := &ssh.ClientConfig{
    User:            worker.SSHUsername,
    Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
    HostKeyCallback: ssh.InsecureIgnoreHostKey(),
    Timeout:         30 * time.Second,
  }
  return ssh.Dial("tcp", fmt.Sprintf("%s:%d", worker.SSHHost, worker.SSHPort), config)
}

func privateKeySigner(key domain.WorkerSSHKey) (ssh.Signer, error) {
  privateKey := []byte(strings.TrimSpace(key.PrivateKey))
  if len(privateKey) == 0 {
    return nil, fmt.Errorf("worker ssh key %s has no private key material", key.ID)
  }
  passphrase := strings.TrimSpace(key.PrivateKeyPassphrase)
  if passphrase != "" {
    return ssh.ParsePrivateKeyWithPassphrase(privateKey, []byte(passphrase))
  }
  return ssh.ParsePrivateKey(privateKey)
}

func remoteRun(client *ssh.Client, script string) (string, error) {
  session, err := client.NewSession()
  if err != nil {
    return "", err
  }
  defer session.Close()
  session.Stdin = strings.NewReader(script)
  output, err := session.CombinedOutput("bash")
  return string(output), err
}

func shellQuote(value string) string {
  return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
