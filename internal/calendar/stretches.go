package calendar

import (
	"sort"
	"time"
)

const (
	// stretchGap is how far apart two of somebody's sessions on one call can be
	// and still be one stretch on their calendar. A reload, a dropped connection
	// coming back, or moving from a laptop to a phone leaves a gap of seconds to a
	// minute or two, and none of that is somebody leaving. Ten minutes covers those
	// with room to spare, and is still well under the twenty minutes the calendar
	// draws even the shortest event as, so two blocks it does split are far enough
	// apart to be read as two. Somebody away longer than that was really away.
	stretchGap = 10 * time.Minute
	// fallbackSpan is the longest a call can be on somebody's calendar when there
	// is no session to say when they were there, only call_participants' first
	// join and last leave, which can be hours apart with them gone in between. A
	// meeting planned for longer keeps its planned length.
	fallbackSpan = 2 * time.Hour
)

// session is one connection to a call, as call_sessions records it.
type session struct {
	joined time.Time
	left   *time.Time
}

// stretch is a time somebody was really in a call.
type stretch struct {
	start, end time.Time
	// open says they are in it now, so end is now and keeps moving.
	open bool
}

// stretchesOf merges somebody's sessions on a call into the stretches they were
// there: devices at once count once, and a gap under stretchGap is not a leave.
// A session with no leave runs until now while the call is active, and otherwise
// until the call ended, as a leave that never arrived.
func stretchesOf(sessions []session, active bool, endedAt *time.Time, now time.Time) []stretch {
	spans := make([]stretch, 0, len(sessions))
	for _, s := range sessions {
		span := stretch{start: s.joined}
		switch {
		case s.left != nil:
			span.end = *s.left
		case active:
			span.end, span.open = now, true
		default:
			span.end = firstTime(now, endedAt)
		}
		if span.end.Before(span.start) {
			span.end = span.start
		}
		spans = append(spans, span)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })

	var out []stretch
	for _, span := range spans {
		if n := len(out); n > 0 && !span.start.After(out[n-1].end.Add(stretchGap)) {
			last := &out[n-1]
			if span.end.After(last.end) {
				last.end = span.end
			}
			last.open = last.open || span.open
			continue
		}
		out = append(out, span)
	}
	return out
}

// fallbackStretch is what can be said without sessions, from a call that has
// none for this person: an old call, or one whose webhooks never arrived. It
// runs from their first join to their last leave, but never longer than
// fallbackSpan, or the planned length of a meeting that had one, since what is
// between those two is not known and is likely not them being there.
func fallbackStretch(joined time.Time, leftAt *time.Time, active bool, endedAt *time.Time, planned time.Duration, now time.Time) stretch {
	s := stretch{start: joined, end: firstTime(now, leftAt, endedAt)}
	s.open = active && leftAt == nil
	limit := fallbackSpan
	if planned > limit {
		limit = planned
	}
	if s.end.Sub(s.start) > limit {
		s.end, s.open = s.start.Add(limit), false
	}
	if s.end.Before(s.start) {
		s.end = s.start
	}
	return s
}
