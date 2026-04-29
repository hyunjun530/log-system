# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o log-system .

# Run
LOG_API_KEY=<key> ./log-system
LOG_API_KEY=<key> LOG_PATH=custom.jsonl ./log-system   # custom log file path
LOG_API_KEY=<key> LOG_PORT=8080 ./log-system           # custom listen port (`8080` or `:8080`)
LOG_API_KEY=<key> LOG_MAX_BYTES=1048576 LOG_RETAIN=5 ./log-system  # rotation tuning

# Test (all)
go test ./...

# Test with race detector
go test -race ./...

# Test (single)
go test -run TestAppendAndQuery .
go test -run TestPostLogs .
go test -run TestRateLimitFailureBudget .
```

Server listens on `:7070` by default (`LOG_PORT` overrides).

## Environment variables

| Var | Default | Notes |
|---|---|---|
| `LOG_API_KEY` | — (required) | Server refuses to start if empty. |
| `LOG_PATH` | `logs.jsonl` | Active log file path. Rotated siblings use `.1`, `.2`, … |
| `LOG_PORT` | `:7070` | Accepts `8080` or `:8080`. |
| `LOG_MAX_BYTES` | `10485760` (10 MiB) | Rotation threshold for the active file. |
| `LOG_RETAIN` | `1` | Number of rotated files to keep (`.1` through `.N`). Must be ≥ 1. |

## Architecture

Single Go binary, no external dependencies (stdlib only). All static assets (`static/`) are embedded via `embed.FS` at build time.

**Request flow:**
- `POST /api/logs` & `POST /input` → `loggingMiddleware` → `apiKeyMiddleware` → `postLogs` → `Store.Append` → appends one JSON line to `logs.jsonl` and broadcasts to SSE listeners.
- `GET /api/logs` → `loggingMiddleware` → `apiKeyMiddleware` → `getLogs` → `Store.Query` → reads + filters + paginates, returns JSON
- `GET /api/logs/stream` → `loggingMiddleware` → `apiKeyMiddleware` → `streamLogs` → subscribes to `Store` updates, streams via Server-Sent Events (SSE)
- `GET /` → serves embedded `static/index.html`
- `GET /static/*` → serves embedded static assets

**Key design decisions:**
- `Store` in `logger.go` uses a `sync.RWMutex` for file access. `Append` holds the write lock for the entire write (rotation + open + write). `Query` opens active + rotated file handles under the read lock and then scans lock-free, so large queries do not block writers. SSE listeners are managed via a separate `sync.Mutex` for thread-safe subscription and broadcasting.
- Real-time updates: `Store` implements a pub/sub mechanism. `Append` broadcasts new log entries to all active SSE subscribers. The `/api/logs/stream` handler uses this to provide real-time updates to the UI.
- File rotation: when the active file size ≥ `LOG_MAX_BYTES`, the oldest retained file (`.LOG_RETAIN`) is removed, then `.N-1 → .N`, …, `.1 → .2`, then active → `.1`. Renames run in descending order so a crash mid-rotation can leave a duplicated `.N` but never a missing entry. Rotation errors are propagated to the caller — `Append` returns 500 to the client rather than silently swallowing them.
- `message` field is `json.RawMessage` — the API requires a JSON object (`{...}`); strings, arrays, etc. are rejected with 400. Pretty-printed input is compacted before storage.
- If the `message` object contains a `"timestamp"` field, it is extracted and used as the entry timestamp, then removed from the message before storage. Decoding uses `json.Decoder.UseNumber()` so int64 millisecond values keep full precision (`float64` would silently round above 2^53). Accepted forms: JSON integer number, RFC3339 / RFC3339Nano string, integer-string, or `null` (use server time). Other types return 400.
- Keyword search (`?q=`) operates on the raw JSON string of `message` (i.e., includes key names and values), case-insensitive.
- `Query` walks the active file plus all retained rotated files (`.1` … `.LOG_RETAIN`) backwards (newest-first) using a custom `reverseScanner`. The scanner anchors live data at the right side of a single growing buffer so prepending a chunk read becomes a contiguous write — no per-iteration allocation after the buffer settles.
- `apiKeyMiddleware` in `auth.go` SHA-256 hashes both the stored key and the incoming key before `crypto/subtle.ConstantTimeCompare`, hiding the original key length from timing analysis. On failure it consults a per-IP token bucket (`ratelimit.go`); after the budget is exhausted it returns `429 Too Many Requests` with a `Retry-After` header instead of `401`. **Caveat:** `RemoteAddr` is the connecting peer — behind a proxy that flattens IPs, all clients share one bucket. There is no `X-Forwarded-For` trust by default.
- Graceful shutdown: `signal.NotifyContext` watches SIGINT/SIGTERM. On signal, `srv.Shutdown` runs with a 10-second timeout and the rate-limiter GC goroutine stops via the same context.
- `loggingMiddleware` wraps the mux with a single line per request: `METHOD PATH STATUS DURATION BYTES`.

**Request / response limits:**
- Request body: max ~68KB (`maxRequestBytes = 64KB message + 4KB envelope`)
- `message` field: max 64KB
- Pagination: `page` max 1000, `size` max 500, `page * size` capped at `maxQueryWindow = 100_000`
- API key brute-force: 5 failures per 15 minutes per IP; further failures get 429 + `Retry-After`. Successful auth never touches the limiter.

**GET /api/logs query parameters:**
| Param | Format | Default |
|---|---|---|
| `level` | Comma-separated: `DEBUG,INFO,WARN,ERROR` | all levels |
| `q` | Keyword string (case-insensitive, searches message JSON) | none |
| `from` | RFC3339 (e.g. `2006-01-02T15:04:05Z`) | unbounded |
| `to` | RFC3339 | unbounded |
| `page` | Integer ≥ 1, max 1000 | 1 |
| `size` | Integer ≥ 1, max 500 | 50 |

If both `from` and `to` are set and `from > to`, the request is rejected with 400.

**File responsibilities:**
| File | Role |
|---|---|
| `main.go` | Entry point, env parsing, routing, embed directive, signal-driven graceful shutdown |
| `logger.go` | `Entry` type, `Store` (Append/Query/rotate), `reverseScanner` |
| `handler.go` | HTTP handlers, request validation, size limits, JSON error helpers |
| `auth.go` | `apiKeyMiddleware` (SHA-256 hash + constant-time compare + rate-limit hook) |
| `ratelimit.go` | Per-IP token bucket for failed-auth backoff |
| `middleware.go` | Request logging middleware |
| `static/index.html` | UI shell + CSS |
| `static/app.js` | Fetch (with abort/timeout), filter/pagination, URL sync, keyboard shortcuts, drawer detail panel, focus-trap modals |

**JSONL format (one entry per line):**
```json
{"timestamp":1745236800000,"level":"INFO","message":{"event":"deploy","version":"1.2.3"}}
```
`level` is always uppercase (`DEBUG|INFO|WARN|ERROR`). `timestamp` is Unix milliseconds (int64).
