package domain

import "testing"

func TestNormalizeProxyProfileCanonicalizesEndpoints(t *testing.T) {
	testCases := []struct {
		name     string
		profile  ProxyProfile
		expected string
	}{
		{
			name:     "http adds scheme",
			profile:  ProxyProfile{ID: "http-proxy", Type: ProxyTypeHTTP, Endpoint: "127.0.0.1:10809"},
			expected: "http://127.0.0.1:10809",
		},
		{
			name:     "socks5 adds scheme",
			profile:  ProxyProfile{ID: "socks-proxy", Type: ProxyTypeSOCKS5, Endpoint: "127.0.0.1:10808"},
			expected: "socks5://127.0.0.1:10808",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			normalized, err := NormalizeProxyProfile(testCase.profile)
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Endpoint != testCase.expected {
				t.Fatalf("Endpoint=%q, want %q", normalized.Endpoint, testCase.expected)
			}
		})
	}
}