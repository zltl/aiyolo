package proxy

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/zltl/aiyolo/internal/domain"
)

type TransportFactory struct{}

func NewTransportFactory() *TransportFactory { return &TransportFactory{} }

func (factory *TransportFactory) HTTPClient(_ context.Context, provider domain.Provider, profile domain.ProxyProfile) (*http.Client, error) {
	timeout := time.Duration(provider.TimeoutSeconds) * time.Second
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	switch strings.ToLower(profile.Type) {
	case "", "direct":
		transport.Proxy = nil
	case "http", "https":
		proxyURL, err := url.Parse(profile.Endpoint)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "xray", "v2ray":
		dialer, err := socksDialer(profile)
		if err != nil {
			return nil, err
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			type contextDialer interface {
				DialContext(context.Context, string, string) (net.Conn, error)
			}
			if cd, ok := dialer.(contextDialer); ok {
				return cd.DialContext(ctx, network, address)
			}
			return dialer.Dial(network, address)
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func socksDialer(profile domain.ProxyProfile) (proxy.Dialer, error) {
	endpoint := profile.Endpoint
	if strings.HasPrefix(endpoint, "socks5://") {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		return proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
	}
	var auth *proxy.Auth
	if profile.Auth != "" && strings.Contains(profile.Auth, ":") {
		parts := strings.SplitN(profile.Auth, ":", 2)
		auth = &proxy.Auth{User: parts[0], Password: parts[1]}
	}
	return proxy.SOCKS5("tcp", endpoint, auth, proxy.Direct)
}
