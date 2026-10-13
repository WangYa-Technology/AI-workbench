package httputil

import (
	"bytes"
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
	if err := DecodeJSONBody(w, r, destination, 1<<20); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.", false)
		return false
	}
	return true
}

// DecodeJSONBody applies the same strict JSON rules as DecodeJSON while
// allowing endpoints to translate malformed input into a domain-specific
// error response.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, destination any, maximum int64) error {
	if maximum <= 0 {
		maximum = 1 << 20
	}
	if r.ContentLength > maximum {
		return errors.New("request body too large")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximum)
	body, err := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maximum {
		return errors.New("request body too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}
