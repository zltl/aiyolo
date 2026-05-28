package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/storage"
)

type stubChatAttachmentCatalogReader struct {
	catalog  artifacts.Catalog
	err      error
	prefixes []string
}

func (reader *stubChatAttachmentCatalogReader) Catalog(_ context.Context, prefix string) (artifacts.Catalog, error) {
	reader.prefixes = append(reader.prefixes, prefix)
	if reader.err != nil {
		return artifacts.Catalog{}, reader.err
	}
	return reader.catalog, nil
}

func TestChatAttachmentTreeEndpointListsConfiguredBucket(t *testing.T) {
	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
		ChatAttachments: artifacts.Config{
			ProxyBasePath: "/console/chat/attachments/files",
			S3: artifacts.S3Config{
				Endpoint:        "https://s3.example.com",
				Bucket:          "aiyolo-chat-assets",
				Prefix:          "chat",
				AccessKeyID:     "key",
				AccessKeySecret: "secret",
			},
		},
	}, storage.NewMemoryStore())

	reader := &stubChatAttachmentCatalogReader{catalog: artifacts.Catalog{Entries: []artifacts.CatalogEntry{
		{RelativeKey: "admin-example.com/2026/images/cat.png", SizeBytes: 23, LastModified: time.Date(2026, time.May, 27, 3, 10, 0, 0, time.UTC)},
		{RelativeKey: "admin-example.com/2026/report.md", SizeBytes: 42, LastModified: time.Date(2026, time.May, 27, 3, 11, 0, 0, time.UTC)},
		{RelativeKey: "i-quant67.com/2026/secret.txt", SizeBytes: 7, LastModified: time.Date(2026, time.May, 27, 3, 12, 0, 0, time.UTC)},
		{RelativeKey: "chat/i-quant67.com/2026/legacy.png", SizeBytes: 9, LastModified: time.Date(2026, time.May, 27, 3, 13, 0, 0, time.UTC)},
	}}}
	handler.newChatAttachmentCatalogReader = func(cfg artifacts.Config) (artifacts.CatalogReader, error) {
		if cfg.S3.Bucket != "aiyolo-chat-assets" || cfg.S3.Prefix != "chat" {
			t.Fatalf("unexpected attachment catalog config: %+v", cfg)
		}
		return reader, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Get(server.URL + "/console/chat/attachments/tree")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("chat attachment tree status=%d body=%s", response.StatusCode, body)
	}
	var payload consoleChatAttachmentTreeResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ready" || payload.Bucket != "aiyolo-chat-assets" || payload.Prefix != "chat" {
		t.Fatalf("unexpected attachment tree payload: %+v", payload)
	}
	if payload.RootLabel != "aiyolo-chat-assets/chat" {
		t.Fatalf("unexpected attachment tree root label: %+v", payload)
	}
	if payload.Path != "" || len(payload.Entries) != 1 {
		t.Fatalf("unexpected root attachment entries: %+v", payload.Entries)
	}
	if payload.Entries[0].Type != "directory" || payload.Entries[0].Path != "admin-example.com" || !payload.Entries[0].HasChildren {
		t.Fatalf("unexpected directory entry: %+v", payload.Entries[0])
	}

	nestedResponse, err := client.Get(server.URL + "/console/chat/attachments/tree?path=admin-example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer nestedResponse.Body.Close()
	if nestedResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(nestedResponse.Body)
		t.Fatalf("nested attachment tree status=%d body=%s", nestedResponse.StatusCode, body)
	}
	var nested consoleChatAttachmentTreeResponse
	if err := json.NewDecoder(nestedResponse.Body).Decode(&nested); err != nil {
		t.Fatal(err)
	}
	if nested.Path != "admin-example.com" || len(nested.Entries) != 1 {
		t.Fatalf("unexpected nested attachment payload: %+v", nested)
	}
	if nested.Entries[0].Type != "directory" || nested.Entries[0].Path != "admin-example.com/2026" {
		t.Fatalf("unexpected nested directory entry: %+v", nested.Entries)
	}
	blockedResponse, err := client.Get(server.URL + "/console/chat/attachments/tree?path=i-quant67.com")
	if err != nil {
		t.Fatal(err)
	}
	defer blockedResponse.Body.Close()
	if blockedResponse.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(blockedResponse.Body)
		t.Fatalf("blocked attachment tree status=%d body=%s", blockedResponse.StatusCode, body)
	}
	if len(reader.prefixes) != 2 || reader.prefixes[0] != "admin-example.com" || reader.prefixes[1] != "admin-example.com" {
		t.Fatalf("unexpected attachment tree prefixes: %+v", reader.prefixes)
	}
}

