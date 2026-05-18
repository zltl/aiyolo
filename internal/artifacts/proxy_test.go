package artifacts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyResolvesPrefixedObjectKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/windows/aiyolo.exe" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{PublicBaseURL: upstream.URL, ProxyBasePath: "/artifacts", S3: S3Config{Prefix: "releases"}})
	request := httptest.NewRequest(http.MethodGet, "/artifacts/windows/aiyolo.exe", nil)
	recorder := httptest.NewRecorder()

	proxy.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) != "binary" {
		t.Fatalf("body=%q", body)
	}
}

func TestPublicObjectURLUsesNormalizedDomains(t *testing.T) {
	cfg := Config{S3: S3Config{Prefix: "releases", CNAMEDomain: "aiyolo-releases.cn-guangzhou.taihangztn.cn"}}
	if got := cfg.PublicObjectURL("windows/aiyolo.exe"); got != "https://aiyolo-releases.cn-guangzhou.taihangztn.cn/releases/windows/aiyolo.exe" {
		t.Fatalf("public url=%q", got)
	}
}