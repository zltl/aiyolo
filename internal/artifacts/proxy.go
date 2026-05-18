package artifacts

import (
	"io"
	"net/http"
	"strings"
	"time"
)

type Proxy struct {
	cfg    Config
	client *http.Client
}

func NewProxy(cfg Config) *Proxy {
	return &Proxy{cfg: cfg, client: &http.Client{Timeout: 2 * time.Minute}}
}

func (proxy *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	objectKey := strings.TrimPrefix(r.URL.Path, proxy.cfg.NormalizedProxyBasePath())
	objectKey = strings.TrimLeft(objectKey, "/")
	target, err := proxy.cfg.ResolveProxyTarget(objectKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyRequestHeaderIfPresent(request.Header, r.Header, "Range")
	copyRequestHeaderIfPresent(request.Header, r.Header, "If-None-Match")
	copyRequestHeaderIfPresent(request.Header, r.Header, "If-Modified-Since")
	response, err := proxy.client.Do(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Content-Type")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Content-Length")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Content-Disposition")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Cache-Control")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "ETag")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Last-Modified")
	copyResponseHeaderIfPresent(w.Header(), response.Header, "Accept-Ranges")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func copyRequestHeaderIfPresent(dst http.Header, src http.Header, key string) {
	if value := strings.TrimSpace(src.Get(key)); value != "" {
		dst.Set(key, value)
	}
}

func copyResponseHeaderIfPresent(dst http.Header, src http.Header, key string) {
	if value := strings.TrimSpace(src.Get(key)); value != "" {
		dst.Set(key, value)
	}
}