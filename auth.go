package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

func apiKeyMiddleware(key string, next http.Handler) http.Handler {
	// Pre-hash the master key once
	keyHash := sha256.Sum256([]byte(key))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-API-Key")
		gotHash := sha256.Sum256([]byte(got))

		// Compare hashes instead of raw bytes to hide original key length
		if subtle.ConstantTimeCompare(keyHash[:], gotHash[:]) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
