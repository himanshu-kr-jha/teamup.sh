package notetaker

import (
	"bytes"
	"testing"
	"time"

	"github.com/pion/rtp"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// talk sends d of 20 ms packets, the clock moving with them.
func talk(r *recorder, c *fakeClock, seq *uint16, d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += 20 * time.Millisecond {
		*seq++
		r.Packet(&rtp.Packet{Header: rtp.Header{SequenceNumber: *seq, Timestamp: uint32(*seq) * 960}, Payload: []byte{0xfc, 0xff, 0xfe}})
		c.advance(20 * time.Millisecond)
		r.Tick()
	}
}

func newTestRecorder() (*recorder, *fakeClock, *[]Clip, *uint16) {
	clock := &fakeClock{t: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)}
	var clips []Clip
	seq := uint16(0)
	r := newRecorder(Speaker{UserID: "u1", Name: "Ada"}, func(c Clip) { clips = append(clips, c) }, clock.now)
	return r, clock, &clips, &seq
}

func TestOnlySpeechIsKeptWithAMomentEitherSide(t *testing.T) {
	r, clock, clips, seq := newTestRecorder()

	talk(r, clock, seq, 5*time.Second) // background noise while quiet: not kept
	speechStart := clock.t
	r.Speaking(true)
	talk(r, clock, seq, 4*time.Second)
	r.Speaking(false)
	talk(r, clock, seq, 3*time.Second) // the tail, then quiet again

	if len(*clips) != 1 {
		t.Fatalf("one clip for one stretch of speech, got %d", len(*clips))
	}
	c := (*clips)[0]
	if c.Speaker.Name != "Ada" || !bytes.HasPrefix(c.Audio, []byte("OggS")) {
		t.Errorf("an Ogg clip of Ada: %q", c.Audio[:4])
	}
	if lead := speechStart.Sub(c.StartedAt); lead < 200*time.Millisecond || lead > 400*time.Millisecond {
		t.Errorf("starts a moment before the first word: %v early", lead)
	}
	if length := c.EndedAt.Sub(c.StartedAt); length < 4*time.Second || length > 5500*time.Millisecond {
		t.Errorf("as long as the speech and a short tail: %v", length)
	}
}

func TestALongStretchIsCutUnderTheThirtySecondLimit(t *testing.T) {
	r, clock, clips, seq := newTestRecorder()
	r.Speaking(true)
	talk(r, clock, seq, 60*time.Second)
	r.Speaking(false)
	talk(r, clock, seq, 2*time.Second)
	if len(*clips) < 3 {
		t.Fatalf("a minute of talking is three clips at least, got %d", len(*clips))
	}
	for _, c := range *clips {
		if d := c.EndedAt.Sub(c.StartedAt); d > 26*time.Second {
			t.Errorf("a clip of %v is over the limit", d)
		}
	}
}

func TestACoughIsNotSentAndAMutedMicrophoneEndsTheClip(t *testing.T) {
	r, clock, clips, seq := newTestRecorder()
	r.Speaking(true)
	talk(r, clock, seq, 200*time.Millisecond)
	r.Speaking(false)
	talk(r, clock, seq, 2*time.Second)
	if len(*clips) != 0 {
		t.Errorf("too short to be worth transcribing: %d", len(*clips))
	}

	r.Speaking(true)
	talk(r, clock, seq, 3*time.Second)
	// Muted mid-sentence: no packets, and still "speaking" for a moment.
	clock.advance(2 * time.Second)
	r.Tick()
	if len(*clips) != 1 {
		t.Errorf("packets stopping closes the clip: %d", len(*clips))
	}
}

func TestLeavingFlushesWhatWasBeingSaid(t *testing.T) {
	r, clock, clips, seq := newTestRecorder()
	r.Speaking(true)
	talk(r, clock, seq, 2*time.Second)
	r.Flush()
	if len(*clips) != 1 {
		t.Errorf("the last words are not lost: %d", len(*clips))
	}
}

// oggSeconds is how long an Ogg Opus file says it is: its last page's granule
// position, in 48 kHz samples, which is what speech-to-text measures.
func oggSeconds(t *testing.T, ogg []byte) float64 {
	t.Helper()
	at := bytes.LastIndex(ogg, []byte("OggS"))
	if at < 0 || len(ogg) < at+14 {
		t.Fatalf("not an Ogg file")
	}
	var granule uint64
	for i := 7; i >= 0; i-- {
		granule = granule<<8 | uint64(ogg[at+6+i])
	}
	return float64(granule) / 48000
}

// stream sends d of 20 ms packets whose RTP timestamps start at *ts.
func stream(r *recorder, c *fakeClock, ts *uint32, d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += 20 * time.Millisecond {
		*ts += 960
		r.Packet(&rtp.Packet{Header: rtp.Header{Timestamp: *ts}, Payload: []byte{0xfc, 0xff, 0xfe}})
		c.advance(20 * time.Millisecond)
		r.Tick()
	}
}

// What happened in the first real meeting: somebody muted, came back a minute
// later and spoke, and the clip carried audio from before the mute, so the file
// said it was over a minute long and Sarvam refused it.
func TestAClipAfterAMuteIsOnlyAsLongAsWhatWasSaid(t *testing.T) {
	r, clock, clips, _ := newTestRecorder()
	ts := uint32(1000)
	stream(r, clock, &ts, time.Second)
	clock.advance(70 * time.Second) // muted: no packets, the sender's clock runs on
	ts += 70 * 48000
	r.Speaking(true)
	stream(r, clock, &ts, 10*time.Second)
	r.Speaking(false)
	stream(r, clock, &ts, 2*time.Second)
	if len(*clips) != 1 {
		t.Fatalf("one clip, got %d", len(*clips))
	}
	if got := oggSeconds(t, (*clips)[0].Audio); got > 12 {
		t.Errorf("the file says %.1f s for 10 s of speech", got)
	}
}

func TestAJumpInTheAudioNeverMakesAFileOverTheLimit(t *testing.T) {
	r, clock, clips, _ := newTestRecorder()
	ts := uint32(1000)
	r.Speaking(true)
	stream(r, clock, &ts, 5*time.Second)
	ts += 40 * 48000 // the sender's timestamps leap forward mid-speech
	stream(r, clock, &ts, 5*time.Second)
	ts -= 3 * 960 // and a few late packets arrive
	stream(r, clock, &ts, 40*time.Millisecond)
	stream(r, clock, &ts, 30*time.Second)
	r.Speaking(false)
	stream(r, clock, &ts, 2*time.Second)
	if len(*clips) < 2 {
		t.Fatalf("got %d clips", len(*clips))
	}
	for i, c := range *clips {
		if got := oggSeconds(t, c.Audio); got > 26 {
			t.Errorf("clip %d says %.1f s, over the limit", i, got)
		}
	}
}
