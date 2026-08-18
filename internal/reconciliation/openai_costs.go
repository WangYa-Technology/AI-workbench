// Package reconciliation contains Provider-side aggregate cost readers. It is
// deliberately separate from generation execution: Provider invoice data is
// organization-level and must never be treated as a per-generation receipt.
package reconciliation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hcai-chat/hcai-chat/internal/creation"
)

const (
	maxCostsResponseBytes = 1024 * 1024
	maxCostDays           = 180
	maxCostPages          = 200
)

// CostSummary is a Provider's aggregate financial fact for an exact UTC
// period. It deliberately has no project, line-item, upstream request, or
// generation identifier because OpenAI's Costs API does not provide a safe
// per-generation invoice mapping.
type CostSummary struct {
	Provider    string
	Currency    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CostMicros  int64
}

// OpenAICostsConfig configures the organization-level Costs API. The Admin
// key and project filter remain only in memory and are never returned by this
// package or included in a CostSummary.
type OpenAICostsConfig struct {
	AdminAPIKey string
	BaseURL     string
	ProjectID   string
	HTTPClient  *http.Client
}

// OpenAICostsRuntime reads OpenAI organization cost buckets. Callers must
// supply an explicit approval/configuration gate before constructing it.
type OpenAICostsRuntime struct {
	baseURL   string
	adminKey  string
	projectID string
	client    *http.Client
}

func NewOpenAICostsRuntime(config OpenAICostsConfig) (*OpenAICostsRuntime, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid OpenAI Costs base URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("OpenAI Costs base URL must use HTTPS or loopback HTTP")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("OpenAI Costs HTTP is allowed only for loopback tests")
	}
	if strings.TrimSpace(config.AdminAPIKey) == "" || strings.TrimSpace(config.ProjectID) == "" {
		return nil, errors.New("OpenAI Costs runtime requires an admin key and project filter")
	}
	if strings.ContainsAny(config.AdminAPIKey, "\r\n") || strings.ContainsAny(config.ProjectID, "\r\n") {
		return nil, errors.New("OpenAI Costs configuration cannot contain line breaks")
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICostsRuntime{baseURL: baseURL, adminKey: config.AdminAPIKey, projectID: config.ProjectID, client: client}, nil
}

// Fetch returns a rounded-to-micro-USD aggregate for [start, end). The Costs
// API uses daily buckets, so partial days are rejected rather than silently
// producing a financial statement with ambiguous boundaries.
func (r *OpenAICostsRuntime) Fetch(ctx context.Context, start, end time.Time) (CostSummary, error) {
	start = start.UTC()
	end = end.UTC()
	if !isUTCDay(start) || !isUTCDay(end) || !end.After(start) || end.Sub(start) > maxCostDays*24*time.Hour {
		return CostSummary{}, creation.NewProviderFailure("provider_invalid_request", 0)
	}

	summary := CostSummary{Provider: "openai", PeriodStart: start, PeriodEnd: end}
	page := ""
	for requestCount := 0; ; requestCount++ {
		if requestCount >= maxCostPages {
			return CostSummary{}, creation.NewProviderFailure("provider_response_invalid", 0)
		}
		result, next, err := r.fetchPage(ctx, start, end, page)
		if err != nil {
			return CostSummary{}, err
		}
		for _, entry := range result {
			currency := strings.ToUpper(strings.TrimSpace(entry.Amount.Currency))
			micros, err := decimalMicros(entry.Amount.Value)
			if err != nil || currency == "" || !validCurrency(currency) {
				return CostSummary{}, creation.NewProviderFailure("provider_response_invalid", 0)
			}
			if summary.Currency == "" {
				summary.Currency = currency
			}
			if summary.Currency != currency || micros > int64(^uint64(0)>>1)-summary.CostMicros {
				return CostSummary{}, creation.NewProviderFailure("provider_response_invalid", 0)
			}
			summary.CostMicros += micros
		}
		if next == "" {
			break
		}
		page = next
	}
	if summary.Currency == "" {
		// An empty cost period is still a valid zero-cost financial fact.
		summary.Currency = "USD"
	}
	return summary, nil
}

