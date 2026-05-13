package domain

import "time"

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	StatusActive   = "active"
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
	ID              string
	Name            string
	BaseURL         string
	Protocol        string
	MasterKey       string
	DefaultProxyID  string
	Priority        int
	Weight          int
	Status          string
	TimeoutSeconds  int
	RateLimitHint   string
	LastHealthCheck *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ModelRoute struct {
	PublicName     string
	ProviderID     string
	UpstreamModel  string
	Protocol       string
	ProxyProfileID string
	PriceRuleID    string
	Enabled        bool
	Priority       int
	Weight         int
	ContextTokens  int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ProxyProfile struct {
	ID             string
	Name           string
	Type           string
	Endpoint       string
	Auth           string
	Region         string
	TimeoutSeconds int
	HealthCheckURL string
	Status         string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

type AuditEvent struct {
	ID             string
	RequestID      string
	TraceID        string
	UserID         string
	APIKeyID       string
	ClientIP       string
	UserAgent      string
	Protocol       string
	Endpoint       string
	ModelAlias     string
	ProviderID     string
	UpstreamModel  string
	ProxyProfileID string
	StatusCode     int
	ErrorCode      string
	LatencyMS      int64
	InputTokens    int
	OutputTokens   int
	CostMicroCents int64
	Stream         bool
	EventType      string
	Message        string
	CreatedAt      time.Time
}

type DashboardData struct {
	RequestCount   int64
	ErrorCount     int64
	EstimatedCount int64
	InputTokens    int64
	OutputTokens   int64
	CostMicroCents int64
	RecentAudits   []AuditEvent
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
