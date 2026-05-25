package domain

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	StatusActive   = "active"

	ProxyTypeDirect = "direct"
	ProxyTypeHTTP   = "http"
	ProxyTypeSOCKS5 = "socks5"

	DefaultProviderTimeoutSeconds           = 90
	DefaultProviderStreamIdleTimeoutSeconds = 300
	DefaultProxyTimeoutSeconds              = 60
	DefaultProxyStreamIdleTimeoutSeconds    = 300
	DefaultWorkerSSHPort                    = 22
	DefaultWorkerExpectedUbuntuVersion      = "26.04"
	DefaultWorkerDataRoot                   = "/var/lib/aiyolo-agent"
	DefaultCloudAgentWorkspacePath          = "/workspace"

	WorkerStatusPending      = "pending"
	WorkerStatusReady        = "ready"
	WorkerStatusInitializing = "initializing"
	WorkerStatusFailed       = "failed"
	WorkerStatusDisabled     = "disabled"

	WorkerProbeStatusUnknown = "unknown"
	WorkerProbeStatusReady   = "ready"
	WorkerProbeStatusFailed  = "failed"

	WorkerInitActionBootstrap = "bootstrap"

	WorkerJobStatusQueued    = "queued"
	WorkerJobStatusRunning   = "running"
	WorkerJobStatusSucceeded = "succeeded"
	WorkerJobStatusFailed    = "failed"

	WorkerJobEventInfo  = "info"
	WorkerJobEventWarn  = "warn"
	WorkerJobEventError = "error"

	CloudAgentTypeClaudeCode = "claude-code"

	CloudAgentStatusStopped  = "stopped"
	CloudAgentStatusStarting = "starting"
	CloudAgentStatusRunning  = "running"
	CloudAgentStatusError    = "error"

	CloudAgentSessionStatusPending = "pending"
	CloudAgentSessionStatusActive  = "active"
	CloudAgentSessionStatusClosed  = "closed"
)

type APIKey struct {
	ID                 string
	Name               string
	KeyHash            string
	Prefix             string
	UserID             string
	OrganizationID     string
	ProjectID          string
	Status             string
	AllowedProtocols   []string
	AllowedModels      []string
	RPMLimit           int
	TPMLimit           int
	ConcurrentLimit    int
	DailyBudgetCents   int64
	MonthlyBudgetCents int64
	ExpiresAt          *time.Time
	CreatedAt          time.Time
	LastUsedAt         *time.Time
}

type Subject struct {
	APIKeyID           string
	UserID             string
	OrganizationID     string
	ProjectID          string
	AllowedProtocols   []string
	AllowedModels      []string
	RPMLimit           int
	TPMLimit           int
	ConcurrentLimit    int
	DailyBudgetCents   int64
	MonthlyBudgetCents int64
}

type CodexInstallToken struct {
	ID            string
	TokenHash     string
	CreatedBy     string
	Platform      string
	DefaultModel  string
	AllowedModels []string
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UsedAt        *time.Time
	APIKeyID      string
}

type QuotaRequest struct {
	RequestID               string
	APIKeyID                string
	UserID                  string
	ModelAlias              string
	RPMLimit                int
	TPMLimit                int
	ConcurrentLimit         int
	DailyBudgetCents        int64
	MonthlyBudgetCents      int64
	EstimatedTokens         int
	EstimatedCostMicroCents int64
	Now                     time.Time
}

type QuotaReservation struct {
	ID                      string
	RequestID               string
	APIKeyID                string
	UserID                  string
	ModelAlias              string
	WindowStart             time.Time
	EstimatedTokens         int
	EstimatedCostMicroCents int64
	ActualTokens            int
	ActualCostMicroCents    int64
	Status                  string
	CreatedAt               time.Time
	SettledAt               *time.Time
}

