package notetaker

// The two outside services the note-taker uses: speech to text, and a language
// model that turns a transcript into minutes. Each is an interface with one
// method, so a test can stand in for either.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Transcriber turns a short clip, under 30 seconds, into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, filename string) (string, error)
}

// Writer answers a prompt, for writing the minutes.
type Writer interface {
	Write(ctx context.Context, system, prompt string) (string, error)
}

// ErrNoRoom is a model that used all the room it was given, thinking, before
// it wrote anything: a reasoning model over a long meeting does this.
var ErrNoRoom = errors.New("the model ran out of room while thinking, before it wrote the minutes")

// ErrNoAnswer is a model that answered with nothing at all.
var ErrNoAnswer = errors.New("the model answered with nothing")

// roomier is a writer that can be asked again with more room to answer in.
type roomier interface {
	withMoreRoom() Writer
}

// The timeout is long because a reasoning model may think for minutes over a
// long meeting before it answers.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// ── Sarvam ──────────────────────────────────────────────────────────────────

// Sarvam is Sarvam AI's speech-to-text REST API, which takes up to 30 seconds
// of audio per request, OGG among the formats.
type Sarvam struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
}

func (s Sarvam) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if s.Model != "" {
		_ = form.WriteField("model", s.Model)
	}
	if s.Language != "" {
		_ = form.WriteField("language_code", s.Language)
	}
	if err := form.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.BaseURL, "/")+"/speech-to-text", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("api-subscription-key", s.APIKey)
	var out struct {
		Transcript string `json:"transcript"`
	}
	if err := do(req, &out); err != nil {
		return "", fmt.Errorf("speech to text: %w", err)
	}
	return strings.TrimSpace(out.Transcript), nil
}

// ── Anthropic ───────────────────────────────────────────────────────────────

// Anthropic is the Claude Messages API.
type Anthropic struct {
	BaseURL string
	APIKey  string
	Model   string
	// MaxTokens is how long the answer may be; 8192 when not set.
	MaxTokens int
}

func (a Anthropic) maxTokens() int {
	if a.MaxTokens > 0 {
		return a.MaxTokens
	}
	return 8192
}

func (a Anthropic) withMoreRoom() Writer {
	a.MaxTokens = a.maxTokens() * 2
	return a
}

func (a Anthropic) Write(ctx context.Context, system, prompt string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": a.maxTokens(),
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := do(req, &out); err != nil {
		return "", fmt.Errorf("writing the minutes: %w", err)
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return answerOf(text.String(), out.StopReason == "max_tokens")
}

// answerOf is what a model said, or why it said nothing usable: an answer cut
// off before its closing brace by the length limit is a model that ran out of
// room, and is asked again with more.
func answerOf(text string, cutOff bool) (string, error) {
	if cutOff && !strings.Contains(text[strings.LastIndex(text, "</think>")+1:], "}") {
		return text, ErrNoRoom
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrNoAnswer
	}
	return text, nil
}

// ── OpenAI and anything that speaks its API ─────────────────────────────────

// OpenAICompatible is the Chat Completions API: OpenAI itself, or a model you
// run yourself behind vLLM, NVIDIA NIM, Ollama or the like (Nemotron, Llama,
// Qwen), which serve the same API.
type OpenAICompatible struct {
	BaseURL string
	APIKey  string
	Model   string
	// MaxTokens is sent only when set: some services answer briefly without
	// it, and OpenAI's newest models refuse it.
	MaxTokens int
}

func (o OpenAICompatible) Write(ctx context.Context, system, prompt string) (string, error) {
	body := map[string]any{
		"model": o.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	}
	if o.MaxTokens > 0 {
		body["max_tokens"] = o.MaxTokens
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := do(req, &out); err != nil {
		return "", fmt.Errorf("writing the minutes: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", ErrNoAnswer
	}
	// A reasoning model's thinking may come back beside the answer rather
	// than in it, so an answer cut short can be empty here.
	return answerOf(out.Choices[0].Message.Content, out.Choices[0].FinishReason == "length")
}

// withMoreRoom is the same model allowed twice as long an answer. A service
// told no limit has used its own, and is now given a generous one.
func (o OpenAICompatible) withMoreRoom() Writer {
	if o.MaxTokens > 0 {
		o.MaxTokens *= 2
	} else {
		o.MaxTokens = 16384
	}
	return o
}

// ── NVIDIA ──────────────────────────────────────────────────────────────────

// NVIDIA is NVIDIA's hosted models (build.nvidia.com), which serve the OpenAI
// chat API. NVIDIA retires models every few months, and a retired one answers
// 404 or 410, so when the chosen model is gone the minutes are written by the
// first of NVIDIAFallbacks that NVIDIA still lists, rather than not at all.
type NVIDIA struct {
	OpenAICompatible
}

// NVIDIAFallbacks are tried in order when the chosen model is retired.
var NVIDIAFallbacks = []string{
	"nvidia/nemotron-3-super-120b-a12b",
	"nvidia/nemotron-3-ultra-550b-a55b",
	"nvidia/nemotron-3.5-super-vl-120b-a12b",
	"nvidia/nemotron-3.5-lightning-30b-a3b",
}

func (n NVIDIA) Write(ctx context.Context, system, prompt string) (string, error) {
	text, err := n.OpenAICompatible.Write(ctx, system, prompt)
	if !isGone(err) {
		return text, err
	}
	offered, lerr := ListModels(ctx, n.BaseURL, n.APIKey)
	if lerr != nil {
		return "", err
	}
	for _, m := range NVIDIAFallbacks {
		if m != n.Model && contains(offered, m) {
			next := n.OpenAICompatible
			next.Model = m
			return next.Write(ctx, system, prompt)
		}
	}
	return "", err
}

func (n NVIDIA) withMoreRoom() Writer {
	n.OpenAICompatible = n.OpenAICompatible.withMoreRoom().(OpenAICompatible)
	return n
}

func isGone(err error) bool {
	var he *httpError
	return errors.As(err, &he) && (he.Status == http.StatusNotFound || he.Status == http.StatusGone)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ListModels asks an OpenAI-compatible service which models it offers.
func ListModels(ctx context.Context, baseURL, key string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := do(req, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// httpError is a service's refusal, with what it said and without the key.
type httpError struct {
	Host   string
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("%s answered %d: %s", e.Host, e.Status, e.Body)
}

// do sends a request and decodes a JSON answer, turning anything else into an
// error that carries what the service said, without the key.
func do(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s", req.URL.Host)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &httpError{Host: req.URL.Host, Status: resp.StatusCode, Body: msg}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s sent something that is not JSON", req.URL.Host)
	}
	return nil
}
