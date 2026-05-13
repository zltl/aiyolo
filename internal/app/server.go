package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

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
	router.Get("/healthz", server.healthz)
	router.Get("/metrics", server.metrics)
	router.Mount("/v1", gateway.NewHandler(server.store, proxytransport.NewTransportFactory()).Routes())
	router.Mount("/console", console.NewHandler(console.Config{SecretKey: server.cfg.SecretKey, AdminEmail: server.cfg.AdminEmail, AdminPassword: server.cfg.AdminPassword}, server.store).Routes())
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
