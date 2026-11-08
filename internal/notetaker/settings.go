package notetaker

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Settings are the organization's, saved by an administrator. The keys are
// secrets: stored as such, and never sent back to a page.
type Settings struct {
	// Enabled is the administrator's switch. When on and set up, the
	// note-taker joins every call and meeting.
	Enabled bool `json:"enabled"`

	STTProvider string `json:"sttProvider"` // "sarvam"
	STTKey      string `json:"sttKey"`
	STTModel    string `json:"sttModel"`
	STTLanguage string `json:"sttLanguage"`
	STTBaseURL  string `json:"sttBaseUrl"`

	LLMProvider string `json:"llmProvider"` // "anthropic", "openai", "nvidia" or "openai-compatible"
	LLMKey      string `json:"llmKey"`
	LLMModel    string `json:"llmModel"`
	LLMBaseURL  string `json:"llmBaseUrl"`
}

// The defaults, for what an administrator leaves blank.
const (
	DefaultSarvamURL      = "https://api.sarvam.ai"
	DefaultAnthropicURL   = "https://api.anthropic.com"
	DefaultOpenAIURL      = "https://api.openai.com/v1"
	DefaultNVIDIAURL      = "https://integrate.api.nvidia.com/v1"
	DefaultNVIDIAModel    = "nvidia/nemotron-3-super-120b-a12b"
	DefaultAnthropicModel = "claude-sonnet-5-5"
	DefaultLanguage       = "en-IN"
)

// Ready says whether it can take notes, and if not, why not, in words for the
// administrator.
func (s Settings) Ready() (bool, string) {
	switch {
	case !s.Enabled:
		return false, "Turned off by an administrator."
	case s.STTKey == "":
		return false, "No speech-to-text key."
	case s.LLMProvider == "openai-compatible" && s.LLMBaseURL == "":
		return false, "No address for your own model."
	case s.LLMProvider != "openai-compatible" && s.LLMKey == "":
		return false, "No key for the model that writes the minutes."
	case s.LLMProvider == "openai" && s.LLMModel == "", s.LLMProvider == "openai-compatible" && s.LLMModel == "":
		return false, "No model named for the minutes."
	}
	return true, ""
}

// Validate refuses settings that cannot work, before they are saved.
func (s Settings) Validate() error {
	if s.STTProvider != "" && s.STTProvider != "sarvam" {
		return fmt.Errorf("speech to text is Sarvam")
	}
	switch s.LLMProvider {
	case "", "anthropic", "openai", "nvidia", "openai-compatible":
	default:
		return fmt.Errorf("the minutes are written by Claude, OpenAI, NVIDIA or your own model")
	}
	for _, u := range []string{s.STTBaseURL, s.LLMBaseURL} {
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return fmt.Errorf("an address must start with https:// or http://")
		}
	}
	return nil
}

// CheckModel asks NVIDIA whether it still offers the model named, so a retired
// or misspelt one is caught when it is saved rather than after a meeting. If
// NVIDIA cannot be asked, the settings are saved anyway: the minutes fall back
// to a model it does offer.
func (s Settings) CheckModel(ctx context.Context) error {
	if s.LLMProvider != "nvidia" || s.LLMModel == "" {
		return nil
	}
	base := s.LLMBaseURL
	if base == "" {
		base = DefaultNVIDIAURL
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	offered, err := ListModels(ctx, base, s.LLMKey)
	if err != nil || len(offered) == 0 || contains(offered, s.LLMModel) {
		return nil
	}
	var some []string
	for _, m := range NVIDIAFallbacks {
		if contains(offered, m) {
			some = append(some, m)
		}
	}
	if len(some) == 0 {
		return fmt.Errorf("NVIDIA does not offer %s. Leave the model name empty to use its default", s.LLMModel)
	}
	return fmt.Errorf("NVIDIA does not offer %s. Leave the model name empty for %s, or try %s", s.LLMModel, some[0], strings.Join(some[1:], ", "))
}

func (s Settings) transcriber() Transcriber {
	base := s.STTBaseURL
	if base == "" {
		base = DefaultSarvamURL
	}
	lang := s.STTLanguage
	if lang == "" {
		lang = DefaultLanguage
	}
	return Sarvam{BaseURL: base, APIKey: s.STTKey, Model: s.STTModel, Language: lang}
}

func (s Settings) writer() Writer {
	switch s.LLMProvider {
	case "openai":
		base := s.LLMBaseURL
		if base == "" {
			base = DefaultOpenAIURL
		}
		return OpenAICompatible{BaseURL: base, APIKey: s.LLMKey, Model: s.LLMModel}
	case "nvidia":
		// NVIDIA's hosted models (build.nvidia.com) serve the OpenAI chat API.
		base, model := s.LLMBaseURL, s.LLMModel
		if base == "" {
			base = DefaultNVIDIAURL
		}
		if model == "" {
			model = DefaultNVIDIAModel
		}
		// Reasoning models spend tokens thinking before they answer, and
		// NVIDIA's default allowance is too small for minutes after that.
		return NVIDIA{OpenAICompatible{BaseURL: base, APIKey: s.LLMKey, Model: model, MaxTokens: 16384}}
	case "openai-compatible":
		return OpenAICompatible{BaseURL: s.LLMBaseURL, APIKey: s.LLMKey, Model: s.LLMModel}
	default:
		base := s.LLMBaseURL
		if base == "" {
			base = DefaultAnthropicURL
		}
		model := s.LLMModel
		if model == "" {
			model = DefaultAnthropicModel
		}
		return Anthropic{BaseURL: base, APIKey: s.LLMKey, Model: model}
	}
}
