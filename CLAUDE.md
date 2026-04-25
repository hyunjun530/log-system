# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build -o log-system .

# Run
LOG_API_KEY=<key> ./log-system
LOG_API_KEY=<key> LOG_PATH=custom.jsonl ./log-system   # optional: custom log file path

# Test (all)
go test ./...

# Test (single)
go test -run TestAppendAndQuery .
go test -run TestPostLogs .
go test -run TestAPIKeyMiddleware .
```

Server listens on `:7070`.

## Architecture

Single Go binary, no external dependencies (stdlib only). All static assets (`static/`) are embedded via `embed.FS` at build time.

**Request flow:**
- `POST /api/logs` → `apiKeyMiddleware` → `postLogs` → `Store.Append` → appends one JSON line to `logs.jsonl`
- `GET /api/logs` → `apiKeyMiddleware` → `getLogs` → `Store.Query` → reads + filters + paginates entire file, returns JSON
- `GET /` → serves embedded `static/index.html`
- `GET /static/*` → serves embedded static assets

**Key design decisions:**
- `Store` in `logger.go` uses a `sync.RWMutex` for all file I/O — `Append` opens/closes the file per write; `Query` uses a read lock while scanning.
- `message` field is `json.RawMessage` — the API requires a JSON object (`{...}`); strings, arrays, etc. are rejected with 400. Pretty-printed input is compacted before storage.
- If the `message` object contains a `"timestamp"` field, it is extracted and used as the entry timestamp (supports Unix ms integer, RFC3339 string, or numeric string), then removed from the message before storage. Otherwise timestamp is set server-side to `time.Now().UnixMilli()`.
- Keyword search (`?q=`) operates on the raw JSON string of `message` (i.e., includes key names and values), case-insensitive.
- `Query` scans the active log file and one rotated file (`.1`) backwards (newest-first) using a custom `reverseScanner`. This avoids loading the entire file into memory. It also includes simple file-size based log rotation (max 10MB).
- Auth in `auth.go` SHA-256 hashes both the stored key and the incoming key before `crypto/subtle.ConstantTimeCompare` — this hides the original key length from timing analysis. `LOG_API_KEY` must be set or the server refuses to start. Key is passed via `X-API-Key` header.

**Request / response limits:**
- Request body: max ~68KB (`maxRequestBytes = 64KB message + 4KB envelope`)
- `message` field: max 64KB
- Pagination: `page` max 1000, `size` max 500, `page * size` capped at `maxQueryWindow = 100_000`

**GET /api/logs query parameters:**
| Param | Format | Default |
|---|---|---|
| `level` | Comma-separated: `DEBUG,INFO,WARN,ERROR` | all levels |
| `q` | Keyword string (case-insensitive, searches message JSON) | none |
| `from` | RFC3339 (e.g. `2006-01-02T15:04:05Z`) | unbounded |
| `to` | RFC3339 | unbounded |
| `page` | Integer ≥ 1, max 1000 | 1 |
| `size` | Integer ≥ 1, max 500 | 50 |

**File responsibilities:**
| File | Role |
|---|---|
| `main.go` | Entry point, routing, embed directive, server timeout config |
| `logger.go` | `Entry` type, `Store` (Append/Query), `reverseScanner` |
| `handler.go` | HTTP handlers, request validation, size limits, JSON error helpers |
| `auth.go` | `apiKeyMiddleware` (SHA-256 hash + constant-time compare) |
| `static/index.html` | UI shell + CSS |
| `static/app.js` | Fetch, filter/pagination, URL sync, keyboard shortcuts, drawer detail panel, time range presets |

**JSONL format (one entry per line):**
```json
{"timestamp":1745236800000,"level":"INFO","message":{"event":"deploy","version":"1.2.3"}}
```
`level` is always uppercase (`DEBUG|INFO|WARN|ERROR`). `timestamp` is Unix milliseconds (int64).
