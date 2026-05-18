package console

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/zltl/aiyolo/internal/domain"
	proxytransport "github.com/zltl/aiyolo/internal/proxy"
	"github.com/zltl/aiyolo/internal/storage"
)

type modelRouteFormView struct {
	PublicName     string
	ProviderID     string
	UpstreamModel  string
	Protocol       string
	ProxyProfileID string
	ContextTokens  int
	Priority       int
	Weight         int
}

type modelTestFormView struct {
	PublicName string
	Prompt     string
}

type modelTestResultView struct {
	PublicName    string
	ProviderID    string
	ProviderName  string
	UpstreamModel string
	Output        string
	ResponseID    string
	FinishReason  string
	DurationMS    int64
	TotalTokens   int
}

type modelTestExecution struct {
	Result     modelTestResultView
	Usage      domain.UsageRecord
	StatusCode int
}

const consoleModelTestEndpoint = "/console/models/test"

func buildModelsViewData(data map[string]any, r *http.Request) {
	routes, _ := data["Routes"].([]domain.ModelRoute)
	providers, _ := data["Providers"].([]domain.Provider)

	form := modelRouteFormView{
		PublicName:     strings.TrimSpace(r.FormValue("public_name")),
		ProviderID:     strings.TrimSpace(r.FormValue("provider_id")),
		UpstreamModel:  strings.TrimSpace(r.FormValue("upstream_model")),
		Protocol:       strings.TrimSpace(r.FormValue("protocol")),
		ProxyProfileID: strings.TrimSpace(r.FormValue("proxy_profile_id")),
		ContextTokens:  formInt(r, "context_tokens", 0),
		Priority:       formInt(r, "priority", 1),
		Weight:         formInt(r, "weight", 100),
	}

	selectedProvider, ok := findProviderByID(providers, form.ProviderID)
	if ok {
		form.Protocol = firstNonEmpty(selectedProvider.Protocol, form.Protocol, domain.ProtocolOpenAI)
	} else {
		form.Protocol = firstNonEmpty(form.Protocol, domain.ProtocolOpenAI)
		selectedProvider = domain.Provider{}
	}

	testForm := modelTestFormView{
		PublicName: firstNonEmpty(strings.TrimSpace(r.FormValue("test_public_name")), form.PublicName),
		Prompt:     strings.TrimSpace(r.FormValue("test_prompt")),
	}
	if testForm.Prompt == "" {
		testForm.Prompt = defaultModelTestPrompt(resolveConsoleLocale(r))
	}

	aliasOptions := routeAliases(routes)
	providerModels := []string{}
	if form.ProviderID != "" {
		aliasOptions = routeAliasesForProvider(routes, form.ProviderID)
		providerModels = upstreamModelsForProvider(routes, form.ProviderID)
	}

	data["ModelForm"] = form
	data["RouteAliasOptions"] = aliasOptions
	data["ProviderUpstreamModels"] = providerModels
	data["SelectedProvider"] = selectedProvider
	data["TestForm"] = testForm
	data["SupportsModelTest"] = selectedProvider.Protocol == "" || selectedProvider.Protocol == domain.ProtocolOpenAI
}

func routeAliasesForProvider(routes []domain.ModelRoute, providerID string) []string {
	values := make([]string, 0, len(routes))
	seen := make(map[string]struct{})
	for _, route := range routes {
		if route.ProviderID != providerID {
			continue
		}
		values = appendUniqueString(values, seen, route.PublicName)
	}
	return values
}

func upstreamModelsForProvider(routes []domain.ModelRoute, providerID string) []string {
	values := make([]string, 0, len(routes))
	seen := make(map[string]struct{})
	for _, route := range routes {
		if route.ProviderID != providerID {
			continue
		}
		values = appendUniqueString(values, seen, route.UpstreamModel)
	}
	return values
}