type Provider struct {
	ID                       string
	Name                     string
	BaseURL                  string
	Protocol                 string
	SupportedProtocols       []string
	MasterKey                string
	DefaultProxyID           string
	Priority                 int
	Weight                   int
	Status                   string
	TimeoutSeconds           int
	StreamIdleTimeoutSeconds int
	RateLimitHint            string
	LastHealthCheck          *time.Time
	LastError                string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type ModelRoute struct {
	PublicName       string
	ProviderID       string
	UpstreamModel    string
	Protocol         string
	AllowedProtocols []string
	ProxyProfileID   string
	PriceRuleID      string
	Enabled          bool
	Priority         int
	Weight           int
	ContextTokens    int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func NormalizeProtocol(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func NormalizeProtocols(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := NormalizeProtocol(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func SupportsProtocol(values []string, protocol string) bool {
	target := NormalizeProtocol(protocol)
	if target == "" {
		return false
	}
	for _, value := range NormalizeProtocols(values) {
		if value == target {
			return true
		}
	}
	return false
}

func ProviderSupportedProtocols(provider Provider) []string {
	values := NormalizeProtocols(provider.SupportedProtocols)
	if len(values) > 0 {
		return values
	}
	if protocol := NormalizeProtocol(provider.Protocol); protocol != "" {
		return []string{protocol}
	}
	return []string{ProtocolOpenAI}
}

func ProviderPrimaryProtocol(provider Provider) string {
	if protocol := NormalizeProtocol(provider.Protocol); protocol != "" {
		return protocol
	}
	values := ProviderSupportedProtocols(provider)
	if len(values) > 0 {
		return values[0]
	}
	return ProtocolOpenAI
}

func EffectiveProviderTimeoutSeconds(provider Provider) int {
	if provider.TimeoutSeconds > 0 {
		return provider.TimeoutSeconds
	}
	return DefaultProviderTimeoutSeconds
}

func EffectiveProviderStreamIdleTimeoutSeconds(provider Provider) int {
	return effectiveStreamIdleTimeoutSeconds(provider.StreamIdleTimeoutSeconds, EffectiveProviderTimeoutSeconds(provider), DefaultProviderStreamIdleTimeoutSeconds)
}

func RouteAllowedProtocols(route ModelRoute, provider Provider) []string {
	values := NormalizeProtocols(route.AllowedProtocols)
	if len(values) > 0 {
		return values
	}
	if protocol := NormalizeProtocol(route.Protocol); protocol != "" {
		return []string{protocol}
	}
	return ProviderSupportedProtocols(provider)
}

func RoutePrimaryProtocol(route ModelRoute, provider Provider) string {
	if protocol := NormalizeProtocol(route.Protocol); protocol != "" {
		return protocol
	}
	values := RouteAllowedProtocols(route, provider)
	if len(values) > 0 {
		return values[0]
	}
	return ProviderPrimaryProtocol(provider)
}

func IsOpenRouterProvider(provider Provider) bool {
	if strings.EqualFold(strings.TrimSpace(provider.ID), "openrouter") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(provider.BaseURL)), "openrouter.ai")
}

func IsDeepSeekProvider(provider Provider) bool {
	if strings.EqualFold(strings.TrimSpace(provider.ID), "deepseek") {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(provider.BaseURL)), "deepseek.com")
}

func ProviderBaseURLForProtocol(provider Provider, protocol string) string {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if trimmedBaseURL == "" {
		return ""
	}
	if !IsDeepSeekProvider(provider) || NormalizeProtocol(protocol) != ProtocolAnthropic {
		return trimmedBaseURL
	}
	parsed, err := url.Parse(trimmedBaseURL)
	if err != nil {
		return trimmedBaseURL
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(basePath, "/v1") {
		basePath = strings.TrimSuffix(basePath, "/v1")
	}
	if basePath == "/anthropic" || strings.HasPrefix(basePath, "/anthropic/") {
		return strings.TrimRight(parsed.String(), "/")
	}
	parsed.Path = path.Join(basePath, "/anthropic")
	return strings.TrimRight(parsed.String(), "/")
}

type ProxyProfile struct {
	ID                       string
	Name                     string
	Type                     string
	Endpoint                 string
	Auth                     string
	Region                   string
	TimeoutSeconds           int
	StreamIdleTimeoutSeconds int
	HealthCheckURL           string
	Status                   string
	LastError                string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

func EffectiveProxyProfileTimeoutSeconds(profile ProxyProfile) int {
	if profile.TimeoutSeconds > 0 {
		return profile.TimeoutSeconds
	}
	return DefaultProxyTimeoutSeconds
}

func EffectiveProxyProfileStreamIdleTimeoutSeconds(profile ProxyProfile) int {
	return effectiveStreamIdleTimeoutSeconds(profile.StreamIdleTimeoutSeconds, EffectiveProxyProfileTimeoutSeconds(profile), DefaultProxyStreamIdleTimeoutSeconds)
}

func effectiveStreamIdleTimeoutSeconds(explicit, regular, fallback int) int {
	if explicit > 0 {
		return explicit
	}
	if regular > fallback {
		return regular
	}
	return fallback
}

func NormalizeProxyProfile(profile ProxyProfile) (ProxyProfile, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
	profile.Endpoint = strings.TrimSpace(profile.Endpoint)
	profile.Auth = strings.TrimSpace(profile.Auth)
	profile.Region = strings.TrimSpace(profile.Region)
	profile.HealthCheckURL = strings.TrimSpace(profile.HealthCheckURL)
	profile.Status = strings.TrimSpace(profile.Status)

	if profile.ID == "" {
		return ProxyProfile{}, fmt.Errorf("proxy profile id is required")
	}
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	if profile.Type == "" {
		profile.Type = ProxyTypeDirect
	}
	switch profile.Type {
	case ProxyTypeDirect, ProxyTypeHTTP, ProxyTypeSOCKS5:
	default:
		return ProxyProfile{}, fmt.Errorf("unsupported proxy profile type %q for %s", profile.Type, profile.ID)
	}
	if profile.Type == ProxyTypeDirect {
		profile.Endpoint = ""
		profile.Auth = ""
	} else if profile.Endpoint == "" {
		return ProxyProfile{}, fmt.Errorf("proxy profile endpoint is required for %s", profile.ID)
	} else {
		endpoint, err := normalizeProxyEndpoint(profile.Type, profile.Endpoint)
		if err != nil {
			return ProxyProfile{}, err
		}
		profile.Endpoint = endpoint
	}
	if profile.TimeoutSeconds <= 0 {
		profile.TimeoutSeconds = DefaultProxyTimeoutSeconds
	}
	if profile.StreamIdleTimeoutSeconds <= 0 {
		profile.StreamIdleTimeoutSeconds = EffectiveProxyProfileStreamIdleTimeoutSeconds(profile)
	}
	if profile.Status == "" {
		profile.Status = StatusEnabled
	}
	return profile, nil
}

func normalizeProxyEndpoint(proxyType, endpoint string) (string, error) {
	value := strings.TrimSpace(endpoint)
	if value == "" {
		return "", nil
	}
	if !strings.Contains(value, "://") {
		switch proxyType {
		case ProxyTypeHTTP:
			value = "http://" + value
		case ProxyTypeSOCKS5:
			value = "socks5://" + value
		}
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid proxy profile endpoint %q: %w", endpoint, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid proxy profile endpoint %q", endpoint)
	}
	switch proxyType {
	case ProxyTypeHTTP:
		if parsed.Scheme != "http" {
			return "", fmt.Errorf("http proxy profile endpoint must use http:// for %s", endpoint)
		}
	case ProxyTypeSOCKS5:
		if parsed.Scheme != "socks5" {
			return "", fmt.Errorf("socks5 proxy profile endpoint must use socks5:// for %s", endpoint)
		}
	}
	return parsed.String(), nil
}

type PricingRule struct {
	ID                              string
	ModelAlias                      string
	ProviderID                      string
	Currency                        string
	InputPricePerMillionTokens      int64
	OutputPricePerMillionTokens     int64
	CacheReadPricePerMillionTokens  int64
	CacheWritePricePerMillionTokens int64
	EffectiveFrom                   time.Time
	EffectiveTo                     *time.Time
}

type ConsoleChatSession struct {
	ID                   string
	UserID               string
	Title                string
	CustomTitle          bool
	PublicName           string
	SystemPrompt         string
	Draft                string
	DraftAttachmentsJSON string
	Status               string
	MessagesJSON         string
	MessageCount         int
	LastRequestID        string
	LastResponseID       string
	LastError            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LastMessageAt        *time.Time
	CompletedAt          *time.Time
}

type WorkerSSHKey struct {
	ID                   string
	Name                 string
	Username             string
	PublicKey            string
	PrivateKey           string
	PrivateKeyPassphrase string
	Fingerprint          string
	Comment              string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type WorkerDataDisk struct {
	WorkerID   string
	DevicePath string
	MountPath  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type WorkerServer struct {
	ID                    string
	Name                  string
	ExpectedUbuntuVersion string
	SSHHost               string
	SSHPort               int
	SSHUsername           string
	SSHKeyID              string
	InstallProxyID        string
	Labels                []string
	DataRoot              string
	Status                string
	LastProbeStatus       string
	LastProbeError        string
	LastProbeSummaryJSON  string
	LastProbedAt          *time.Time
	LastInitJobID         string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type WorkerInitJob struct {
	ID          string
	WorkerID    string
	Action      string
	Status      string
	TriggeredBy string
	LogSummary  string
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

type WorkerInitJobEvent struct {
	WorkerID  string
	JobID     string
	Sequence  int64
	Level     string
	Message   string
	CreatedAt time.Time
}

type CloudAgentAccount struct {
	ID              string
	UserID          string
	WorkerID        string
	AgentType       string
	ModelPublicName string
	ContainerID     string
	ContainerName   string
	WorkspacePath   string
	Credential      string
	Status          string
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastStartedAt   *time.Time
	LastSeenAt      *time.Time
}

type CloudAgentSession struct {
	ID            string
	UserID        string
	WorkerID      string
	AccountID     string
	AgentType     string
	ChatSessionID string
	WorkspacePath string
	Status        string
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ClosedAt      *time.Time
}

func NormalizeWorkerLabels(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	labels := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		labels = append(labels, normalized)
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func NormalizeWorkerDataDisk(disk WorkerDataDisk) (WorkerDataDisk, error) {
	disk.WorkerID = strings.TrimSpace(disk.WorkerID)
	disk.DevicePath = path.Clean(strings.TrimSpace(disk.DevicePath))
	disk.MountPath = path.Clean(strings.TrimSpace(disk.MountPath))
	if disk.DevicePath == "." || disk.DevicePath == "" || !strings.HasPrefix(disk.DevicePath, "/dev/") {
		return WorkerDataDisk{}, fmt.Errorf("worker data disk device path must start with /dev/")
	}
	if disk.MountPath == "." || disk.MountPath == "" || !strings.HasPrefix(disk.MountPath, "/") || disk.MountPath == "/" {
		return WorkerDataDisk{}, fmt.Errorf("worker data disk mount path must be an absolute non-root path")
	}
	return disk, nil
}

func NormalizeWorkerDisks(disks []WorkerDataDisk) ([]WorkerDataDisk, error) {
	if len(disks) == 0 {
		return nil, nil
	}
	result := make([]WorkerDataDisk, 0, len(disks))
	seen := make(map[string]struct{}, len(disks))
	for _, disk := range disks {
		normalized, err := NormalizeWorkerDataDisk(disk)
		if err != nil {
			return nil, err
		}
		key := normalized.DevicePath + "|" + normalized.MountPath
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func NormalizeWorkerServer(server WorkerServer) (WorkerServer, error) {
	server.ID = strings.TrimSpace(server.ID)
	server.Name = strings.TrimSpace(server.Name)
	server.ExpectedUbuntuVersion = strings.TrimSpace(server.ExpectedUbuntuVersion)
	server.SSHHost = strings.TrimSpace(server.SSHHost)
	server.SSHUsername = strings.TrimSpace(server.SSHUsername)
	server.SSHKeyID = strings.TrimSpace(server.SSHKeyID)
	server.InstallProxyID = strings.TrimSpace(server.InstallProxyID)
	server.Labels = NormalizeWorkerLabels(server.Labels)
	server.DataRoot = path.Clean(strings.TrimSpace(server.DataRoot))
	server.Status = strings.TrimSpace(server.Status)
	server.LastProbeStatus = strings.TrimSpace(server.LastProbeStatus)
	server.LastProbeError = strings.TrimSpace(server.LastProbeError)
	server.LastProbeSummaryJSON = strings.TrimSpace(server.LastProbeSummaryJSON)
	server.LastInitJobID = strings.TrimSpace(server.LastInitJobID)

	if server.ID == "" {
		return WorkerServer{}, fmt.Errorf("worker id is required")
	}
	if server.Name == "" {
		server.Name = server.ID
	}
	if server.ExpectedUbuntuVersion == "" {
		server.ExpectedUbuntuVersion = DefaultWorkerExpectedUbuntuVersion
	}
	if server.SSHHost == "" {
		return WorkerServer{}, fmt.Errorf("worker ssh host is required")
	}
	if server.SSHPort <= 0 {
		server.SSHPort = DefaultWorkerSSHPort
	}
	if server.SSHUsername == "" {
		return WorkerServer{}, fmt.Errorf("worker ssh username is required")
	}
	if server.SSHKeyID == "" {
		return WorkerServer{}, fmt.Errorf("worker ssh key id is required")
	}
	if server.InstallProxyID == "" {
		server.InstallProxyID = ProxyTypeDirect
	}
	if server.DataRoot == "" || server.DataRoot == "." {
		server.DataRoot = DefaultWorkerDataRoot
	}
	if !strings.HasPrefix(server.DataRoot, "/") {
		return WorkerServer{}, fmt.Errorf("worker data root must be an absolute path")
	}
	if server.Status == "" {
		server.Status = WorkerStatusPending
	}
	if server.LastProbeStatus == "" {
		server.LastProbeStatus = WorkerProbeStatusUnknown
	}
	return server, nil
}

func NormalizeWorkerSSHKey(key WorkerSSHKey) (WorkerSSHKey, error) {
	key.ID = strings.TrimSpace(key.ID)
	key.Name = strings.TrimSpace(key.Name)
	key.Username = strings.TrimSpace(key.Username)
	key.PublicKey = strings.TrimSpace(key.PublicKey)
	key.Fingerprint = strings.TrimSpace(key.Fingerprint)
	key.Comment = strings.TrimSpace(key.Comment)

	if key.ID == "" {
		return WorkerSSHKey{}, fmt.Errorf("worker ssh key id is required")
	}
	if key.Name == "" {
		key.Name = key.ID
	}
	if key.PublicKey == "" && key.PrivateKey == "" {
		return WorkerSSHKey{}, fmt.Errorf("worker ssh key requires a public key or private key")
	}
	publicKey, fingerprint, err := normalizeWorkerSSHPublicKey(key.PublicKey, key.PrivateKey, key.PrivateKeyPassphrase)
	if err != nil {
		return WorkerSSHKey{}, err
	}
	key.PublicKey = publicKey
	key.Fingerprint = fingerprint
	return key, nil
}

func normalizeWorkerSSHPublicKey(publicKey, privateKey, passphrase string) (string, string, error) {
	if trimmed := strings.TrimSpace(publicKey); trimmed != "" {
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
		if err == nil {
			clean := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))
			return clean, ssh.FingerprintSHA256(parsed), nil
		}
	}
	if strings.TrimSpace(privateKey) == "" {
		return "", "", fmt.Errorf("worker ssh public key is invalid")
	}
	raw, err := parseWorkerSSHPrivateKey([]byte(privateKey), []byte(passphrase))
	if err != nil {
		return "", "", fmt.Errorf("worker ssh private key is invalid: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return "", "", fmt.Errorf("worker ssh private key signer is invalid: %w", err)
	}
	parsed := signer.PublicKey()
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed))), ssh.FingerprintSHA256(parsed), nil
}

func parseWorkerSSHPrivateKey(privateKey []byte, passphrase []byte) (any, error) {
	if len(passphrase) > 0 {
		return ssh.ParseRawPrivateKeyWithPassphrase(privateKey, passphrase)
	}
	return ssh.ParseRawPrivateKey(privateKey)
}

func NormalizeWorkerInitJob(job WorkerInitJob) (WorkerInitJob, error) {
	job.ID = strings.TrimSpace(job.ID)
	job.WorkerID = strings.TrimSpace(job.WorkerID)
	job.Action = strings.TrimSpace(job.Action)
	job.Status = strings.TrimSpace(job.Status)
	job.TriggeredBy = strings.TrimSpace(job.TriggeredBy)
	job.LogSummary = strings.TrimSpace(job.LogSummary)
	job.LastError = strings.TrimSpace(job.LastError)
	if job.ID == "" {
		return WorkerInitJob{}, fmt.Errorf("worker init job id is required")
	}
	if job.WorkerID == "" {
		return WorkerInitJob{}, fmt.Errorf("worker init job worker id is required")
	}
	if job.Action == "" {
		job.Action = WorkerInitActionBootstrap
	}
	if job.Status == "" {
		job.Status = WorkerJobStatusQueued
	}
	return job, nil
}

func NormalizeCloudAgentAccount(account CloudAgentAccount) (CloudAgentAccount, error) {
	account.ID = strings.TrimSpace(account.ID)
	account.UserID = strings.TrimSpace(account.UserID)
	account.WorkerID = strings.TrimSpace(account.WorkerID)
	account.AgentType = strings.TrimSpace(account.AgentType)
	account.ModelPublicName = strings.TrimSpace(account.ModelPublicName)
	account.ContainerID = strings.TrimSpace(account.ContainerID)
	account.ContainerName = strings.TrimSpace(account.ContainerName)
	account.WorkspacePath = path.Clean(strings.TrimSpace(account.WorkspacePath))
	account.Credential = strings.TrimSpace(account.Credential)
	account.Status = strings.TrimSpace(account.Status)
	account.LastError = strings.TrimSpace(account.LastError)
	if account.ID == "" {
		return CloudAgentAccount{}, fmt.Errorf("cloud agent account id is required")
	}
	if account.UserID == "" {
		return CloudAgentAccount{}, fmt.Errorf("cloud agent account user id is required")
	}
	if account.WorkerID == "" {
		return CloudAgentAccount{}, fmt.Errorf("cloud agent account worker id is required")
	}
	if account.AgentType == "" {
		account.AgentType = CloudAgentTypeClaudeCode
	}
	if account.WorkspacePath == "" || account.WorkspacePath == "." {
		account.WorkspacePath = DefaultCloudAgentWorkspacePath
	}
	if !strings.HasPrefix(account.WorkspacePath, "/") {
		return CloudAgentAccount{}, fmt.Errorf("cloud agent workspace path must be absolute")
	}
	if account.Status == "" {
		account.Status = CloudAgentStatusStopped
	}
	return account, nil
}

func NormalizeCloudAgentSession(session CloudAgentSession) (CloudAgentSession, error) {
	session.ID = strings.TrimSpace(session.ID)
	session.UserID = strings.TrimSpace(session.UserID)
	session.WorkerID = strings.TrimSpace(session.WorkerID)
	session.AccountID = strings.TrimSpace(session.AccountID)
	session.AgentType = strings.TrimSpace(session.AgentType)
	session.ChatSessionID = strings.TrimSpace(session.ChatSessionID)
	session.WorkspacePath = path.Clean(strings.TrimSpace(session.WorkspacePath))
	session.Status = strings.TrimSpace(session.Status)
	session.LastError = strings.TrimSpace(session.LastError)
	if session.ID == "" {
		return CloudAgentSession{}, fmt.Errorf("cloud agent session id is required")
	}
	if session.UserID == "" {
		return CloudAgentSession{}, fmt.Errorf("cloud agent session user id is required")
	}
	if session.WorkerID == "" {
		return CloudAgentSession{}, fmt.Errorf("cloud agent session worker id is required")
	}
	if session.AccountID == "" {
		return CloudAgentSession{}, fmt.Errorf("cloud agent session account id is required")
	}
	if session.AgentType == "" {
		session.AgentType = CloudAgentTypeClaudeCode
	}
	if session.WorkspacePath == "" || session.WorkspacePath == "." {
		session.WorkspacePath = DefaultCloudAgentWorkspacePath
	}
	if !strings.HasPrefix(session.WorkspacePath, "/") {
		return CloudAgentSession{}, fmt.Errorf("cloud agent session workspace path must be absolute")
	}
	if session.Status == "" {
		session.Status = CloudAgentSessionStatusPending
	}
	return session, nil
}

type UsageRecord struct {
	RequestID           string
	UserID              string
	APIKeyID            string
	ProviderID          string
	ModelAlias          string
	UpstreamModel       string
	Protocol            string
	Endpoint            string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	TotalTokens         int
	CostMicroCents      int64
	Currency            string
	Estimated           bool
	Stream              bool
	StatusCode          int
	LatencyMS           int64
	CreatedAt           time.Time
}

type UsageTotals struct {
	Requests       int64 `json:"requests"`
	TotalTokens    int64 `json:"total_tokens"`
	CostMicroCents int64 `json:"cost_micro_cents"`
}

type APIKeyUsageSummary struct {
	AllTime UsageTotals `json:"all_time"`
	Daily   UsageTotals `json:"daily"`
	Weekly  UsageTotals `json:"weekly"`
	Monthly UsageTotals `json:"monthly"`
}

type DashboardData struct {
	RequestCount   int64
	ErrorCount     int64
	EstimatedCount int64
	InputTokens    int64
	OutputTokens   int64
	CostMicroCents int64
	RecentUsage    []UsageRecord
	ModelCosts     []ModelCost
}

type ModelCost struct {
	ModelAlias     string
	RequestCount   int64
	InputTokens    int64
	OutputTokens   int64
	CostMicroCents int64
}

type SpendBreakdown struct {
	Key            string
	Label          string
	RequestCount   int64
	TotalTokens    int64
	CostMicroCents int64
	LastSeen       *time.Time
}

type BillingOverview struct {
	RequestCount   int64
	EstimatedCount int64
	InputTokens    int64
	OutputTokens   int64
	CostMicroCents int64
	RecentUsage    []UsageRecord
	ModelCosts     []ModelCost
	ProviderCosts  []SpendBreakdown
	APIKeyCosts    []SpendBreakdown
	UserCosts      []SpendBreakdown
}

type ConsoleUserSummary struct {
	UserID           string
	APIKeyCount      int64
	RequestCount     int64
	CostMicroCents   int64
	LastSeen         *time.Time
	LastAPIKeyPrefix string
}

type UserDirectory struct {
	Settings            ConsoleAuthSettings
	Summaries           []ConsoleUserSummary
	ObservedUsers       int64
	ActiveAPIKeys       int64
	ReadyOAuthProviders int64
}

type ConsoleAuthSettings struct {
	LocalPasswordEnabled bool
	AllowedEmails        []string
	AllowedDomains       []string
	Providers            []OAuthProviderSettings
	UpdatedAt            time.Time
}

type OAuthProviderSettings struct {
	ID                  string
	Name                string
	Kind                string
	Enabled             bool
	ClientID            string
	ClientSecret        string
	Scopes              []string
	AuthURL             string
	TokenURL            string
	TokenStyle          string
	TokenResponsePath   string
	AuthStyle           string
	UserInfoURL         string
	UserInfoMethod      string
	UserInfoTokenStyle  string
	UserInfoSubjectPath string
	UserInfoEmailPath   string
	UserInfoNamePath    string
	UserInfoLoginPath   string
	ExtraEmailURL       string
	IssuerURL           string
	AuthParams          []KeyValue
	TokenParams         []KeyValue
	UserInfoParams      []KeyValue
}

type KeyValue struct {
	Key   string
	Value string
}
