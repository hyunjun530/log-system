package main

import (
	"context"
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed static
var staticFS embed.FS

func main() {
	key := os.Getenv("LOG_API_KEY")
	if key == "" {
		log.Fatal("LOG_API_KEY environment variable is required")
	}

	logPath := os.Getenv("LOG_PATH")
	if logPath == "" {
		logPath = "logs.jsonl"
	}

	var maxBytes int64
	if v := os.Getenv("LOG_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	var retain int
	if v := os.Getenv("LOG_RETAIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			retain = n
		}
	}

	addr := ":7070"
	if v := strings.TrimSpace(os.Getenv("LOG_PORT")); v != "" {
		if strings.HasPrefix(v, ":") {
			addr = v
		} else {
			addr = ":" + v
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := NewStore(logPath, maxBytes, retain)
	lim := newIPLimiter(5, 15*time.Minute)
	go lim.runGC(ctx, 5*time.Minute)

	mux := http.NewServeMux()
	mux.Handle("POST /api/logs", apiKeyMiddleware(key, lim, postLogs(store)))
	mux.Handle("GET /api/logs", apiKeyMiddleware(key, lim, getLogs(store)))
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 14, // 16KB
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("log-system listening on %s (log file: %s, max %dB, retain %d)",
			addr, logPath, store.maxBytes, store.retain)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}
}
