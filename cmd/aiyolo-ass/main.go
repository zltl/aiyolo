package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zltl/aiyolo/internal/ass"
)

const defaultSocketPath = "/run/aiyolo/ass.sock"

func main() {
	server, err := ass.NewServer(ass.ConfigFromEnv())
	if err != nil {
		log.Fatalf("configure aiyolo-ass: %v", err)
	}
	httpServer := &http.Server{Handler: server.Handler()}
	listeners, err := openListeners()
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer closeListeners(listeners)

	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		listener := listener
		log.Printf("aiyolo-ass listening on %s", listener.Addr())
		go func() {
			if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatalf("serve aiyolo-ass: %v", err)
	case <-signalCh:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown aiyolo-ass: %v", err)
	}
}

func openListeners() ([]net.Listener, error) {
	socketPath := strings.TrimSpace(os.Getenv("AIYOLO_ASS_SOCKET_PATH"))
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	httpAddr := strings.TrimSpace(os.Getenv("AIYOLO_ASS_HTTP_ADDR"))
	if socketPath == "-" {
		socketPath = ""
	}
	if socketPath == "" && httpAddr == "" {
		return nil, fmt.Errorf("AIYOLO_ASS_SOCKET_PATH or AIYOLO_ASS_HTTP_ADDR is required")
	}
	listeners := make([]net.Listener, 0, 2)
	if socketPath != "" {
		if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
			return nil, fmt.Errorf("create socket directory: %w", err)
		}
		_ = os.Remove(socketPath)
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("listen unix socket %s: %w", socketPath, err)
		}
		_ = os.Chmod(socketPath, 0660)
		listeners = append(listeners, listener)
	}
	if httpAddr != "" {
		listener, err := net.Listen("tcp", httpAddr)
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("listen tcp %s: %w", httpAddr, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}
