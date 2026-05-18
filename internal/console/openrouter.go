package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

type openRouterTransport struct {
	base http.RoundTripper
}

func (t *openRouterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("HTTP-Referer", "https://github.com/zltl/aiyolo")
	req.Header.Set("X-Title", "aiyolo")
	return t.base.RoundTrip(req)
}

type openRouterModelListResponse struct {
	Data []openRouterModelMetadata `json:"data"`
}

type openRouterModelMetadata struct {
	ID            string                        `json:"id"`
	ContextLength int                           `json:"context_length"`
	Pricing       openRouterPricingMetadata     `json:"pricing"`
	TopProvider   openRouterTopProviderMetadata `json:"top_provider"`
}

type openRouterPricingMetadata struct {
	Prompt          string `json:"prompt"`
	Completion      string `json:"completion"`
	InputCacheRead  string `json:"input_cache_read"`
	InputCacheWrite string `json:"input_cache_write"`
}

type openRouterTopProviderMetadata struct {
	ContextLength int `json:"context_length"`
}

type openRouterImportedModel struct {
	Route       domain.ModelRoute
	PricingRule domain.PricingRule
}

type openRouterSyncSummary struct {
	Synced           int
	SkippedConflicts int
}

type compatibleProviderManualPricing struct {
	Currency       string
	InputPerMillion string
	OutputPerMillion string
	CacheReadPerMillion string
	CacheWritePerMillion string
}

var deepSeekCompatibleModelPricing = map[string]compatibleProviderManualPricing{
	"deepseek-v4-flash": {
		Currency:            "CNY",
		InputPerMillion:     "1",
		OutputPerMillion:    "2",
		CacheReadPerMillion: "0.02",
		CacheWritePerMillion: "1",
	},
	"deepseek-v4-pro": {
		Currency:            "CNY",
		InputPerMillion:     "3",
		OutputPerMillion:    "6",
		CacheReadPerMillion: "0.025",
		CacheWritePerMillion: "3",
	},
	"deepseek-chat": {
		Currency:            "CNY",
		InputPerMillion:     "1",
		OutputPerMillion:    "2",
		CacheReadPerMillion: "0.02",
		CacheWritePerMillion: "1",
	},
	"deepseek-reasoner": {
		Currency:            "CNY",
		InputPerMillion:     "1",
		OutputPerMillion:    "2",
		CacheReadPerMillion: "0.02",
		CacheWritePerMillion: "1",
	},
}

func (metadata openRouterModelMetadata) hasPricing() bool {
	return strings.TrimSpace(metadata.Pricing.Prompt) != "" ||
		strings.TrimSpace(metadata.Pricing.Completion) != "" ||
		strings.TrimSpace(metadata.Pricing.InputCacheRead) != "" ||
		strings.TrimSpace(metadata.Pricing.InputCacheWrite) != ""
}

func (metadata openRouterModelMetadata) resolvedContextLength() int {
	if metadata.ContextLength > 0 {
		return metadata.ContextLength
	}
	if metadata.TopProvider.ContextLength > 0 {
		return metadata.TopProvider.ContextLength
	}
	return 0
}

func (metadata openRouterModelMetadata) pricingRule(providerID string) domain.PricingRule {
	modelID := strings.TrimSpace(metadata.ID)
	ruleID := openRouterPricingRuleID(providerID, modelID)
	return domain.PricingRule{
		ID:                              ruleID,
		ModelAlias:                      modelID,
		ProviderID:                      providerID,
		Currency:                        "USD",
		InputPricePerMillionTokens:      openRouterPriceToMicroCentsPerMillion(metadata.Pricing.Prompt),
		OutputPricePerMillionTokens:     openRouterPriceToMicroCentsPerMillion(metadata.Pricing.Completion),
		CacheReadPricePerMillionTokens:  openRouterPriceToMicroCentsPerMillion(metadata.Pricing.InputCacheRead),
		CacheWritePricePerMillionTokens: openRouterPriceToMicroCentsPerMillion(metadata.Pricing.InputCacheWrite),
	}
}

func compatibleProviderPricingRule(provider domain.Provider, providerID string, metadata openRouterModelMetadata) (domain.PricingRule, bool) {
	if isDeepSeekProvider(provider) {
		if rule, ok := deepSeekPricingRule(providerID, metadata.ID); ok {
			return rule, true
		}
	}
	if metadata.hasPricing() {
		return metadata.pricingRule(providerID), true
	}
	return domain.PricingRule{}, false
}

func deepSeekPricingRule(providerID, modelID string) (domain.PricingRule, bool) {
	trimmedModelID := strings.TrimSpace(modelID)
	pricing, ok := deepSeekCompatibleModelPricing[trimmedModelID]
	if !ok {
		return domain.PricingRule{}, false
	}
	ruleID := openRouterPricingRuleID(providerID, trimmedModelID)
	return domain.PricingRule{
		ID:                             ruleID,
		ModelAlias:                     trimmedModelID,
		ProviderID:                     providerID,
		Currency:                       pricing.Currency,
		InputPricePerMillionTokens:     pricePerMillionToMicroCents(pricing.InputPerMillion),
		OutputPricePerMillionTokens:    pricePerMillionToMicroCents(pricing.OutputPerMillion),
		CacheReadPricePerMillionTokens: pricePerMillionToMicroCents(pricing.CacheReadPerMillion),
		CacheWritePricePerMillionTokens: pricePerMillionToMicroCents(pricing.CacheWritePerMillion),
	}, true
}

