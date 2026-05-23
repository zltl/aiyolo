package proxy

import (
	"context"
	"fmt"
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

func (factory *TransportFactory) HTTPClient(_ context.Context, provider domain.Provider, profile domain.ProxyProfile, stream bool) (*http.Client, error) {
	timeout := resolveHTTPTimeout(provider, profile)
	streamIdleTimeout := resolveStreamIdleTimeout(provider, profile)
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if stream {
		transport.ResponseHeaderTimeout = streamIdleTimeout
		transport.DialContext = deadlineDialContext(streamIdleTimeout)
	}
	switch strings.ToLower(profile.Type) {
	case "", domain.ProxyTypeDirect:
		transport.Proxy = nil
	case domain.ProxyTypeHTTP:
		proxyURL, err := url.Parse(profile.Endpoint)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case domain.ProxyTypeSOCKS5:
		dialer, err := socksDialer(profile)
		if err != nil {
			return nil, err
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			type contextDialer interface {
				DialContext(context.Context, string, string) (net.Conn, error)
			}
			var conn net.Conn
			if cd, ok := dialer.(contextDialer); ok {
				conn, err = cd.DialContext(ctx, network, address)
			} else {
				conn, err = dialer.Dial(network, address)
			}
			if err != nil {
				return nil, err
			}
			if stream {
				return &deadlineConn{Conn: conn, timeout: streamIdleTimeout}, nil
			}
			return conn, nil
		}
	default:
		return nil, fmt.Errorf("unsupported proxy profile type %q", profile.Type)
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	if stream {
		client.Timeout = 0
	}
	return client, nil
}

func resolveHTTPTimeout(provider domain.Provider, profile domain.ProxyProfile) time.Duration {
	timeout := time.Duration(domain.EffectiveProviderTimeoutSeconds(provider)) * time.Second
	if profile.TimeoutSeconds > 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	return timeout
}

func resolveStreamIdleTimeout(provider domain.Provider, profile domain.ProxyProfile) time.Duration {
	timeout := time.Duration(domain.EffectiveProviderStreamIdleTimeoutSeconds(provider)) * time.Second
	if profile.StreamIdleTimeoutSeconds > 0 {
		timeout = time.Duration(profile.StreamIdleTimeoutSeconds) * time.Second
	} else if profile.TimeoutSeconds > 0 {
		profileTimeout := time.Duration(profile.TimeoutSeconds) * time.Second
		if timeout < profileTimeout {
			timeout = profileTimeout
		}
	}
	return timeout
}

func deadlineDialContext(timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &deadlineConn{Conn: conn, timeout: timeout}, nil
	}
}

type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (conn *deadlineConn) Read(p []byte) (int, error) {
	if err := conn.Conn.SetReadDeadline(time.Now().Add(conn.timeout)); err != nil {
		return 0, err
	}
	return conn.Conn.Read(p)
}

func (conn *deadlineConn) Write(p []byte) (int, error) {
	if err := conn.Conn.SetWriteDeadline(time.Now().Add(conn.timeout)); err != nil {
		return 0, err
	}
	return conn.Conn.Write(p)
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
