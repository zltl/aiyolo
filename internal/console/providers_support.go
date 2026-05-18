package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

type providerFormView struct {
	ID                 string
	Name               string
	BaseURL            string
	Protocol           string
	SupportedProtocols []string
	DefaultProxyID     string
	TimeoutSeconds     int
	Priority           int
	Weight             int
	Status             string
	Editing            bool
	HasSavedMasterKey  bool
}

func buildProvidersViewData(ctx context.Context, store storage.Store, data map[string]any, r *http.Request) error {
	form := providerFormView{
		Protocol:           domain.ProtocolOpenAI,
		SupportedProtocols: []string{domain.ProtocolOpenAI},
		TimeoutSeconds:     90,
		Priority:           1,
		Weight:             100,
		Status:             domain.StatusEnabled,
	}
	if r == nil {
		data["ProviderForm"] = form
		return nil
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("edit_provider_id"))
	if providerID == "" {
		data["ProviderForm"] = form
		return nil
	}
	provider, err := store.GetProvider(ctx, providerID)
	if err != nil {
		if err == storage.ErrNotFound {
			data["ProviderForm"] = form
			return nil
		}
		return err
	}
	data["ProviderForm"] = providerFormView{
		ID:                 provider.ID,
		Name:               provider.Name,
		BaseURL:            provider.BaseURL,
		Protocol:           firstNonEmpty(provider.Protocol, domain.ProtocolOpenAI),
		SupportedProtocols: domain.ProviderSupportedProtocols(provider),
		DefaultProxyID:     provider.DefaultProxyID,
		TimeoutSeconds:     defaultProviderInt(provider.TimeoutSeconds, 90),
		Priority:           defaultProviderInt(provider.Priority, 1),
		Weight:             defaultProviderInt(provider.Weight, 100),
		Status:             firstNonEmpty(provider.Status, domain.StatusEnabled),
		Editing:            true,
		HasSavedMasterKey:  strings.TrimSpace(provider.MasterKey) != "",
	}
	return nil
}

func normalizeProviderSupportedProtocols(primary string, selected []string) []string {
	protocols := make([]string, 0, len(selected)+1)
	seen := make(map[string]struct{}, len(selected)+1)
	appendUnique := func(value string) {
		normalized := domain.NormalizeProtocol(value)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		protocols = append(protocols, normalized)
	}
	primary = domain.NormalizeProtocol(primary)
	if primary == "" {
		primary = domain.ProtocolOpenAI
	}
	appendUnique(primary)
	for _, value := range domain.NormalizeProtocols(selected) {
		appendUnique(value)
	}
	if len(protocols) == 0 {
		return []string{domain.ProtocolOpenAI}
	}
	return protocols
}

func defaultProviderInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}