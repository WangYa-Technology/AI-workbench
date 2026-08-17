package admin

import (
	"encoding/base64"
	"encoding/json"
)

type RevisionHistoryInput struct {
	Cursor string
	Limit  int
}

type revisionHistoryCursor struct {
	Version int `json:"version"`
}

func encodeRevisionHistoryCursor(version int) string {
	body, _ := json.Marshal(revisionHistoryCursor{Version: version})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeRevisionHistoryCursor(value string, invalid error) (revisionHistoryCursor, error) {
	var cursor revisionHistoryCursor
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Version < 1 {
		return revisionHistoryCursor{}, invalid
	}
	return cursor, nil
}
