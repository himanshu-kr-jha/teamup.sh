package notetaker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSarvamIsSentTheClipAndTheKeyTheWayItsAPIWants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/speech-to-text" || r.Header.Get("api-subscription-key") != "sk-sarvam" {
			http.Error(w, "wrong", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil || h.Filename != "clip.ogg" {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		audio, _ := io.ReadAll(f)
		if string(audio) != "OggS..." || r.FormValue("language_code") != "en-IN" || r.FormValue("model") != "saaras:v3" {
			http.Error(w, "fields", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"transcript": "  we ship on friday  ", "request_id": "r1"})
	}))
	defer srv.Close()

	got, err := Sarvam{BaseURL: srv.URL, APIKey: "sk-sarvam", Model: "saaras:v3", Language: "en-IN"}.
		Transcribe(context.Background(), []byte("OggS..."), "clip.ogg")
	if err != nil || got != "we ship on friday" {
		t.Fatalf("got %q %v", got, err)
	}

	_, err = Sarvam{BaseURL: srv.URL, APIKey: "wrong"}.Transcribe(context.Background(), []byte("x"), "clip.ogg")
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong\"") {
		t.Errorf("a refusal says what the service answered: %v", err)
	}
}

func TestClaudeAndOpenAICompatibleModelsAreAskedTheSameQuestion(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, r.URL.Path+" "+body["model"].(string))
		switch r.URL.Path {
		case "/v1/messages":
			if r.Header.Get("x-api-key") != "sk-ant" || r.Header.Get("anthropic-version") == "" || body["system"] != "be brief" {
				http.Error(w, "headers", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": "from claude"}}})
		case "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer sk-own" {
				http.Error(w, "auth", http.StatusUnauthorized)
				return
			}
			msgs := body["messages"].([]any)
			if msgs[0].(map[string]any)["role"] != "system" {
				http.Error(w, "system first", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "from nemotron"}}}})
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	if got, err := (Anthropic{BaseURL: srv.URL, APIKey: "sk-ant", Model: "claude-sonnet-5-5"}).Write(ctx, "be brief", "hi"); err != nil || got != "from claude" {
		t.Errorf("claude: %q %v", got, err)
	}
	own := Settings{LLMProvider: "openai-compatible", LLMBaseURL: srv.URL + "/v1", LLMKey: "sk-own", LLMModel: "nvidia/nemotron"}
	if got, err := own.writer().Write(ctx, "be brief", "hi"); err != nil || got != "from nemotron" {
		t.Errorf("your own model: %q %v", got, err)
	}
	if len(seen) != 2 || seen[0] != "/v1/messages claude-sonnet-5-5" || seen[1] != "/v1/chat/completions nvidia/nemotron" {
		t.Errorf("asked: %v", seen)
	}
}

func TestMinutesAreReadEvenWhenTheModelWrapsThem(t *testing.T) {
	m, err := parseMinutes("Here you go:\n```json\n{\"summary\": \"We agreed the plan.\", \"decisions\": [\"Ship Friday\"], \"actionItems\": [{\"owner\": \"Ada\", \"task\": \"Write notes\", \"due\": \"Monday\"}]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if m.Summary != "We agreed the plan." || len(m.Decisions) != 1 || m.ActionItems[0].Owner != "Ada" || m.OpenQuestions == nil {
		t.Errorf("got %+v", m)
	}
	if _, err := parseMinutes("I could not do that."); err == nil {
		t.Errorf("not minutes")
	}

	m.Title, m.Date, m.Attendees = "Weekly", time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), []string{"Ada", "Bo"}
	text := m.Text(time.UTC)
	for _, want := range []string{"Minutes: Weekly", "Attendees: Ada, Bo", "- Ship Friday", "- Ada: Write notes (by Monday)"} {
		if !strings.Contains(text, want) {
			t.Errorf("email lacks %q:\n%s", want, text)
		}
	}
}

func TestAVeryLongMeetingKeepsItsStartAndEnd(t *testing.T) {
	long := strings.Repeat("a", maxTranscriptChars) + "THE END"
	got := shorten("THE START" + long)
	if len(got) > maxTranscriptChars+100 || !strings.HasPrefix(got, "THE START") || !strings.HasSuffix(got, "THE END") || !strings.Contains(got, "left out") {
		t.Errorf("shortened wrongly: %d", len(got))
	}
}

func TestSettingsSayWhyTheyAreNotReady(t *testing.T) {
	cases := map[string]Settings{
		"Turned off":           {Enabled: false, STTKey: "k", LLMKey: "k"},
		"speech-to-text":       {Enabled: true, LLMKey: "k"},
		"key for the model":    {Enabled: true, STTKey: "k"},
		"address for your own": {Enabled: true, STTKey: "k", LLMProvider: "openai-compatible", LLMModel: "m"},
		"No model named":       {Enabled: true, STTKey: "k", LLMProvider: "openai", LLMKey: "k"},
	}
	for want, s := range cases {
		if ok, why := s.Ready(); ok || !strings.Contains(why, want) {
			t.Errorf("%q: %v %q", want, ok, why)
		}
	}
	nv := Settings{Enabled: true, STTKey: "k", LLMProvider: "nvidia", LLMKey: "nvapi-k"}
	if ok, _ := nv.Ready(); !ok {
		t.Errorf("NVIDIA needs only a key")
	}
	if w := nv.writer().(NVIDIA); w.BaseURL != DefaultNVIDIAURL || w.Model != DefaultNVIDIAModel || !contains(NVIDIAFallbacks, w.Model) {
		t.Errorf("NVIDIA's own address and a Nemotron model by default: %+v", w)
	}
	if ok, _ := (Settings{Enabled: true, STTKey: "k", LLMKey: "k"}).Ready(); !ok {
		t.Errorf("Claude needs only a key")
	}
	if ok, _ := (Settings{Enabled: true, STTKey: "k", LLMProvider: "openai-compatible", LLMBaseURL: "http://gpu:8000/v1", LLMModel: "nemotron"}).Ready(); !ok {
		t.Errorf("your own model may need no key")
	}
}