func findProviderByID(providers []domain.Provider, providerID string) (domain.Provider, bool) {
	for _, provider := range providers {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return domain.Provider{}, false
}

func effectiveModelProxy(route domain.ModelRoute, providers []domain.Provider) string {
	if proxyID := strings.TrimSpace(route.ProxyProfileID); proxyID != "" {
		return proxyID
	}
	provider, ok := findProviderByID(providers, route.ProviderID)
	if ok {
		if proxyID := strings.TrimSpace(provider.DefaultProxyID); proxyID != "" {
			return proxyID
		}
	}
	return domain.ProxyTypeDirect
}

func modelProxySelectLabel(locale string, provider domain.Provider) string {
	if proxyID := strings.TrimSpace(provider.DefaultProxyID); proxyID != "" {
		return consoleText(locale, "继承 Provider 默认代理 · ", "Use provider default · ") + proxyID
	}
	return domain.ProxyTypeDirect
}

func defaultModelTestPrompt(locale string) string {
	return consoleText(locale, "请用一句短话回复 ok，并带上你收到的模型标识。", "Reply with ok and mention the model identifier you received.")
}

func newOpenAICompatibleHTTPClient(provider domain.Provider, baseClient *http.Client) *http.Client {
	if baseClient == nil {
		baseClient = &http.Client{}
	}
	client := *baseClient
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	if isOpenRouterProvider(provider) {
		client.Transport = &openRouterTransport{base: client.Transport}
	}
	return &client
}

func newOpenAICompatibleClient(provider domain.Provider, baseClient *http.Client) *openai.Client {
	config := openai.DefaultConfig(provider.MasterKey)
	if baseURL := strings.TrimSpace(provider.BaseURL); baseURL != "" {
		config.BaseURL = baseURL
	}
	config.HTTPClient = newOpenAICompatibleHTTPClient(provider, baseClient)
	return openai.NewClientWithConfig(config)
}

func isOpenRouterProvider(provider domain.Provider) bool {
	return domain.IsOpenRouterProvider(provider)
}

func isDeepSeekProvider(provider domain.Provider) bool {
	return domain.IsDeepSeekProvider(provider)
}

func supportsCompatibleModelImportProvider(provider domain.Provider) bool {
	if domain.ProviderPrimaryProtocol(provider) != domain.ProtocolOpenAI {
		return false
	}
	return isOpenRouterProvider(provider) || isDeepSeekProvider(provider)
}

func providerProtocolSummary(provider domain.Provider) string {
	protocols := domain.ProviderSupportedProtocols(provider)
	if len(protocols) == 0 {
		protocols = []string{domain.ProtocolOpenAI}
	}
	labels := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		labels = append(labels, protocolLabel(protocol))
	}
	return strings.Join(labels, " / ")
}

func directProxyProfile(provider domain.Provider) domain.ProxyProfile {
	return domain.ProxyProfile{
		ID:             domain.ProxyTypeDirect,
		Name:           domain.ProxyTypeDirect,
		Type:           domain.ProxyTypeDirect,
		Status:         domain.StatusEnabled,
		TimeoutSeconds: provider.TimeoutSeconds,
	}
}

func resolveModelTestProxyProfile(ctx context.Context, store storage.Store, provider domain.Provider, route domain.ModelRoute) (domain.ProxyProfile, error) {
	profileID := firstNonEmpty(strings.TrimSpace(route.ProxyProfileID), strings.TrimSpace(provider.DefaultProxyID))
	if profileID == "" || profileID == domain.ProxyTypeDirect {
		return directProxyProfile(provider), nil
	}
	profile, err := store.GetProxyProfile(ctx, profileID)
	if err != nil {
		if err == storage.ErrNotFound {
			return domain.ProxyProfile{}, fmt.Errorf("proxy profile %s was not found", profileID)
		}
		return domain.ProxyProfile{}, err
	}
	return profile, nil
}

func resolveModelTestPricingRule(ctx context.Context, store storage.Store, route domain.ModelRoute) (domain.PricingRule, error) {
	if strings.TrimSpace(route.PriceRuleID) == "" {
		return domain.PricingRule{}, nil
	}
	rule, err := store.GetPricingRule(ctx, route.PriceRuleID)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.PricingRule{}, nil
	}
	return rule, err
}

func currentConsoleSessionSubject(r *http.Request, secret string) string {
	cookie, err := r.Cookie(consoleSessionCookieName)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(cookie.Value)
	if value == "" || !verifySessionCookie(value, secret) {
		return ""
	}
	parts := strings.Split(value, ":")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(strings.Join(parts[:len(parts)-2], ":"))
}

func calculateModelTestUsageCost(rule domain.PricingRule, usage domain.UsageRecord) int64 {
	return modelTestCostForTokens(rule.InputPricePerMillionTokens, usage.InputTokens) +
		modelTestCostForTokens(rule.OutputPricePerMillionTokens, usage.OutputTokens) +
		modelTestCostForTokens(rule.CacheReadPricePerMillionTokens, usage.CacheReadTokens) +
		modelTestCostForTokens(rule.CacheWritePricePerMillionTokens, usage.CacheCreationTokens)
}

func modelTestCostForTokens(pricePerMillionTokens int64, tokens int) int64 {
	if pricePerMillionTokens <= 0 || tokens <= 0 {
		return 0
	}
	return (pricePerMillionTokens*int64(tokens) + 500000) / 1000000
}

