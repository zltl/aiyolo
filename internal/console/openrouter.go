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

func fetchOpenRouterModels(ctx context.Context, provider domain.Provider) ([]openRouterImportedModel, error) {
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if trimmedBaseURL == "" {
		trimmedBaseURL = "https://openrouter.ai/api/v1"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trimmedBaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter build request: %w", err)
	}
	if apiKey := strings.TrimSpace(provider.MasterKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Transport: &openRouterTransport{base: http.DefaultTransport}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openrouter fetch models: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload openRouterModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("openrouter decode models: %w", err)
	}

	imports := make([]openRouterImportedModel, 0, len(payload.Data))
	providerID := firstNonEmpty(strings.TrimSpace(provider.ID), "openrouter")
	for _, metadata := range payload.Data {
		modelID := strings.TrimSpace(metadata.ID)
		if modelID == "" {
			continue
		}
		rule := metadata.pricingRule(providerID)
		imports = append(imports, openRouterImportedModel{
			Route: domain.ModelRoute{
				PublicName:    modelID,
				ProviderID:    providerID,
				UpstreamModel: modelID,
				Protocol:      domain.ProtocolOpenAI,
				PriceRuleID:   rule.ID,
				Enabled:       true,
				Priority:      1,
				Weight:        100,
				ContextTokens: metadata.resolvedContextLength(),
			},
			PricingRule: rule,
		})
	}

	return imports, nil
}

func syncOpenRouterModelRoutes(ctx context.Context, store storage.Store, provider domain.Provider) (openRouterSyncSummary, error) {
	if !isOpenRouterProvider(provider) {
		return openRouterSyncSummary{}, fmt.Errorf("provider %s is not OpenRouter-compatible", provider.ID)
	}
	if strings.TrimSpace(provider.MasterKey) == "" {
		return openRouterSyncSummary{}, fmt.Errorf("provider %s is missing master key", provider.ID)
	}

	imports, err := fetchOpenRouterModels(ctx, provider)
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
			route = mergeOpenRouterRoute(existing, route)
		case errors.Is(err, storage.ErrNotFound):
			// Create a new route below.
		case err != nil:
			return summary, fmt.Errorf("lookup model route %s: %w", route.PublicName, err)
		}

		if err := store.UpsertPricingRule(ctx, imported.PricingRule); err != nil {
			return summary, fmt.Errorf("upsert pricing rule %s: %w", imported.PricingRule.ID, err)
		}
		if err := store.UpsertModelRoute(ctx, route); err != nil {
			return summary, fmt.Errorf("upsert model route %s: %w", route.PublicName, err)
		}
		summary.Synced++
	}

	return summary, nil
}

func mergeOpenRouterRoute(existing, imported domain.ModelRoute) domain.ModelRoute {
	imported.ProxyProfileID = existing.ProxyProfileID
	imported.Enabled = existing.Enabled
	imported.Priority = existing.Priority
	imported.Weight = existing.Weight
	imported.CreatedAt = existing.CreatedAt
	return imported
}

func openRouterPricingRuleID(providerID, modelID string) string {
	return firstNonEmpty(strings.TrimSpace(providerID), "openrouter") + ":" + strings.TrimSpace(modelID)
}

func openRouterPriceToMicroCentsPerMillion(value string) int64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	ratio, ok := new(big.Rat).SetString(trimmed)
	if !ok {
		return 0
	}
	scaled := new(big.Rat).Mul(ratio, big.NewRat(100000000000000, 1))
	quotient, remainder := new(big.Int).QuoRem(new(big.Int).Set(scaled.Num()), scaled.Denom(), new(big.Int))
	if new(big.Int).Lsh(remainder, 1).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0
	}
	return quotient.Int64()
}
