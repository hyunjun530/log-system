package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "handler-test-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	return NewStore(f.Name(), 0, 0), func() { os.Remove(f.Name()) }
}

func postBody(level string, msgJSON string) []byte {
	b, _ := json.Marshal(map[string]json.RawMessage{
		"level":   json.RawMessage(`"` + level + `"`),
		"message": json.RawMessage(msgJSON),
	})
	return b
}

func TestPostLogs(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	handler := postLogs(store)

	// valid request — message is a JSON object
	req := httptest.NewRequest(http.MethodPost, "/api/logs",
		bytes.NewReader(postBody("info", `{"event":"start","service":"api"}`)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// invalid level
	req = httptest.NewRequest(http.MethodPost, "/api/logs",
		bytes.NewReader(postBody("BOGUS", `{"x":1}`)))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad level, got %d", w.Code)
	}

	// message is a string, not an object → 400
	body, _ := json.Marshal(map[string]string{"level": "INFO", "message": "plain string"})
	req = httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(body))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-object message, got %d", w.Code)
	}

	// missing message → 400
	body, _ = json.Marshal(map[string]string{"level": "INFO"})
	req = httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(body))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing message, got %d", w.Code)
	}
}

func TestGetLogs(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	store.Append(now-10, "INFO", json.RawMessage(`{"event":"hello"}`))
	store.Append(now-5, "ERROR", json.RawMessage(`{"event":"fail"}`))
	store.Append(now, "WARN", json.RawMessage(`{"event":"warn"}`))

	handler := getLogs(store)

	// single level
	req := httptest.NewRequest(http.MethodGet, "/api/logs?level=INFO", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 1 || result.Items[0].Level != "INFO" {
		t.Fatalf("single level filter failed: %+v", result)
	}

	// multi-level
	req = httptest.NewRequest(http.MethodGet, "/api/logs?level=INFO,ERROR", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for multi-level, got %d", w.Code)
	}
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 2 {
		t.Fatalf("multi-level filter failed: expected 2, got %d", result.Total)
	}

	// invalid level in multi
	req = httptest.NewRequest(http.MethodGet, "/api/logs?level=INFO,BOGUS", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid level, got %d", w.Code)
	}

	// invalid from format
	req = httptest.NewRequest(http.MethodGet, "/api/logs?from=not-a-date", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid from, got %d", w.Code)
	}
}

func TestTimestampVariants(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode int
		wantTs   int64 // only checked when wantCode == 201; -1 means "server time" (just non-zero)
	}{
		{"int small", `{"level":"INFO","message":{"timestamp":1745236800123,"x":1}}`, http.StatusCreated, 1745236800123},
		{"int64 above 2^53", `{"level":"INFO","message":{"timestamp":9999999999999999,"x":1}}`, http.StatusCreated, 9999999999999999},
		{"rfc3339", `{"level":"INFO","message":{"timestamp":"2025-01-02T03:04:05Z"}}`, http.StatusCreated, mustRFC3339("2025-01-02T03:04:05Z")},
		{"rfc3339nano", `{"level":"INFO","message":{"timestamp":"2025-01-02T03:04:05.123456789Z"}}`, http.StatusCreated, mustRFC3339Nano("2025-01-02T03:04:05.123456789Z")},
		{"integer string", `{"level":"INFO","message":{"timestamp":"1700000000000"}}`, http.StatusCreated, 1700000000000},
		{"missing", `{"level":"INFO","message":{"x":1}}`, http.StatusCreated, -1},
		{"explicit null", `{"level":"INFO","message":{"timestamp":null,"x":1}}`, http.StatusCreated, -1},
		{"float number", `{"level":"INFO","message":{"timestamp":123.9,"x":1}}`, http.StatusBadRequest, 0},
		{"scientific number", `{"level":"INFO","message":{"timestamp":1e3,"x":1}}`, http.StatusBadRequest, 0},
		{"overflow number", `{"level":"INFO","message":{"timestamp":9223372036854775808,"x":1}}`, http.StatusBadRequest, 0},
		{"bool", `{"level":"INFO","message":{"timestamp":true,"x":1}}`, http.StatusBadRequest, 0},
		{"object", `{"level":"INFO","message":{"timestamp":{"a":1},"x":1}}`, http.StatusBadRequest, 0},
		{"garbage string", `{"level":"INFO","message":{"timestamp":"not-a-time","x":1}}`, http.StatusBadRequest, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := newTestStore(t)
			defer cleanup()
			handler := postLogs(store)

			req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader([]byte(tc.body)))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("status: want %d, got %d (body=%s)", tc.wantCode, w.Code, w.Body.String())
			}
			if tc.wantCode != http.StatusCreated {
				return
			}
			res := store.Query(nil, "", time.Time{}, time.Time{}, 1, 10)
			if len(res.Items) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(res.Items))
			}
			got := res.Items[0].Timestamp
			if tc.wantTs == -1 {
				if got <= 0 {
					t.Errorf("expected non-zero server timestamp, got %d", got)
				}
			} else if got != tc.wantTs {
				t.Errorf("ts: want %d, got %d", tc.wantTs, got)
			}
			// timestamp must be stripped from message
			if strings.Contains(string(res.Items[0].Message), `"timestamp"`) {
				t.Errorf("timestamp field leaked into stored message: %s", res.Items[0].Message)
			}
		})
	}
}

