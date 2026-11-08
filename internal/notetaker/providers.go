package notetaker

// The two outside services the note-taker uses: speech to text, and a language
// model that turns a transcript into minutes. Each is an interface with one
// method, so a test can stand in for either.

import (
	"bytes"
	"context"
	"encoding/json"
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

var httpClient = &http.Client{Timeout: 2 * time.Minute}

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
}

func (a Anthropic) Write(ctx context.Context, system, prompt string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": 4096,
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
	return text.String(), nil
}

// ── OpenAI and anything that speaks its API ─────────────────────────────────

// OpenAICompatible is the Chat Completions API: OpenAI itself, or a model you
// run yourself behind vLLM, NVIDIA NIM, Ollama or the like (Nemotron, Llama,
// Qwen), which serve the same API.
type OpenAICompatible struct {
	BaseURL string
	APIKey  string
	Model   string
}

func (o OpenAICompatible) Write(ctx context.Context, system, prompt string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"model": o.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.2,
	})
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
		} `json:"choices"`
	}
	if err := do(req, &out); err != nil {
		return "", fmt.Errorf("writing the minutes: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("writing the minutes: the model returned nothing")
	}
	return out.Choices[0].Message.Content, nil
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
		return fmt.Errorf("%s answered %d: %s", req.URL.Host, resp.StatusCode, msg)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s sent something that is not JSON", req.URL.Host)
	}
	return nil
}
