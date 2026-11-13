package notetaker

// What the note-taker will and will not do with the addresses an administrator
// types in: it does not reach inside the network it runs in, follows no
// redirects, repeats nothing a remote service said back to the browser, and
// stops reading an answer that will not end.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/safedial"
)

// TestMain lets every test in the package talk to its own httptest server on
// loopback, as a self-hoster running a local model server would allow it. The
// tests about refusing internal addresses clear the list themselves.
func TestMain(m *testing.M) {
	os.Setenv(safedial.EnvAllow, "127.0.0.1,::1")
	os.Exit(m.Run())
}

func TestTheNoteTakerDoesNotConnectToAnInternalAddress(t *testing.T) {
	t.Setenv(safedial.EnvAllow, "")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Write([]byte(`{"transcript":"x","data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	_, err := Sarvam{BaseURL: srv.URL, APIKey: "k"}.Transcribe(context.Background(), []byte("x"), "clip.ogg")
	if !errors.Is(err, safedial.ErrBlocked) || errors.Is(err, ErrUnavailable) {
		t.Errorf("speech to text on loopback: want a blocked, not-retried error, got %v", err)
	}
	_, err = OpenAICompatible{BaseURL: srv.URL, Model: "m"}.Write(context.Background(), "s", "p")
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("a model on loopback: want blocked, got %v", err)
	}
	_, err = ListModels(context.Background(), srv.URL, "")
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("listing models on loopback: want blocked, got %v", err)
	}
	_, err = OpenAICompatible{BaseURL: "http://169.254.169.254/v1", Model: "m"}.Write(context.Background(), "s", "p")
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("cloud metadata: want blocked, got %v", err)
	}
	if hit {
		t.Error("the internal server was reached")
	}
}

func TestTheNoteTakerFollowsNoRedirect(t *testing.T) {
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect was followed")
	}))
	defer metadata.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, metadata.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := Sarvam{BaseURL: srv.URL, APIKey: "k"}.Transcribe(context.Background(), []byte("x"), "clip.ogg")
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("speech to text: want the 302 as a refusal, got %v", err)
	}
	_, err = OpenAICompatible{BaseURL: srv.URL, Model: "m"}.Write(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("model: want the 302 as a refusal, got %v", err)
	}
}

func TestARefusalSaysOnlyTheStatusNotWhatTheServiceSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "redis_version:7.2.4 internal-secret-banner", http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := Sarvam{BaseURL: srv.URL, APIKey: "k"}.Transcribe(context.Background(), []byte("x"), "clip.ogg")
	if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "internal-secret-banner") {
		t.Errorf("speech to text: want the status and nothing the service said, got %v", err)
	}
	_, err = OpenAICompatible{BaseURL: srv.URL, Model: "m"}.Write(context.Background(), "s", "p")
	if err == nil || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "internal-secret-banner") {
		t.Errorf("model: want the status and nothing the service said, got %v", err)
	}
}

func TestAStreamedAnswerIsCutOffPastItsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := strings.Repeat("a", 32<<10)
		// 8 MB of answer, well past any real set of minutes.
		for i := 0; i < 256; i++ {
			if _, err := fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", chunk); err != nil {
				return
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	got, err := OpenAICompatible{BaseURL: srv.URL, Model: "m"}.Write(context.Background(), "s", "p")
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("want ErrTooLong, got %d bytes and %v", len(got), err)
	}
	if len(got) > maxAnswer {
		t.Errorf("kept %d bytes, more than the %d limit", len(got), maxAnswer)
	}
}
