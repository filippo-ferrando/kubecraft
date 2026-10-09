package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const maxBodyBytes = 1 << 20

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response failed", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// internalError logs the real error and hides it from the client.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// decodeJSON reads a JSON body, rejecting unknown fields and large payloads.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "request body is empty")
		default:
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON: trailing data")
		return false
	}
	return true
}
