package websocket

import (
	"encoding/json"
	"time"

	"github.com/teamup/teamup/pkg/protocol"
)

// What one live connection may send. A person typing, reading and sending
// stays far below these; a script on a stolen session does not, and without
// them it could make the server check permissions and fan out to a whole
// channel as fast as it can write.
const (
	// maxFrameBytes is the largest frame read. A message is at most 40,000
	// characters, which in JSON is well inside this.
	maxFrameBytes = 256 << 10
	// framesPerSecond is the steady rate, and frameBurst how far above it a
	// connection may go for a moment (a reconnect resubscribing, say).
	framesPerSecond = 20
	frameBurst      = 60
	// maxSocketsPerUser is how many connections one person may hold open on
	// one server: a phone, a laptop and a dozen tabs.
	maxSocketsPerUser = 20
	// typingEvery is how often "typing" from one person in one conversation
	// goes out to everybody in it. The app says it at most every few seconds.
	typingEvery = 2 * time.Second
)

// frameBucket lets framesPerSecond through on average with bursts up to
// frameBurst. Only the read loop uses it, so it needs no lock.
type frameBucket struct {
	tokens float64
	last   time.Time
	// refused counts frames refused since the last one let through.
	refused int
}

func (b *frameBucket) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = frameBurst
	} else {
		b.tokens += now.Sub(b.last).Seconds() * framesPerSecond
		if b.tokens > frameBurst {
			b.tokens = frameBurst
		}
	}
	b.last = now
	if b.tokens < 1 {
		b.refused++
		return false
	}
	b.tokens--
	b.refused = 0
	return true
}

// floods is a connection that has gone on sending flat out after being
// refused for a whole burst: it is not a person, so it is closed.
func (b *frameBucket) floods() bool { return b.refused > frameBurst }

// typingTooSoon says whether a "started typing" in this conversation came too
// soon after the last one passed on, with no "stopped" between. "Stopped
// typing" always goes through, so nobody is left shown typing.
func (c *Connection) typingTooSoon(msg *protocol.Message, now time.Time) bool {
	var p protocol.ChatTypingPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return false
	}
	if c.typingAt == nil {
		c.typingAt = map[string]time.Time{}
	}
	// The app says "started" once and "stopped" when the box empties, so a
	// start after a stop is a new one and must go out, or nobody ever sees
	// them typing again. Pairs sent flat out are the frame limit's to stop.
	if !p.IsTyping {
		delete(c.typingAt, p.ChannelID)
		return false
	}
	if last, ok := c.typingAt[p.ChannelID]; ok && now.Sub(last) < typingEvery {
		return true
	}
	if len(c.typingAt) > 1000 {
		c.typingAt = map[string]time.Time{}
	}
	c.typingAt[p.ChannelID] = now
	return false
}

// socketsOf is how many connections somebody has open on this server.
func (h *Hub) socketsOf(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.userConns[userID])
}