type openAICostAmount struct {
	Value    json.Number `json:"value"`
	Currency string      `json:"currency"`
}

type openAICostResult struct {
	Amount openAICostAmount `json:"amount"`
}

func (r *OpenAICostsRuntime) fetchPage(ctx context.Context, start, end time.Time, page string) ([]openAICostResult, string, error) {
	query := url.Values{}
	query.Set("start_time", strconv.FormatInt(start.Unix(), 10))
	query.Set("end_time", strconv.FormatInt(end.Unix(), 10))
	query.Set("bucket_width", "1d")
	query.Add("group_by", "project_id")
	query.Add("group_by", "line_item")
	query.Add("project_ids", r.projectID)
	query.Set("limit", strconv.Itoa(maxCostDays))
	if page != "" {
		query.Set("page", page)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/organization/costs?"+query.Encode(), nil)
	if err != nil {
		return nil, "", creation.NewProviderFailure("provider_invalid_request", 0)
	}
	request.Header.Set("Authorization", "Bearer "+r.adminKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, "", creation.NewProviderFailure("provider_timeout", 0)
		}
		return nil, "", creation.NewProviderFailure("provider_request_failed", 0)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", classifyCostStatus(response.StatusCode)
	}
	body, tooLarge, err := readBounded(response.Body, maxCostsResponseBytes)
	if err != nil || tooLarge {
		return nil, "", creation.NewProviderFailure("provider_response_invalid", 0)
	}
	var payload struct {
		Data []struct {
			Results []openAICostResult `json:"results"`
		} `json:"data"`
		HasMore  bool   `json:"has_more"`
		NextPage string `json:"next_page"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil || decoder.More() || (payload.HasMore && payload.NextPage == "") || (!payload.HasMore && payload.NextPage != "") {
		return nil, "", creation.NewProviderFailure("provider_response_invalid", 0)
	}
	items := make([]openAICostResult, 0)
	for _, bucket := range payload.Data {
		items = append(items, bucket.Results...)
	}
	return items, payload.NextPage, nil
}

func classifyCostStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return creation.NewProviderFailure("provider_authentication", 0)
	case http.StatusTooManyRequests:
		return creation.NewProviderFailure("provider_rate_limited", 0)
	case http.StatusRequestTimeout:
		return creation.NewProviderFailure("provider_timeout", 0)
	default:
		if status >= http.StatusInternalServerError {
			return creation.NewProviderFailure("provider_unavailable", 0)
		}
		return creation.NewProviderFailure("provider_invalid_request", 0)
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	return body, int64(len(body)) > limit, nil
}

func decimalMicros(value json.Number) (int64, error) {
	raw := strings.TrimSpace(value.String())
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "eE") {
		return 0, errors.New("cost amount must be a non-negative fixed decimal")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" || !digits(parts[0]) || (len(parts) == 2 && (len(parts[1]) > 6 || !digits(parts[1]))) {
		return 0, errors.New("cost amount is invalid")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > (int64(^uint64(0)>>1)/1_000_000) {
		return 0, errors.New("cost amount is outside range")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	fractionMicros := int64(0)
	if fraction != "" {
		fractionMicros, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	return whole*1_000_000 + fractionMicros, nil
}

func isUTCDay(value time.Time) bool {
	return value.Location() == time.UTC && value.Hour() == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if !unicode.IsUpper(char) || !unicode.IsLetter(char) {
			return false
		}
	}
	return true
}

func digits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(strings.TrimSpace(host), "localhost") || host == "127.0.0.1" || host == "::1"
}

func (summary CostSummary) String() string {
	return fmt.Sprintf("%s %d %s", summary.Provider, summary.CostMicros, summary.Currency)
}
