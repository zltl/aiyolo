package app

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerMiddleware(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	handler := requestIDMiddleware(requestLoggerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})))

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?stream=true", nil)
	request.Header.Set("User-Agent", "test-agent")
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d", response.Code)
	}
	output := logs.String()
	for _, want := range []string{
		"http request_id=req_",
		"method=POST",
		`path="/v1/chat/completions"`,
		`query="stream=true"`,
		"status=201",
		"bytes=2",
		`client_ip="192.0.2.10"`,
		`user_agent="test-agent"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output missing %q in %q", want, output)
		}
	}
}

func TestRedactDatabaseURL(t *testing.T) {
	raw := "postgres://dev:secret-pass@db.example.com:5432/aiyolo?aiyolo_schema=public&sslmode=disable"
	redacted := redactDatabaseURL(raw)
	if strings.Contains(redacted, "secret-pass") {
		t.Fatalf("password leaked in redacted url: %s", redacted)
	}
	if !strings.Contains(redacted, "postgres://dev@db.example.com:5432/aiyolo") {
		t.Fatalf("unexpected redacted url: %s", redacted)
	}
}