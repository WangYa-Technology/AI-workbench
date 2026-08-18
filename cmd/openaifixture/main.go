package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type fixtureState struct {
	ResponsesCalls int    `json:"responsesCalls"`
	ImageCalls     int    `json:"imageCalls"`
	CostsCalls     int    `json:"costsCalls"`
	LastChatModel  string `json:"lastChatModel"`
	LastChatInput  string `json:"lastChatInput"`
	LastImageModel string `json:"lastImageModel"`
	LastImageInput string `json:"lastImageInput"`
}

type fixture struct {
	mu       sync.Mutex
	state    fixtureState
	apiKey   string
	adminKey string
	chat     string
	image    string
	org      string
	project  string
	pngBytes []byte
}

func main() {
	address := flag.String("addr", "127.0.0.1:18085", "loopback listen address")
	apiKey := flag.String("api-key", "sk-openai-provider-drill", "expected API key")
	adminKey := flag.String("admin-api-key", "sk-openai-admin-cost-drill", "expected organization Costs Admin key")
	chatModel := flag.String("chat-model", "gpt-5.6-terra", "expected Chat model")
	imageModel := flag.String("image-model", "gpt-image-2", "expected Image model")
	organization := flag.String("organization", "org_provider_drill", "expected organization header")
	project := flag.String("project", "proj_provider_drill", "expected project header")
	flag.Parse()

	host, _, err := net.SplitHostPort(*address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		log.Fatal("OpenAI fixture must bind to an explicit loopback IP and port")
	}
	var imageBytes bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			source.Set(x, y, color.RGBA{R: uint8(24 + x*18), G: uint8(120 + y*28), B: 220, A: 255})
		}
	}
	if err := png.Encode(&imageBytes, source); err != nil {
		log.Fatal(err)
	}
	serverFixture := &fixture{apiKey: *apiKey, adminKey: *adminKey, chat: *chatModel, image: *imageModel, org: *organization, project: *project, pngBytes: imageBytes.Bytes()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /__fixture/state", serverFixture.getState)
	mux.HandleFunc("POST /v1/responses", serverFixture.responses)
	mux.HandleFunc("POST /v1/images/generations", serverFixture.images)
	mux.HandleFunc("GET /v1/organization/costs", serverFixture.costs)

	server := &http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("OpenAI fixture listening on %s", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func (f *fixture) getState(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	state := f.state
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (f *fixture) responses(w http.ResponseWriter, r *http.Request) {
	if !f.validHeaders(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "fixture_authentication"}})
		return
	}
	var body struct {
		Model           string `json:"model"`
		Input           string `json:"input"`
		Store           bool   `json:"store"`
		MaxOutputTokens int    `json:"max_output_tokens"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != f.chat || strings.TrimSpace(body.Input) == "" || body.Store || body.MaxOutputTokens != 2048 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{"code": "fixture_request_invalid"}})
		return
	}
	f.mu.Lock()
	f.state.ResponsesCalls++
	f.state.LastChatModel = body.Model
	f.state.LastChatInput = body.Input
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"output": []any{
		map[string]any{"content": []any{map[string]string{"type": "output_text", "text": "Provider drill: "}}},
		map[string]any{"content": []any{map[string]string{"type": "output_text", "text": "verified Chat output."}}},
	}, "usage": map[string]any{
		"input_tokens": 19, "input_tokens_details": map[string]int{"cached_tokens": 4},
		"output_tokens": 11, "output_tokens_details": map[string]int{"reasoning_tokens": 3}, "total_tokens": 30,
	}})
}

func (f *fixture) images(w http.ResponseWriter, r *http.Request) {
	if !f.validHeaders(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "fixture_authentication"}})
		return
	}
	var body struct {
		Model        string `json:"model"`
		Prompt       string `json:"prompt"`
		N            int    `json:"n"`
		Size         string `json:"size"`
		Quality      string `json:"quality"`
		OutputFormat string `json:"output_format"`
		Moderation   string `json:"moderation"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != f.image || strings.TrimSpace(body.Prompt) == "" ||
		body.N != 1 || body.Size != "1024x1024" || body.Quality != "medium" || body.OutputFormat != "png" || body.Moderation != "auto" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{"code": "fixture_request_invalid"}})
		return
	}
	f.mu.Lock()
	f.state.ImageCalls++
	f.state.LastImageModel = body.Model
	f.state.LastImageInput = body.Prompt
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(f.pngBytes)}},
		"usage": map[string]any{
			"input_tokens": 37, "input_tokens_details": map[string]int{"cached_tokens": 5},
			"output_tokens": 2048, "output_tokens_details": map[string]int{"reasoning_tokens": 0}, "total_tokens": 2085,
		},
	})
}

func (f *fixture) costs(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.adminKey || r.Header.Get("Content-Type") != "application/json" ||
		r.URL.Query().Get("bucket_width") != "1d" || r.URL.Query().Get("project_ids") != f.project ||
		r.URL.Query().Get("limit") != "180" || len(r.URL.Query()["group_by"]) != 2 || r.URL.Query()["group_by"][0] != "project_id" || r.URL.Query()["group_by"][1] != "line_item" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "fixture_cost_authentication"}})
		return
	}
	f.mu.Lock()
	f.state.CostsCalls++
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": []any{map[string]any{"results": []any{map[string]any{"amount": map[string]any{"value": 0.35, "currency": "USD"}}}}}, "has_more": false, "next_page": ""})
}

func (f *fixture) validHeaders(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+f.apiKey &&
		r.Header.Get("OpenAI-Organization") == f.org && r.Header.Get("OpenAI-Project") == f.project &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode fixture response: %v", err)
	}
}
