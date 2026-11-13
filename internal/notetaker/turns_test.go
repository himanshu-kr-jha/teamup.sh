package notetaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSTT answers after a moment, failing as told, and records how many
// requests it had at once.
type fakeSTT struct {
	mu      sync.Mutex
	calls   int
	failFor int // the first failFor calls fail with err
	err     error
	now     atomic.Int32
	most    atomic.Int32
	hold    time.Duration
}

func (f *fakeSTT) Transcribe(ctx context.Context, _ []byte, _ string) (string, error) {
	n := f.now.Add(1)
	defer f.now.Add(-1)
	for {
		m := f.most.Load()
		if n <= m || f.most.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(f.hold)
	f.mu.Lock()
	f.calls++
	fail := f.calls <= f.failFor
	f.mu.Unlock()
	if fail {
		return "", f.err
	}
	return "hello", nil
}

func quickPauses(t *testing.T) {
	t.Helper()
	pause := sttPause
	sttPause = time.Millisecond
	t.Cleanup(func() { sttPause = pause })
}

// Speech to text saying it is busy no longer costs the sentence: the clip is
// sent again after a pause.
func TestABusySpeechServiceIsAskedAgain(t *testing.T) {
	quickPauses(t)
	s := &Service{transcribing: make(chan struct{}, 2)}
	stt := &fakeSTT{failFor: 2, err: &httpError{Host: "stt", Status: 429}}
	text, err := s.transcribeClip(context.Background(), stt, []byte("x"))
	if err != nil || text != "hello" {
		t.Fatalf("got %q, %v; want the clip transcribed on the third try", text, err)
	}
	if stt.calls != 3 {
		t.Errorf("asked %d times, want 3", stt.calls)
	}
}

// A refusal of the request itself, such as a wrong key, needs a person, so it
// is not sent again.
func TestARefusedClipIsNotSentAgain(t *testing.T) {
	quickPauses(t)
	s := &Service{transcribing: make(chan struct{}, 2)}
	stt := &fakeSTT{failFor: 5, err: &httpError{Host: "stt", Status: 401}}
	if _, err := s.transcribeClip(context.Background(), stt, []byte("x")); err == nil {
		t.Fatal("a refused clip was taken as transcribed")
	}
	if stt.calls != 1 {
		t.Errorf("asked %d times, want once", stt.calls)
	}
}

// It gives up in the end, and says why.
func TestAServiceThatStaysDownIsGivenUpOn(t *testing.T) {
	quickPauses(t)
	s := &Service{transcribing: make(chan struct{}, 2)}
	stt := &fakeSTT{failFor: 100, err: &httpError{Host: "stt", Status: 503}}
	_, err := s.transcribeClip(context.Background(), stt, []byte("x"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want the service's last answer", err)
	}
	if stt.calls != sttAttempts {
		t.Errorf("asked %d times, want %d", stt.calls, sttAttempts)
	}
}

// However many meetings are talking, one server sends speech to text no more
// than its share of clips at once; the rest wait their turn.
func TestManyMeetingsShareTheServersTurnsAtSpeechToText(t *testing.T) {
	s := &Service{transcribing: make(chan struct{}, 3)}
	stt := &fakeSTT{hold: 20 * time.Millisecond}
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.transcribeClip(context.Background(), stt, []byte("x")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := stt.most.Load(); got > 3 {
		t.Errorf("%d clips were sent at once, want at most 3", got)
	}
	if stt.calls != 30 {
		t.Errorf("%d of 30 clips were transcribed", stt.calls)
	}
}

// A meeting that ends while its clip waits for a turn stops waiting.
func TestWaitingForATurnEndsWithTheMeeting(t *testing.T) {
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if turn(ctx, slots) {
		t.Fatal("got a turn that was not free")
	}
}

func TestRetriesAreSpreadOut(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 50 {
		d := jitter(time.Second)
		if d < 750*time.Millisecond || d > 1250*time.Millisecond {
			t.Fatalf("jitter(1s) = %v, want between 0.75s and 1.25s", d)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d different waits in 50, so retries would still arrive together", len(seen))
	}
	if jitter(0) != 0 {
		t.Error("no pause should stay no pause")
	}
}
