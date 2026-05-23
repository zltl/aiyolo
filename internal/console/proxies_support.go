package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

type proxyProfileFormView struct {
	ID                       string
	Name                     string
	Type                     string
	Endpoint                 string
	Region                   string
	TimeoutSeconds           int
	StreamIdleTimeoutSeconds int
	HealthCheckURL           string
	Status                   string
	Editing                  bool
	HasSavedAuth             bool
}

func isLockedProxyProfileID(profileID string) bool {
	return strings.EqualFold(strings.TrimSpace(profileID), domain.ProxyTypeDirect)
}

func buildProxiesViewData(ctx context.Context, store storage.Store, data map[string]any, r *http.Request) error {
	profiles, _ := data["Profiles"].([]domain.ProxyProfile)
	for index := range profiles {
		profiles[index].Endpoint = proxyEndpointForDisplay(profiles[index])
	}
	data["Profiles"] = profiles

	form := proxyProfileFormView{
		Type:                     domain.ProxyTypeDirect,
		TimeoutSeconds:           domain.DefaultProxyTimeoutSeconds,
		StreamIdleTimeoutSeconds: domain.DefaultProxyStreamIdleTimeoutSeconds,
		Status:                   domain.StatusEnabled,
	}
	if r == nil {
		data["ProxyForm"] = form
		return nil
	}
	profileID := strings.TrimSpace(r.URL.Query().Get("edit_proxy_id"))
	if profileID == "" || isLockedProxyProfileID(profileID) {
		data["ProxyForm"] = form
		return nil
	}
	profile, err := store.GetProxyProfile(ctx, profileID)
	if err != nil {
		if err == storage.ErrNotFound {
			data["ProxyForm"] = form
			return nil
		}
		return err
	}
	data["ProxyForm"] = proxyProfileFormView{
		ID:                       profile.ID,
		Name:                     profile.Name,
		Type:                     firstNonEmpty(profile.Type, domain.ProxyTypeDirect),
		Endpoint:                 proxyEndpointForDisplay(profile),
		Region:                   profile.Region,
		TimeoutSeconds:           domain.EffectiveProxyProfileTimeoutSeconds(profile),
		StreamIdleTimeoutSeconds: domain.EffectiveProxyProfileStreamIdleTimeoutSeconds(profile),
		HealthCheckURL:           profile.HealthCheckURL,
		Status:                   firstNonEmpty(profile.Status, domain.StatusEnabled),
		Editing:                  true,
		HasSavedAuth:             strings.TrimSpace(profile.Auth) != "",
	}
	return nil
}

func proxyEndpointForDisplay(profile domain.ProxyProfile) string {
	normalized, err := domain.NormalizeProxyProfile(domain.ProxyProfile{
		ID:                       firstNonEmpty(profile.ID, "proxy"),
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
		return profile.Endpoint
	}
	return normalized.Endpoint
}
