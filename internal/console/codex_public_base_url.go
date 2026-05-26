package console

import (
	"net/http"
	"strings"
)

func (handler *Handler) codexPublicBaseURL(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(handler.cfg.CodexPublicBaseURL), "/"); configured != "" {
		return configured
	}
	if r == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := strings.ToLower(forwardedHeaderValue(r.Header.Get("x-forwarded-proto"))); forwardedProto == "http" || forwardedProto == "https" {
		scheme = forwardedProto
	}
	host := forwardedHeaderValue(r.Header.Get("x-forwarded-host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}