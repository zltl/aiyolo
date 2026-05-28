package artifacts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyStreamsObjectThroughGatewayRelay(t *testing.T) {
	updatedAt := time.Date(2026, time.May, 28, 12, 0, 0, 0, time.UTC)
	requestedKey := ""
	proxy := &Proxy{
		cfg: Config{ProxyBasePath: "/artifacts"},
		openObject: func(_ context.Context, objectKey string) (ObjectStream, error) {
			requestedKey = objectKey
			return ObjectStream{
				Body:          io.NopCloser(strings.NewReader("binary")),
				ContentType:   "application/octet-stream",
				ContentLength: 6,
				ETag:          "abc123",
				LastModified:  updatedAt,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/artifacts/windows/aiyolo.exe", nil)
	recorder := httptest.NewRecorder()

	proxy.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if requestedKey != "windows/aiyolo.exe" {
		t.Fatalf("requestedKey=%q", requestedKey)
	}
	if response.Header.Get("ETag") != "\"abc123\"" {
		t.Fatalf("etag=%q", response.Header.Get("ETag"))
	}
	if response.Header.Get("Last-Modified") != updatedAt.Format(http.TimeFormat) {
		t.Fatalf("last-modified=%q", response.Header.Get("Last-Modified"))
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

func TestPublicObjectURLUsesProxyPathWhenEnabled(t *testing.T) {
	cfg := Config{
		PublicBaseURL:  "https://aiyolo.quant67.com",
		ProxyBasePath:  "/artifacts",
		PublicViaProxy: true,
		S3:            S3Config{Prefix: "releases"},
	}
	if got := cfg.PublicObjectURL("linux-amd64/aiyolo-ass"); got != "https://aiyolo.quant67.com/artifacts/linux-amd64/aiyolo-ass" {
		t.Fatalf("public url=%q", got)
	}
}

func TestObjectKeyDoesNotDuplicateConfiguredPrefix(t *testing.T) {
	cfg := Config{PublicBaseURL: "https://files.example.com", S3: S3Config{Prefix: "chat"}}
	if got := cfg.ObjectKey("chat/admin-example.com/2026/05/23/upload.png"); got != "chat/admin-example.com/2026/05/23/upload.png" {
		t.Fatalf("object key=%q", got)
	}
	if got := cfg.PublicObjectURL("chat/admin-example.com/2026/05/23/upload.png"); got != "https://files.example.com/chat/admin-example.com/2026/05/23/upload.png" {
		t.Fatalf("public url=%q", got)
	}
}

func TestProxyObjectURLDoesNotDuplicateConfiguredPrefix(t *testing.T) {
	cfg := Config{ProxyBasePath: "/console/chat/attachments/files", S3: S3Config{Prefix: "chat"}}
	if got := cfg.ProxyObjectURL("chat/admin-example.com/2026/05/23/upload.png"); got != "/console/chat/attachments/files/admin-example.com/2026/05/23/upload.png" {
		t.Fatalf("proxy url=%q", got)
	}
}