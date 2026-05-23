package proxy

import (
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestResolveHTTPTimeoutUsesRegularRequestTimeout(t *testing.T) {
	provider := domain.Provider{TimeoutSeconds: 30, StreamIdleTimeoutSeconds: 180}
	profile := domain.ProxyProfile{TimeoutSeconds: 20, StreamIdleTimeoutSeconds: 240}

	if got := resolveHTTPTimeout(provider, profile); got != 20*time.Second {
		t.Fatalf("resolveHTTPTimeout() = %s, want %s", got, 20*time.Second)
	}
}

func TestResolveStreamIdleTimeoutUsesDedicatedSetting(t *testing.T) {
	provider := domain.Provider{TimeoutSeconds: 30, StreamIdleTimeoutSeconds: 180}
	profile := domain.ProxyProfile{TimeoutSeconds: 20, StreamIdleTimeoutSeconds: 240}

	if got := resolveStreamIdleTimeout(provider, profile); got != 240*time.Second {
		t.Fatalf("resolveStreamIdleTimeout() = %s, want %s", got, 240*time.Second)
	}
}

func TestResolveStreamIdleTimeoutFallsBackToGenerousDefault(t *testing.T) {
	provider := domain.Provider{TimeoutSeconds: 30}
	if got := resolveStreamIdleTimeout(provider, domain.ProxyProfile{}); got != 300*time.Second {
		t.Fatalf("resolveStreamIdleTimeout() default = %s, want %s", got, 300*time.Second)
	}

	provider.TimeoutSeconds = 600
	if got := resolveStreamIdleTimeout(provider, domain.ProxyProfile{}); got != 600*time.Second {
		t.Fatalf("resolveStreamIdleTimeout() inherited = %s, want %s", got, 600*time.Second)
	}

	provider.StreamIdleTimeoutSeconds = 180
	profile := domain.ProxyProfile{TimeoutSeconds: 240}
	if got := resolveStreamIdleTimeout(provider, profile); got != 240*time.Second {
		t.Fatalf("resolveStreamIdleTimeout() profile fallback = %s, want %s", got, 240*time.Second)
	}
}
