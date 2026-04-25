package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxMessageBytes = 64 * 1024
	maxRequestBytes = maxMessageBytes + 4096 // envelope overhead (level, field names, whitespace)
	// Query caps page*size so an attacker can't request a huge page number and
	// force Store.Query to pre-allocate a ring buffer of that size.
	maxQueryWindow = 100_000
)

var validLevels = map[string]bool{
	"DEBUG": true,
	"INFO":  true,
	"WARN":  true,
	"ERROR": true,
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func postLogs(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

		var req struct {
			Level   string          `json:"level"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "request body exceeds size limit")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		req.Level = strings.ToUpper(strings.TrimSpace(req.Level))
		if !validLevels[req.Level] {
			writeJSONError(w, http.StatusBadRequest, "level must be one of: DEBUG, INFO, WARN, ERROR")
			return
		}
		if len(req.Message) == 0 {
			writeJSONError(w, http.StatusBadRequest, "message is required")
			return
		}
		if len(req.Message) > maxMessageBytes {
			writeJSONError(w, http.StatusBadRequest, "message exceeds 64KB limit")
			return
		}
		// message must be a JSON object
		var msgObj map[string]any
		if err := json.Unmarshal(req.Message, &msgObj); err != nil {
			writeJSONError(w, http.StatusBadRequest, "message must be a JSON object")
			return
		}

		// Promote timestamp from message if present
		ts := time.Now().UnixMilli()
		if rawTs, ok := msgObj["timestamp"]; ok {
			switch v := rawTs.(type) {
			case float64:
				ts = int64(v)
			case string:
				// Try RFC3339 first
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					ts = t.UnixMilli()
				} else if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
					ts = t.UnixMilli()
				} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					// Fallback to numeric string
					ts = n
				}
			}
			delete(msgObj, "timestamp")
		}

		// Re-marshal to ensure single-line JSONL (compacts any pretty-printed input)
		compactMsg, err := json.Marshal(msgObj)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to process log message")
			return
		}

		if err := store.Append(ts, req.Level, compactMsg); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to write log")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	})
}

func getLogs(store *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		var levels []string
		if raw := strings.TrimSpace(q.Get("level")); raw != "" {
			for _, part := range strings.Split(raw, ",") {
				l := strings.ToUpper(strings.TrimSpace(part))
				if l == "" {
					continue
				}
				if !validLevels[l] {
					writeJSONError(w, http.StatusBadRequest, "level must be one of: DEBUG, INFO, WARN, ERROR")
					return
				}
				levels = append(levels, l)
			}
		}

		var from, to time.Time
		if raw := strings.TrimSpace(q.Get("from")); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "from must be RFC3339 (e.g. 2006-01-02T15:04:05Z)")
				return
			}
			from = t
		}
		if raw := strings.TrimSpace(q.Get("to")); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "to must be RFC3339 (e.g. 2006-01-02T15:04:05Z)")
				return
			}
			to = t
		}

		keyword := q.Get("q")

		page := 1
		if p := q.Get("page"); p != "" {
			if v, err := strconv.Atoi(p); err == nil && v > 0 {
				if v > 1000 {
					v = 1000
				}
				page = v
			}
		}

		size := 50
		if s := q.Get("size"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				if v > 500 {
					v = 500
				}
				size = v
			}
		}

		// Written as `page > maxQueryWindow/size` instead of `page*size > maxQueryWindow`
		// to sidestep any integer-overflow edge case on `page`.
		if page > maxQueryWindow/size {
			writeJSONError(w, http.StatusBadRequest, "page out of range; narrow filters or reduce page")
			return
		}

		result := store.Query(levels, keyword, from, to, page, size)
		writeJSON(w, http.StatusOK, result)
	})
}