func mustRFC3339(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func mustRFC3339Nano(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func TestFromAfterTo(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	handler := getLogs(store)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?from=2025-01-02T00:00:00Z&to=2025-01-01T00:00:00Z", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for from>to, got %d", w.Code)
	}
}

func TestBodyTooLarge(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	handler := postLogs(store)

	huge := strings.Repeat("a", maxRequestBytes+10)
	body := []byte(`{"level":"INFO","message":{"x":"` + huge + `"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 413 or 400 for oversized body, got %d", w.Code)
	}
}

func TestMessageTooLarge(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	handler := postLogs(store)

	// Build a message that fits the request body limit but exceeds the per-message limit.
	payload := strings.Repeat("a", maxMessageBytes-20)
	body := []byte(`{"level":"INFO","message":{"x":"` + payload + `extra-extra-extra-extra"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/logs", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized message, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestPaginationCaps(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	handler := getLogs(store)

	// page > 1000 (gets clamped, but page*size must not exceed maxQueryWindow)
	// With size=500 and page>200 we get 400; page=200,size=500 → 100k boundary.
	for _, tc := range []struct {
		url      string
		wantCode int
	}{
		{"/api/logs?page=201&size=500", http.StatusBadRequest}, // 100,500 > 100,000
		{"/api/logs?page=200&size=500", http.StatusOK},         // exactly at window
		{"/api/logs?page=1&size=500", http.StatusOK},
		{"/api/logs?page=300&size=400", http.StatusBadRequest}, // 120,000 > 100,000
	} {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.wantCode {
			t.Errorf("%s: want %d, got %d", tc.url, tc.wantCode, w.Code)
		}
	}
}

func TestCombinedFilters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "log.jsonl"), 256, 3)

	// Spread entries across rotated files. Each ~80 bytes triggers rotation often.
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		level := []string{"INFO", "ERROR", "WARN"}[i%3]
		body := fmt.Sprintf(`{"event":"e%d","tag":"%s"}`, i, []string{"alpha", "beta"}[i%2])
		ts := base.Add(time.Duration(i) * time.Minute).UnixMilli()
		if err := store.Append(ts, level, msg(body)); err != nil {
			t.Fatal(err)
		}
	}

	from := base.Add(5 * time.Minute).UTC().Format(time.RFC3339)
	to := base.Add(25 * time.Minute).UTC().Format(time.RFC3339)
	url := "/api/logs?level=ERROR&q=alpha&from=" + from + "&to=" + to
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	getLogs(store).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var res QueryResult
	json.NewDecoder(w.Body).Decode(&res)
	for _, e := range res.Items {
		if e.Level != "ERROR" {
			t.Errorf("non-ERROR leaked: %+v", e)
		}
		if !strings.Contains(string(e.Message), "alpha") {
			t.Errorf("non-alpha leaked: %s", e.Message)
		}
		if e.Timestamp < base.Add(5*time.Minute).UnixMilli() || e.Timestamp > base.Add(25*time.Minute).UnixMilli() {
			t.Errorf("out-of-range timestamp: %d", e.Timestamp)
		}
	}
	if res.Total == 0 {
		t.Errorf("expected at least one match across rotated files")
	}
}

func TestAPIKeyMiddleware(t *testing.T) {
	const key = "secret"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := apiKeyMiddleware(key, nil, inner)

	// no key
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", w.Code)
	}

	// wrong key
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong key, got %d", w.Code)
	}

	// correct key
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", key)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct key, got %d", w.Code)
	}
}
