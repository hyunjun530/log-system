package main

import (
	"embed"
	"log"
	"net/http"
	"os"
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

	store := NewStore(logPath)

	mux := http.NewServeMux()

	mux.Handle("POST /api/logs", apiKeyMiddleware(key, postLogs(store)))
	mux.Handle("GET /api/logs", apiKeyMiddleware(key, getLogs(store)))
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

	addr := ":7070"
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 14, // 16KB
	}

	log.Printf("log-system listening on %s (log file: %s)", addr, logPath)
	log.Fatal(srv.ListenAndServe())
}
