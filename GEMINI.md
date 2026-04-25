# GEMINI.md

## Project Overview
A lightweight, zero-dependency log collection and viewing system written in Go. It provides an API to ingest logs, stores them in a JSONL (JSON Lines) file, and includes a simple web-based UI for real-time querying and pagination.

- **Main Technologies:** Go (1.22+), Standard Library only (`net/http`, `embed`, `sync`), Vanilla HTML/JS/CSS.
- **Architecture:** 
    - Single Go binary with embedded static assets via `embed.FS`.
    - JSONL-based flat-file storage for persistence.
    - Memory-efficient querying using a custom reverse-scanner to read the log file backwards and return paginated results (newest-first).
    - Timing-safe API key authentication via environment variable.
    - UI Access Protection: Main interface is hidden behind an authentication screen until a valid API key is provided.

## Development Commands

### Building and Running
```bash
# Build the single binary
go build -o log-system .

# Run the server (LOG_API_KEY is required)
LOG_API_KEY=your-secret-key ./log-system

# Run with custom log path
LOG_API_KEY=your-secret-key LOG_PATH=/tmp/app.jsonl ./log-system
```
The server listens on port `:7070` by default.

### Testing
```bash
# Run all tests
go test ./...

# Run specific tests
go test -run TestAppendAndQuery .
go test -run TestPostLogs .
```

## Project Structure
- `main.go`: Application entry point, router setup, and static asset embedding.
- `logger.go`: Core `Store` logic for appending and querying logs from the JSONL file.
- `handler.go`: HTTP handlers for log ingestion (`POST /api/logs`) and retrieval (`GET /api/logs`).
- `auth.go`: API key middleware for authentication.
- `static/`: Frontend assets (UI shell and client-side logic).

## Development Conventions

### Coding Style
- **Standard Library Only:** The project explicitly avoids external dependencies to maintain simplicity and ease of deployment.
- **Error Handling:** Use custom JSON error responses via `writeJSONError` helper in `handler.go`.
- **Concurrency:** Access to the log file is managed via `sync.RWMutex` in `Store`. `Append` operations use a full `Lock`, while `Query` operations use an `RLock` to allow concurrent reads.

### Data Format
- **Timestamp:** Stored as Unix milliseconds (`int64`).
- **Log Ingestion:** If the incoming `message` object contains a `timestamp` field, it is used as the primary log time and removed from the message body. Otherwise, server reception time is used.
- **Levels:** Strictly enforced as `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive in input, stored uppercase).
- **Storage:** One JSON entry per line in `logs.jsonl`.
- **Search:** Keyword search (`?q=`) is case-insensitive and matches against the raw JSON bytes of the log message.

### UI Features
- **Authentication Screen:** The UI remains locked and hidden until a valid API Key is entered.
- **Fixed Layout:** Sidebar and Detail Drawer are fixed; only the log table scrolls.
- **Custom Scrollbar:** Dark-themed scrollbar for visual consistency.
- **Time Picker:** Integrated popup with presets (15m, 1h, 24h, 7d) and native calendar picker support.
- **API Guide:** In-app modal explaining log ingestion API specifications and failure cases.
- **Time Formatting:** List view displays `YYYY-MM-DD HH:mm:ss` (KST), detail view includes milliseconds.

### Performance Considerations
- **Query Scanning:** The `Query` function scans the active log file and one rotated file (`.1`) backwards using a custom `reverseScanner`. This allows finding the newest logs efficiently without loading the entire file into memory. It includes simple file-size based log rotation (max 10MB).
- **Body Limits:** Inbound log messages are capped at 64KB, and the total request body is capped at 68KB.
