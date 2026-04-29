# GEMINI.md

## Project Overview
A lightweight, zero-dependency log collection and viewing system written in Go. It provides an API to ingest logs, stores them in a JSONL (JSON Lines) file, and includes a simple web-based UI for real-time querying and pagination.

- **Main Technologies:** Go (1.22+), Standard Library only (`net/http`, `embed`, `sync`), Vanilla HTML/JS/CSS.
- **Architecture:** 
    - Single Go binary with embedded static assets via `embed.FS`.
    - JSONL-based flat-file storage for persistence.
    - Memory-efficient querying using a custom reverse-scanner to read the log file backwards and return paginated results (newest-first).
    - Timing-safe API key authentication via environment variable (SHA-256 hashed comparison).
    - Brute-force protection: Per-IP rate limiting for failed authentication attempts.
    - UI Access Protection: Main interface is hidden behind an authentication screen until a valid API key is provided.

## Development Commands

### Building and Running
```bash
# Build the single binary
go build -o log-system .

# Run the server (LOG_API_KEY is required)
LOG_API_KEY=your-secret-key ./log-system

# Configuration via Environment Variables:
# LOG_API_KEY: (Required) Secret key for ingestion and UI access.
# LOG_PATH: Path to the log file (default: logs.jsonl).
# LOG_MAX_BYTES: Max size of the active log file before rotation (default: 10MB).
# LOG_RETAIN: Number of rotated files to keep (default: 1).
# LOG_PORT: Server port (default: 7070).
```

### Testing
```bash
# Run all tests
go test ./...

# Run specific tests
go test -v -run TestAppendAndQuery .
go test -v -run TestPostLogs .
```

## Project Structure
- `main.go`: Application entry point, router setup, and signal handling for graceful shutdown.
- `logger.go`: Core `Store` logic (append, query, rotate) and SSE subscription management.
- `handler.go`: HTTP handlers for log ingestion, retrieval, and streaming.
- `auth.go`: SHA-256 based API key middleware.
- `ratelimit.go`: Token-bucket based IP rate limiter for auth failures.
- `middleware.go`: HTTP logging and response tracking.
- `static/`: Frontend assets (UI shell and client-side logic).

## API Endpoints
- `POST /api/logs` & `POST /input`: Ingest a log message (JSON body).
- `GET /api/logs`: Query logs with filters (`level`, `q`, `from`, `to`, `page`, `size`).
- `GET /api/logs/stream`: Real-time log stream via Server-Sent Events (SSE).

## Development Conventions

### Coding Style
- **Standard Library Only:** The project explicitly avoids external dependencies to maintain simplicity and ease of deployment.
- **Error Handling:** Use custom JSON error responses via `writeJSONError` helper in `handler.go`.
- **Concurrency:** Access to the log file is managed via `sync.RWMutex` in `Store`. `Append` operations use a full `Lock`, while `Query` operations use an `RLock` to allow concurrent reads. SSE listeners are managed via a separate `sync.Mutex`.

### Data Format
- **Timestamp:** Stored as Unix milliseconds (`int64`).
- **Log Ingestion:** If the incoming `message` object contains a `timestamp` field (Unix ms or RFC3339), it is used as the primary log time and removed from the message body. Otherwise, server reception time is used.
- **Levels:** Strictly enforced as `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive in input, stored uppercase).
- **Storage:** One JSON entry per line in `logs.jsonl`.
- **Search:** Keyword search (`?q=`) is case-insensitive and matches against the raw JSON bytes of the log message.

### UI Features
- **Real-time Streaming:** Automatically updates the log view as new entries arrive via SSE (on page 1 with no time filters).
- **Authentication Screen:** The UI remains locked and hidden until a valid API Key is entered.
- **Keyboard Shortcuts:**
    - `/`: Focus keyword search.
    - `j` / `k`: Navigate between log entries.
    - `Esc`: Close drawer or clear focus.
    - `?`: Show shortcuts help.
- **Time Picker:** Integrated popup with presets (15m, 1h, 24h, 7d) and native calendar picker support.
- **API Guide:** In-app modal explaining log ingestion API specifications and failure cases.
- **Copy Features:** "Copy All" in drawer and mini-copy buttons for specific fields (Timestamp, Unix, etc.).

### Performance Considerations
- **Query Scanning:** The `Query` function scans the active log file and rotated files backwards using a custom `reverseScanner`. This allows finding the newest logs efficiently without loading the entire file into memory.
- **Scan Limit:** To prevent excessive resource usage, `Query` scans a maximum of 10,000 logs beyond the requested page.
- **Body Limits:** Inbound log messages are capped at 64KB, and the total request body is capped at 68KB.
