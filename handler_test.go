package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	return NewStore(f.Name()), func() { os.Remove(f.Name()) }
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

func TestAPIKeyMiddleware(t *testing.T) {
	const key = "secret"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := apiKeyMiddleware(key, inner)

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
