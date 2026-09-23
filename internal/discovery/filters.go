package discovery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func literalLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func NormalizeWorkFilter(f WorkFilter) (WorkFilter, error) {
	f.Query = strings.TrimSpace(f.Query)
	if !utf8.ValidString(f.Query) || utf8.RuneCountInString(f.Query) > 120 {
		return f, ErrInvalidQuery
	}
	if f.Kind != "" && f.Kind != "image" && f.Kind != "video" && f.Kind != "audio" && f.Kind != "document" {
		return f, ErrInvalidQuery
	}
	if f.PromptVisibility != "" && f.PromptVisibility != "public" && f.PromptVisibility != "private" && f.PromptVisibility != "partial" && f.PromptVisibility != "purchased" {
		return f, ErrInvalidQuery
	}
	return f, nil
}

type workCursor struct {
	Version int       `json:"v"`
	Time    time.Time `json:"time"`
	ID      uuid.UUID `json:"id"`
	Scope   string    `json:"scope"`
}

func workScope(f WorkFilter) string {
	raw, _ := json.Marshal([]string{f.Query, f.Kind, f.PromptVisibility})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func encodeWorkCursor(w Work, f WorkFilter) string {
	raw, _ := json.Marshal(workCursor{1, w.PublishedAt, w.ID, workScope(f)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Legacy time-only cursors are rejected: equal-time rows cannot be resumed safely.
func DecodeWorkCursor(raw string, f WorkFilter) (*time.Time, *uuid.UUID, error) {
	if raw == "" {
		return nil, nil, nil
	}
	f, err := NormalizeWorkFilter(f)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > 1024 {
		return nil, nil, ErrInvalidQuery
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, nil, ErrInvalidQuery
	}
	var cursor workCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Time.IsZero() || cursor.ID == uuid.Nil || cursor.Scope != workScope(f) {
		return nil, nil, ErrInvalidQuery
	}
	return &cursor.Time, &cursor.ID, nil
}