func buildModelTestUsageRecord(requestID, userID, protocol string, route domain.ModelRoute, provider domain.Provider, pricingRule domain.PricingRule, started time.Time, execution modelTestExecution) domain.UsageRecord {
	usage := execution.Usage
	usage.RequestID = requestID
	usage.UserID = userID
	usage.ProviderID = provider.ID
	usage.ModelAlias = route.PublicName
	usage.UpstreamModel = firstNonEmpty(route.UpstreamModel, route.PublicName)
	usage.Protocol = protocol
	usage.Endpoint = consoleModelTestEndpoint
	if usage.StatusCode == 0 {
		usage.StatusCode = execution.StatusCode
	}
	if usage.StatusCode == 0 {
		usage.StatusCode = http.StatusBadGateway
	}
	if usage.LatencyMS <= 0 {
		usage.LatencyMS = time.Since(started).Milliseconds()
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	if usage.Currency == "" {
		usage.Currency = firstNonEmpty(pricingRule.Currency, "USD")
	}
	if usage.CostMicroCents == 0 && usage.StatusCode < 400 {
		usage.CostMicroCents = calculateModelTestUsageCost(pricingRule, usage)
	}
	if usage.CreatedAt.IsZero() {
		usage.CreatedAt = time.Now().UTC()
	}
	return usage
}

func modelTestErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Type != "" {
			return apiErr.Type
		}
		if apiErr.Code != nil {
			return fmt.Sprint(apiErr.Code)
		}
	}
	return "model_test_failed"
}

func persistModelTestOutcome(ctx context.Context, store storage.Store, requestID, userID, clientAddress, userAgent, protocol string, route domain.ModelRoute, provider domain.Provider, profile domain.ProxyProfile, pricingRule domain.PricingRule, started time.Time, execution modelTestExecution, cause error) {
	usage := buildModelTestUsageRecord(requestID, userID, protocol, route, provider, pricingRule, started, execution)
	if err := store.InsertUsage(ctx, usage); err != nil {
		log.Printf("insert console model test usage request_id=%s err=%v", requestID, err)
	}
	event := domain.AuditEvent{
		ID:             newID("audit"),
		RequestID:      requestID,
		UserID:         userID,
		ClientIP:       clientAddress,
		UserAgent:      userAgent,
		Protocol:       protocol,
		Endpoint:       consoleModelTestEndpoint,
		ModelAlias:     route.PublicName,
		ProviderID:     provider.ID,
		UpstreamModel:  firstNonEmpty(route.UpstreamModel, route.PublicName),
		ProxyProfileID: profile.ID,
		StatusCode:     usage.StatusCode,
		ErrorCode:      modelTestErrorCode(cause),
		LatencyMS:      usage.LatencyMS,
		InputTokens:    usage.InputTokens,
		OutputTokens:   usage.OutputTokens,
		CostMicroCents: usage.CostMicroCents,
		EventType:      "console_model_test",
		Message:        strings.TrimSpace(firstNonEmpty(errorString(cause), "Console model route test completed.")),
		CreatedAt:      usage.CreatedAt,
	}
	if err := store.InsertAudit(ctx, event); err != nil {
		log.Printf("insert console model test audit request_id=%s err=%v", requestID, err)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runModelRouteTest(ctx context.Context, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, prompt string) (modelTestExecution, error) {
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = defaultModelTestPrompt("en")
	}

	timeoutSeconds := provider.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 90
	}

	testCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	httpClient, err := proxytransport.NewTransportFactory().HTTPClient(testCtx, provider, profile)
	if err != nil {
		return modelTestExecution{StatusCode: http.StatusBadGateway, Usage: domain.UsageRecord{Currency: "USD"}}, err
	}

	client := newOpenAICompatibleClient(provider, httpClient)
	start := time.Now()
	response, err := client.CreateChatCompletion(testCtx, openai.ChatCompletionRequest{
		Model: firstNonEmpty(route.UpstreamModel, route.PublicName),
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are a connectivity test. Reply briefly."},
			{Role: openai.ChatMessageRoleUser, Content: testPrompt},
		},
		MaxTokens: 96,
	})
	if err != nil {
		statusCode := http.StatusBadGateway
		var apiErr *openai.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatusCode > 0 {
			statusCode = apiErr.HTTPStatusCode
		}
		return modelTestExecution{StatusCode: statusCode, Usage: domain.UsageRecord{Currency: "USD", StatusCode: statusCode, LatencyMS: time.Since(start).Milliseconds()}}, err
	}

	usage := domain.UsageRecord{
		InputTokens:  response.Usage.PromptTokens,
		OutputTokens: response.Usage.CompletionTokens,
		TotalTokens:  response.Usage.TotalTokens,
		Currency:     "USD",
		StatusCode:   http.StatusOK,
		LatencyMS:    time.Since(start).Milliseconds(),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	result := modelTestResultView{
		PublicName:    route.PublicName,
		ProviderID:    provider.ID,
		ProviderName:  firstNonEmpty(provider.Name, provider.ID),
		UpstreamModel: firstNonEmpty(route.UpstreamModel, route.PublicName),
		ResponseID:    response.ID,
		DurationMS:    usage.LatencyMS,
		TotalTokens:   usage.TotalTokens,
	}
	if len(response.Choices) > 0 {
		result.Output = strings.TrimSpace(response.Choices[0].Message.Content)
		result.FinishReason = string(response.Choices[0].FinishReason)
	}
	if result.Output == "" {
		result.Output = "No text returned."
	}
	return modelTestExecution{Result: result, Usage: usage, StatusCode: http.StatusOK}, nil
}
