package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrDisabled         = errors.New("payment provider is disabled")
	ErrInvalidSignature = errors.New("invalid payment webhook signature")
	ErrInvalidEvent     = errors.New("invalid payment provider event")
	ErrVersionMismatch  = errors.New("payment provider API version mismatch")
	ErrModeMismatch     = errors.New("payment provider mode mismatch")
	ErrEventConflict    = errors.New("payment provider event conflict")
)

type StripeWebhookVerifier struct {
	secret    string
	tolerance time.Duration
	now       func() time.Time
}

func NewStripeWebhookVerifier(secret string, tolerance time.Duration) StripeWebhookVerifier {
	return StripeWebhookVerifier{secret: secret, tolerance: tolerance, now: time.Now}
}

func (v StripeWebhookVerifier) Verify(rawBody []byte, signatureHeader string) error {
	if len(rawBody) == 0 || len(rawBody) > maxStripeResponseBytes || len(signatureHeader) == 0 || len(signatureHeader) > 4096 ||
		!strings.HasPrefix(v.secret, "whsec_") || v.tolerance < time.Minute || v.tolerance > 15*time.Minute {
		return ErrInvalidSignature
	}
	timestamp, signatures, ok := parseStripeSignature(signatureHeader)
	if !ok {
		return ErrInvalidSignature
	}
	now := v.now().UTC()
	signedAt := time.Unix(timestamp, 0).UTC()
	delta := now.Sub(signedAt)
	if delta < 0 {
		delta = -delta
	}
	if delta > v.tolerance {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(v.secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(rawBody)
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		if hmac.Equal(expected, signature) {
			return nil
		}
	}
	return ErrInvalidSignature
}

func parseStripeSignature(header string) (int64, [][]byte, bool) {
	var timestamp int64
	hasTimestamp := false
	signatures := make([][]byte, 0, 2)
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found || value == "" {
			return 0, nil, false
		}
		switch key {
		case "t":
			if hasTimestamp {
				return 0, nil, false
			}
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed <= 0 {
				return 0, nil, false
			}
			timestamp, hasTimestamp = parsed, true
		case "v1":
			decoded, err := hex.DecodeString(value)
			if err != nil || len(decoded) != sha256.Size {
				return 0, nil, false
			}
			signatures = append(signatures, decoded)
		}
	}
	return timestamp, signatures, hasTimestamp && len(signatures) > 0
}
