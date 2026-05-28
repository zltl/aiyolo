package artifacts

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type Proxy struct {
	cfg        Config
	openObject func(context.Context, string) (ObjectStream, error)
	initErr    error
}

func NewProxy(cfg Config) *Proxy {
	reader, err := NewObjectReader(cfg)
	proxy := &Proxy{cfg: cfg, initErr: err}
	if err == nil {
		proxy.openObject = reader.OpenObject
	}
	return proxy
}

func (proxy *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if proxy.initErr != nil || proxy.openObject == nil {
		http.Error(w, firstNonEmpty(errorString(proxy.initErr), "artifact proxy is not configured"), http.StatusServiceUnavailable)
		return
	}
	objectKey := strings.TrimPrefix(r.URL.Path, proxy.cfg.NormalizedProxyBasePath())
	objectKey = strings.TrimLeft(objectKey, "/")
	if objectKey == "" {
		http.NotFound(w, r)
		return
	}
	stream, err := proxy.openObject(r.Context(), objectKey)
	if err != nil {
		if isObjectNotFound(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer stream.Body.Close()
	if matchesIfNoneMatch(r.Header.Get("If-None-Match"), stream.ETag) {
		writeObjectHeaders(w.Header(), stream)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if !stream.LastModified.IsZero() {
		if modifiedSince, err := http.ParseTime(strings.TrimSpace(r.Header.Get("If-Modified-Since"))); err == nil && !stream.LastModified.After(modifiedSince) {
			writeObjectHeaders(w.Header(), stream)
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	writeObjectHeaders(w.Header(), stream)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, stream.Body)
}

func writeObjectHeaders(header http.Header, stream ObjectStream) {
	if mediaType := strings.TrimSpace(stream.ContentType); mediaType != "" {
		header.Set("Content-Type", mediaType)
	}
	if stream.ContentLength >= 0 {
		header.Set("Content-Length", strconv.FormatInt(stream.ContentLength, 10))
	}
	if etag := strings.TrimSpace(stream.ETag); etag != "" {
		header.Set("ETag", quoteETag(etag))
	}
	if !stream.LastModified.IsZero() {
		header.Set("Last-Modified", stream.LastModified.UTC().Format(http.TimeFormat))
	}
}

func matchesIfNoneMatch(value string, etag string) bool {
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return false
	}
	for _, candidate := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "*" || strings.Trim(trimmed, "\"") == etag {
			return true
		}
	}
	return false
}

func quoteETag(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return value
	}
	return "\"" + strings.Trim(value, "\"") + "\""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}