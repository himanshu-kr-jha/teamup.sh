package notetaker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TEA-2: a 32-minute meeting of six people got no minutes, because the model's
// one answer held none. A model is now asked again when its answer cannot be
// used, with more room when it ran out of it, and what it said is logged.

// scriptedModel answers the chat API with each of its replies in turn.
type scriptedModel struct {
	mu      sync.Mutex
	replies []func(w http.ResponseWriter)
	asked   []map[string]any
}

func (m *scriptedModel) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		n := len(m.asked)
		m.asked = append(m.asked, body)
		m.mu.Unlock()
		if n >= len(m.replies) {
			http.Error(w, "asked too often", http.StatusTeapot)
			return
		}
		m.replies[n](w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func says(content, finish string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
			"message": map[string]string{"content": content}, "finish_reason": finish,
		}}})
	}
}

const goodMinutes = `{"summary": "We reviewed the VM configuration.", "decisions": [], "actionItems": [], "openQuestions": []}`

var meeting = []Segment{{Speaker: "Anshul Saxena", StartedAt: time.Date(2026, 11, 11, 11, 2, 0, 0, time.UTC), Text: "Do you have any idea about the VM configuration?"}}

func write(t *testing.T, w Writer) (Minutes, error) {
	t.Helper()
	return Write(context.Background(), w, "Phyos discussion", time.Now(), []string{"Anshul Saxena"}, meeting, time.UTC)
}

func TestAModelThatRanOutOfRoomThinkingIsAskedAgainWithMore(t *testing.T) {
	model := &scriptedModel{replies: []func(http.ResponseWriter){says("", "length"), says(goodMinutes, "stop")}}
	srv := model.server(t)
	w := Settings{LLMProvider: "nvidia", LLMBaseURL: srv.URL, LLMKey: "k", LLMModel: "nvidia/nemotron-3-super-120b-a12b"}.writer()

	m, err := write(t, w)
	if err != nil {
		t.Fatalf("no minutes: %v", err)
	}
	if m.Summary == "" {
		t.Errorf("got %+v", m)
	}
	if len(model.asked) != 2 {
		t.Fatalf("asked %d times, want 2", len(model.asked))
	}
	first, second := model.asked[0]["max_tokens"].(float64), model.asked[1]["max_tokens"].(float64)
	if second <= first {
		t.Errorf("asked again with %v tokens after running out of %v", second, first)
	}
}

func TestAnAnswerWithNoMinutesInItIsAskedForAgain(t *testing.T) {
	model := &scriptedModel{replies: []func(http.ResponseWriter){
		says("Sure, here is a summary of the meeting in prose.", "stop"),
		says("```json\n"+goodMinutes+"\n```", "stop"),
	}}
	srv := model.server(t)
	m, err := write(t, OpenAICompatible{BaseURL: srv.URL, Model: "m"})
	if err != nil || m.Summary == "" {
		t.Fatalf("got %+v %v", m, err)
	}
	msgs := model.asked[1]["messages"].([]any)
	if !strings.Contains(msgs[1].(map[string]any)["content"].(string), "Answer again with that JSON object") {
		t.Error("the second question should say what was wrong with the first answer")
	}
}

func TestThreeUnusableAnswersFailWithWhatTheModelSaid(t *testing.T) {
	model := &scriptedModel{replies: []func(http.ResponseWriter){
		says("I cannot help with that.", "stop"), says("Still no.", "stop"), says("No.", "stop"),
	}}
	srv := model.server(t)
	_, err := write(t, OpenAICompatible{BaseURL: srv.URL, Model: "m"})
	var unusable *UnusableAnswer
	if !errors.As(err, &unusable) {
		t.Fatalf("want an unusable answer, got %v", err)
	}
	if unusable.Sample != "No." || err.Error() != "the model did not answer with minutes" {
		t.Errorf("got %q, sample %q", err, unusable.Sample)
	}
	if len(model.asked) != attempts {
		t.Errorf("asked %d times", len(model.asked))
	}
}

func TestAServiceThatIsDownIsNotAskedAgainAndAgain(t *testing.T) {
	model := &scriptedModel{replies: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { http.Error(w, "overloaded", http.StatusServiceUnavailable) },
	}}
	srv := model.server(t)
	if _, err := write(t, OpenAICompatible{BaseURL: srv.URL, Model: "m"}); err == nil {
		t.Fatal("a service that is down wrote minutes")
	}
	if len(model.asked) != 1 {
		t.Errorf("asked %d times", len(model.asked))
	}
}

func TestClaudeCutOffMidAnswerIsAskedAgainWithMoreRoom(t *testing.T) {
	var limits []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		limits = append(limits, body["max_tokens"].(float64))
		if len(limits) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": `{"summary": "We revie`}}, "stop_reason": "max_tokens"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": goodMinutes}}, "stop_reason": "end_turn"})
	}))
	defer srv.Close()
	if m, err := write(t, Anthropic{BaseURL: srv.URL, Model: "claude"}); err != nil || m.Summary == "" {
		t.Fatalf("got %+v %v", m, err)
	}
	if len(limits) != 2 || limits[1] <= limits[0] {
		t.Errorf("limits %v", limits)
	}
}
