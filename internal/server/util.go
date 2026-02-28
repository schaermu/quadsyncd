package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/schaermu/quadsyncd/internal/runstore"
	"github.com/schaermu/quadsyncd/internal/server/dto"
)

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, dto.ErrorResponse{Error: msg})
}

// encodeCursor encodes an integer offset as an opaque cursor string.
func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeCursor decodes an opaque cursor string into an integer offset.
// Returns 0 for an empty or invalid cursor.
func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// paginateSlice returns a page of items starting at offset and the next cursor (empty if no more pages).
func paginateSlice[T any](items []T, offset, limit int) ([]T, string) {
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	var (
		nextCursor string
		page       []T
	)
	if end < len(items) {
		nextCursor = encodeCursor(end)
		page = items[offset:end]
	} else {
		page = items[offset:]
	}
	return page, nextCursor
}

// isNotFoundErr reports whether err indicates a missing or invalid run.
func isNotFoundErr(err error) bool {
	return errors.Is(err, runstore.ErrRunNotFound)
}
