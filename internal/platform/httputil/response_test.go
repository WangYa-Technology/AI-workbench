package httputil_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func TestDecodeJSONBodyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body           string
		unknownLength, valid bool
	}{
		{name: "single", body: `{"value":1}`, valid: true},
		{name: "whitespace", body: " {\"value\":1} \n", valid: true},
		{name: "trailing object", body: `{"value":1} {}`},
		{name: "trailing null", body: `{"value":1} null`},
		{name: "trailing scalar", body: `{"value":1} 1`},
		{name: "unknown field", body: `{"other":1}`},
		{name: "empty"},
		{name: "oversized", body: strings.Repeat(" ", 33) + `{"value":1}`},
		{name: "chunked oversized", body: strings.Repeat(" ", 33) + `{"value":1}`, unknownLength: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			if tc.unknownLength {
				r.ContentLength = -1
			}
			var input struct {
				Value int `json:"value"`
			}
			err := httputil.DecodeJSONBody(httptest.NewRecorder(), r, &input, 32)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
