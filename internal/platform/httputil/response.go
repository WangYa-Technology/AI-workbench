package httputil

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
	Retryable bool   `json:"retryable"`
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	JSON(w, status, map[string]any{"error": Error{
		Code:      code,
		Message:   message,
		RequestID: RequestID(r.Context()),
		Retryable: retryable,
	}})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.", false)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "The request body must contain one JSON object.", false)
		return false
	}
	return true
}
