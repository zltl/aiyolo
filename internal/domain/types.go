package domain

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
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
