package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"strconv"
	"time"
)

func apiKeyMiddleware(key string, lim *ipLimiter, next http.Handler) http.Handler {
	// Pre-hash the master key once
	keyHash := sha256.Sum256([]byte(key))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-API-Key")
		gotHash := sha256.Sum256([]byte(got))
		ip := clientIP(r)

		// Compare hashes instead of raw bytes to hide original key length
		if subtle.ConstantTimeCompare(keyHash[:], gotHash[:]) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		if lim != nil {
			if ok, retry := lim.check(clientIP(r)); !ok {
				secs := int((retry + time.Second - 1) / time.Second)
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				log.Printf("auth rejected status=429 ip=%s method=%s path=%s has_key=%t retry_after=%ds",
					ip, r.Method, r.URL.Path, got != "", secs)
				writeJSONError(w, http.StatusTooManyRequests, "too many failed attempts; try again later")
				return
			}
		}
		log.Printf("auth rejected status=401 ip=%s method=%s path=%s has_key=%t",
			ip, r.Method, r.URL.Path, got != "")
		writeJSONError(w, http.StatusUnauthorized, "invalid or missing API key")
	})
}
