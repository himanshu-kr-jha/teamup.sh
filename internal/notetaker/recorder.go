package notetaker

// One person's microphone, cut into clips of what they said.
//
// Only speech is kept. LiveKit says who is speaking, and a clip opens when they
// start, carries a moment from just before (so the first word is not clipped),
// and closes a moment after they stop. A clip never runs past MaxClip, because
// the speech-to-text API takes 30 seconds at most. Silence is never sent, which
// is most of a meeting, and what speech-to-text charges by.
//
// The audio stays as it arrives, Opus, written into an Ogg container: nothing is
// decoded or re-encoded, so this needs no audio library.

import (
	"bytes"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

const (
	// MaxClip keeps every clip inside the 30-second limit.
	MaxClip = 25 * time.Second
	// tail is how long a clip stays open after somebody stops, for the end of a
	// sentence and a short pause between two.
	tail = 900 * time.Millisecond
	// gap closes a clip whose packets stopped, such as a microphone muted mid-word.
	gap = 1500 * time.Millisecond
	// minSpeech is the least speaking a clip needs before it is sent, counted
	// while they were speaking, not the moments either side: a cough is not a
	// contribution.
	minSpeech = 500 * time.Millisecond
	// prerollPackets is about 300 ms of 20 ms Opus packets.
	prerollPackets = 15
)

// Speaker is whose microphone it is.
type Speaker struct {
	UserID string
	Name   string
}

// Clip is a stretch of somebody's speech, ready to transcribe.
type Clip struct {
	Speaker   Speaker
	StartedAt time.Time
	EndedAt   time.Time
	Audio     []byte
}

type recorder struct {
	mu      sync.Mutex
	speaker Speaker
	emit    func(Clip)
	now     func() time.Time

	speaking   bool
	quietSince time.Time

	preroll []*rtp.Packet
	buf     *bytes.Buffer
	w       *oggwriter.OggWriter
	started time.Time
	last    time.Time
	// spoken counts the packets written while they were speaking.
	spoken int
}

func newRecorder(speaker Speaker, emit func(Clip), now func() time.Time) *recorder {
	return &recorder{speaker: speaker, emit: emit, now: now}
}

// Packet takes one RTP packet of the person's Opus audio.
func (r *recorder) Packet(p *rtp.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.w == nil {
		if !r.speaking {
			r.preroll = append(r.preroll, p)
			if len(r.preroll) > prerollPackets {
				r.preroll = r.preroll[1:]
			}
			return
		}
		r.open(now)
		if r.w == nil {
			return
		}
	}
	_ = r.w.WriteRTP(p)
	r.last = now
	if r.speaking {
		r.spoken++
	}
	if now.Sub(r.started) >= MaxClip {
		r.close(now)
	}
}

// Speaking is what LiveKit says about whether they are talking.
func (r *recorder) Speaking(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.speaking && !on {
		r.quietSince = r.now()
	}
	r.speaking = on
}

// Tick closes a clip once they have been quiet long enough. Called often.
func (r *recorder) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return
	}
	now := r.now()
	if (!r.speaking && now.Sub(r.quietSince) >= tail) || now.Sub(r.last) >= gap {
		r.close(now)
	}
}

// Flush closes whatever is open, at the end of the meeting or when they leave.
func (r *recorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w != nil {
		r.close(r.now())
	}
}

func (r *recorder) open(now time.Time) {
	r.buf = &bytes.Buffer{}
	w, err := oggwriter.NewWith(r.buf, 48000, 2)
	if err != nil {
		r.buf = nil
		return
	}
	r.w = w
	r.started = now.Add(-time.Duration(len(r.preroll)) * 20 * time.Millisecond)
	for _, p := range r.preroll {
		_ = r.w.WriteRTP(p)
	}
	r.preroll = nil
	r.last = now
	r.spoken = 0
}

func (r *recorder) close(now time.Time) {
	_ = r.w.Close()
	clip := Clip{Speaker: r.speaker, StartedAt: r.started, EndedAt: r.last, Audio: r.buf.Bytes()}
	r.w, r.buf = nil, nil
	if time.Duration(r.spoken)*20*time.Millisecond >= minSpeech {
		r.emit(clip)
	}
}