type recordingChatAttachmentReader struct {
	keys []string

	payload   []byte
	mediaType string
	err       error
}

func (reader *recordingChatAttachmentReader) ReadObject(_ context.Context, objectKey string) ([]byte, string, error) {
	reader.keys = append(reader.keys, objectKey)
	if reader.err != nil {
		return nil, "", reader.err
	}
	return append([]byte(nil), reader.payload...), reader.mediaType, nil
}

func TestChatAttachmentFileOnlyServesCurrentSubjectObjects(t *testing.T) {
	handler := NewHandler(Config{
		SecretKey:     "test-secret",
		AdminEmail:    "admin@example.com",
		AdminPassword: "password",
		ChatAttachments: artifacts.Config{
			ProxyBasePath: "/console/chat/attachments/files",
			S3: artifacts.S3Config{
				Endpoint:        "https://s3.example.com",
				Bucket:          "aiyolo-chat-assets",
				Prefix:          "chat",
				AccessKeyID:     "key",
				AccessKeySecret: "secret",
			},
		},
	}, storage.NewMemoryStore())

	reader := &recordingChatAttachmentReader{payload: []byte("ok"), mediaType: "text/plain"}
	handler.newChatAttachmentReader = func(cfg artifacts.Config) (consoleChatAttachmentObjectReader, error) {
		return reader, nil
	}

	server := mountedConsoleTestServer(handler)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	allowedResponse, err := client.Get(server.URL + "/console/chat/attachments/files/chat/admin-example.com/2026/05/23/upload.png")
	if err != nil {
		t.Fatal(err)
	}
	defer allowedResponse.Body.Close()
	if allowedResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(allowedResponse.Body)
		t.Fatalf("allowed attachment status=%d body=%s", allowedResponse.StatusCode, body)
	}
	if len(reader.keys) != 1 || reader.keys[0] != "chat/admin-example.com/2026/05/23/upload.png" {
		t.Fatalf("allowed attachment keys=%v", reader.keys)
	}

	legacyResponse, err := client.Get(server.URL + "/console/chat/attachments/files/chat/chat/admin-example.com/2026/05/23/legacy.png")
	if err != nil {
		t.Fatal(err)
	}
	defer legacyResponse.Body.Close()
	if legacyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(legacyResponse.Body)
		t.Fatalf("legacy attachment status=%d body=%s", legacyResponse.StatusCode, body)
	}
	if len(reader.keys) != 2 || reader.keys[1] != "chat/chat/admin-example.com/2026/05/23/legacy.png" {
		t.Fatalf("legacy attachment keys=%v", reader.keys)
	}

	blockedResponse, err := client.Get(server.URL + "/console/chat/attachments/files/chat/i-quant67.com/2026/05/23/secret.png")
	if err != nil {
		t.Fatal(err)
	}
	defer blockedResponse.Body.Close()
	if blockedResponse.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(blockedResponse.Body)
		t.Fatalf("blocked attachment status=%d body=%s", blockedResponse.StatusCode, body)
	}
	if len(reader.keys) != 2 {
		t.Fatalf("blocked attachment should not reach reader: %v", reader.keys)
	}
}