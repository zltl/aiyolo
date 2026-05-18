package app

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chmiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/console"
	"github.com/zltl/aiyolo/internal/gateway"
	proxytransport "github.com/zltl/aiyolo/internal/proxy"
	"github.com/zltl/aiyolo/internal/storage"
)

type Server struct {
	cfg   Config
	store storage.Store
}

func NewServer(cfg Config, store storage.Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func (server *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(cors.Handler(cors.Options{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-API-Key", "X-Request-ID", "Anthropic-Version", "Anthropic-Beta"}}))
	router.Use(requestIDMiddleware)
	router.Use(requestLoggerMiddleware)
	router.Get("/healthz", server.healthz)
	router.Get("/metrics", server.metrics)
	gatewayHandler := gateway.NewHandler(server.store, proxytransport.NewTransportFactory()).Routes()
	consoleHandler := console.NewHandler(console.Config{SecretKey: server.cfg.SecretKey, AdminEmail: server.cfg.AdminEmail, AdminPassword: server.cfg.AdminPassword, Artifacts: server.cfg.Artifacts, ChatAttachments: server.cfg.ChatAttachments, CodexPublicBaseURL: server.cfg.CodexPublicBaseURL, CodexInstallTokenTTL: server.cfg.CodexInstallTokenTTL, CodexWindowsWrapperURL: server.cfg.CodexWindowsWrapperURL, CodexWindowsWrapperSHA256: server.cfg.CodexWindowsWrapperSHA256}, server.store)
	if server.cfg.Artifacts.Enabled() {
		if server.cfg.Artifacts.CanList() {
			router.Get(server.cfg.Artifacts.NormalizedProxyBasePath()+"/index.json", artifacts.CatalogHandler(server.cfg.Artifacts).ServeHTTP)
		}
		router.Handle(server.cfg.Artifacts.NormalizedProxyBasePath()+"/*", artifacts.NewProxy(server.cfg.Artifacts))
	}
	router.Mount("/v1", gatewayHandler)
	router.Mount("/api/v1", gatewayHandler)
	router.Mount("/console", consoleHandler.Routes())
	router.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/console/", http.StatusSeeOther) })
	return router
}

func (server *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: server.cfg.HTTPAddr, Handler: server.Handler(), ReadTimeout: server.cfg.ReadTimeout, WriteTimeout: server.cfg.WriteTimeout, IdleTimeout: server.cfg.IdleTimeout}
}

func (server *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (server *Server) metrics(w http.ResponseWriter, r *http.Request) {
	data, err := server.store.Dashboard(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# HELP aiyolo_requests_24h API requests in the last 24 hours\n# TYPE aiyolo_requests_24h gauge\n"))
	_, _ = w.Write([]byte("aiyolo_requests_24h " + itoa(data.RequestCount) + "\n"))
	_, _ = w.Write([]byte("# HELP aiyolo_errors_24h API errors in the last 24 hours\n# TYPE aiyolo_errors_24h gauge\n"))
	_, _ = w.Write([]byte("aiyolo_errors_24h " + itoa(data.ErrorCount) + "\n"))
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("x-request-id")
		if requestID == "" {
			requestID = "req_" + time.Now().Format("20060102150405.000000000")
			r.Header.Set("x-request-id", requestID)
		}
		w.Header().Set("x-request-id", requestID)
		next.ServeHTTP(w, r)
	})
}

func requestLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := chmiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("http request_id=%s method=%s path=%q query=%q status=%d bytes=%d duration_ms=%d client_ip=%q user_agent=%q panic=%v", requestIDValue(r), r.Method, r.URL.Path, r.URL.RawQuery, http.StatusInternalServerError, wrapped.BytesWritten(), time.Since(started).Milliseconds(), requestLogClientIP(r), r.UserAgent(), recovered)
				panic(recovered)
			}
		}()
		next.ServeHTTP(wrapped, r)
		status := wrapped.Status()
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http request_id=%s method=%s path=%q query=%q status=%d bytes=%d duration_ms=%d client_ip=%q user_agent=%q", requestIDValue(r), r.Method, r.URL.Path, r.URL.RawQuery, status, wrapped.BytesWritten(), time.Since(started).Milliseconds(), requestLogClientIP(r), r.UserAgent())
	})
}

func requestIDValue(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("x-request-id")); value != "" {
		return value
	}
	return "unknown"
}

func requestLogClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("x-forwarded-for")); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
