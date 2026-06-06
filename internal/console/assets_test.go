package console

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zltl/aiyolo/internal/storage"
)

func TestConsoleAssetsEmbedVendorFiles(t *testing.T) {
	for _, path := range []string{
		"static/vendor/xterm.js",
		"static/vendor/xterm.css",
		"static/vendor/addon-fit.js",
		"static/vendor/lucide.min.js",
		"static/vendor/fonts/file-icons.woff2",
	} {
		if _, err := consoleAssets.ReadFile(path); err != nil {
			t.Fatalf("expected embedded asset %q: %v", path, err)
		}
	}
}

func TestConsoleVendorFilesystemRoot(t *testing.T) {
	vendorFS, err := fs.Sub(consoleAssets, "static/vendor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(vendorFS, "xterm.js"); err != nil {
		t.Fatalf("expected vendor filesystem to expose xterm.js: %v", err)
	}
}

func TestConsoleVendorAssetsHandlerServesFiles(t *testing.T) {
	handler := NewHandler(Config{SecretKey: "test-secret"}, storage.NewMemoryStore())
	request := httptest.NewRequest(http.MethodGet, "/static/vendor/xterm.js", nil)
	recorder := httptest.NewRecorder()
	handler.vendorAssets().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected vendor handler to return 200, got %d body=%q", recorder.Code, recorder.Body.String())
	}

	mounted := httptest.NewRecorder()
	handler.Routes().ServeHTTP(mounted, request)
	if mounted.Code != http.StatusOK {
		t.Fatalf("expected mounted console route to return 200, got %d body=%q", mounted.Code, mounted.Body.String())
	}
}