func compatibleModelsBaseURL(provider domain.Provider) string {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if trimmedBaseURL != "" {
		return trimmedBaseURL
	}
	if isOpenRouterProvider(provider) {
		return "https://openrouter.ai/api/v1"
	}
	if isDeepSeekProvider(provider) {
		return "https://api.deepseek.com"
	}
	return ""
}

func fetchCompatibleModels(ctx context.Context, provider domain.Provider) ([]openRouterImportedModel, error) {
	trimmedBaseURL := compatibleModelsBaseURL(provider)
	if trimmedBaseURL == "" {
		return nil, fmt.Errorf("provider %s is missing base URL", provider.ID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmedBaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	if apiKey := strings.TrimSpace(provider.MasterKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	transport := http.DefaultTransport
	if isOpenRouterProvider(provider) {
		transport = &openRouterTransport{base: transport}
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch models: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload openRouterModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	imports := make([]openRouterImportedModel, 0, len(payload.Data))
	providerID := firstNonEmpty(strings.TrimSpace(provider.ID), "openrouter")
	primaryProtocol := domain.ProviderPrimaryProtocol(provider)
	allowedProtocols := domain.ProviderSupportedProtocols(provider)
	for _, metadata := range payload.Data {
		modelID := strings.TrimSpace(metadata.ID)
		if modelID == "" {
			continue
		}
		imported := openRouterImportedModel{
			Route: domain.ModelRoute{
				PublicName:       modelID,
				ProviderID:       providerID,
				UpstreamModel:    modelID,
				Protocol:         primaryProtocol,
				AllowedProtocols: allowedProtocols,
				Enabled:          true,
				Priority:         1,
				Weight:           100,
				ContextTokens:    metadata.resolvedContextLength(),
			},
		}
		if rule, ok := compatibleProviderPricingRule(provider, providerID, metadata); ok {
			imported.Route.PriceRuleID = rule.ID
			imported.PricingRule = rule
		}
		imports = append(imports, imported)
	}

	return imports, nil
}

func fetchOpenRouterModels(ctx context.Context, provider domain.Provider) ([]openRouterImportedModel, error) {
	return fetchCompatibleModels(ctx, provider)
}

func syncCompatibleModelRoutes(ctx context.Context, store storage.Store, provider domain.Provider) (openRouterSyncSummary, error) {
	if domain.ProviderPrimaryProtocol(provider) != domain.ProtocolOpenAI {
		return openRouterSyncSummary{}, fmt.Errorf("provider %s is not OpenAI-compatible", provider.ID)
	}
	if strings.TrimSpace(provider.MasterKey) == "" {
		return openRouterSyncSummary{}, fmt.Errorf("provider %s is missing master key", provider.ID)
	}

	imports, err := fetchCompatibleModels(ctx, provider)
	if err != nil {
		return openRouterSyncSummary{}, err
	}

	summary := openRouterSyncSummary{}
	for _, imported := range imports {
		route := imported.Route
		existing, err := store.LookupModelRoute(ctx, route.PublicName)
		switch {
		case err == nil:
			if existing.ProviderID != "" && existing.ProviderID != route.ProviderID {
				summary.SkippedConflicts++
				continue
			}
			route = mergeImportedRoute(existing, route)
		case errors.Is(err, storage.ErrNotFound):
			// Create a new route below.
		case err != nil:
			return summary, fmt.Errorf("lookup model route %s: %w", route.PublicName, err)
		}

		if strings.TrimSpace(imported.PricingRule.ID) != "" {
			if err := store.UpsertPricingRule(ctx, imported.PricingRule); err != nil {
				return summary, fmt.Errorf("upsert pricing rule %s: %w", imported.PricingRule.ID, err)
			}
		}
		if err := store.UpsertModelRoute(ctx, route); err != nil {
			return summary, fmt.Errorf("upsert model route %s: %w", route.PublicName, err)
		}
		summary.Synced++
	}

	return summary, nil
}

func syncOpenRouterModelRoutes(ctx context.Context, store storage.Store, provider domain.Provider) (openRouterSyncSummary, error) {
	return syncCompatibleModelRoutes(ctx, store, provider)
}

func mergeImportedRoute(existing, imported domain.ModelRoute) domain.ModelRoute {
	imported.ProxyProfileID = existing.ProxyProfileID
	imported.Enabled = existing.Enabled
	imported.Priority = existing.Priority
	imported.Weight = existing.Weight
	if imported.PriceRuleID == "" {
		imported.PriceRuleID = existing.PriceRuleID
	}
	imported.CreatedAt = existing.CreatedAt
	return imported
}

func openRouterPricingRuleID(providerID, modelID string) string {
	return firstNonEmpty(strings.TrimSpace(providerID), "openrouter") + ":" + strings.TrimSpace(modelID)
}

func pricePerMillionToMicroCents(value string) int64 {
	return decimalStringToScaledInt64(value, 100000000)
}

func openRouterPriceToMicroCentsPerMillion(value string) int64 {
	return decimalStringToScaledInt64(value, 100000000000000)
}

func decimalStringToScaledInt64(value string, scale int64) int64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	ratio, ok := new(big.Rat).SetString(trimmed)
	if !ok {
		return 0
	}
	scaled := new(big.Rat).Mul(ratio, big.NewRat(scale, 1))
	quotient, remainder := new(big.Int).QuoRem(new(big.Int).Set(scaled.Num()), scaled.Denom(), new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0
	}
	return quotient.Int64()
}
