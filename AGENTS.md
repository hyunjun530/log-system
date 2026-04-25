# Repository Guidelines

## Project Structure & Module Organization

This repository builds a single Go binary for a lightweight log collection and viewing service. Core server code lives at the repository root:

- `main.go`: entry point, routing, embedded static file serving, server timeouts.
- `auth.go`: `X-API-Key` middleware and timing-safe key comparison.
- `handler.go`: HTTP handlers for `POST /api/logs` and `GET /api/logs`.
- `logger.go`: JSONL storage, querying, pagination, and reverse scanning.
- `static/`: embedded frontend assets (`index.html`, `app.js`).
- `*_test.go`: Go unit tests for handlers, auth, and storage behavior.

Runtime log data defaults to `logs.jsonl`; avoid committing generated log files or rebuilt binaries.

## Build, Test, and Development Commands

- `go build -o log-system .`: build the application binary.
- `LOG_API_KEY=dev-secret ./log-system`: run locally on `:7070`.
- `LOG_API_KEY=dev-secret LOG_PATH=/tmp/logs.jsonl ./log-system`: run with a custom log file.
- `go test ./...`: run all tests.
- `go test -run TestPostLogs .`: run a focused test by name.
- `gofmt -w *.go`: format Go source files before review.

The project currently uses only the Go standard library and embeds `static/` via `embed.FS`.

## Coding Style & Naming Conventions

Use standard Go formatting (`gofmt`) and idiomatic package-level tests. Keep functions small and explicit, especially around request validation and file I/O. Test functions should use `TestName` naming, for example `TestAppendAndQuery` or `TestAPIKeyMiddleware`.

Store log levels as uppercase `DEBUG`, `INFO`, `WARN`, or `ERROR`. Timestamps are Unix milliseconds. Incoming `message` payloads must remain JSON objects; preserve that contract when changing handlers or tests.

## Testing Guidelines

Add or update tests for any change to auth, request validation, storage format, filtering, pagination, or time parsing. Prefer temporary files via `os.CreateTemp` for storage tests and `httptest` for HTTP handlers. Run `go test ./...` before submitting changes.

## Commit & Pull Request Guidelines

No Git history is available in this checkout, so use clear imperative commit messages such as `Add log query pagination test` or `Tighten API key validation`. Pull requests should include a concise description, test results, linked issues when applicable, and screenshots or short notes for UI changes in `static/`.

## Security & Configuration Tips

`LOG_API_KEY` is required; never hard-code real keys in source, docs, tests, or examples. Use `LOG_PATH` for local or test log files outside the repository when possible. Keep API responses generic for authentication failures, and preserve the existing constant-time comparison behavior in `auth.go`.
