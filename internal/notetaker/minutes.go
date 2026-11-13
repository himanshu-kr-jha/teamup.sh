package notetaker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Segment is one stretch of one person speaking.
type Segment struct {
	Speaker   string    `json:"speaker"`
	StartedAt time.Time `json:"startedAt"`
	Text      string    `json:"text"`
}

// ActionItem is something somebody is to do.
type ActionItem struct {
	Owner string `json:"owner"`
	Task  string `json:"task"`
	Due   string `json:"due,omitempty"`
}

// Minutes are what everybody who was there receives.
type Minutes struct {
	Title         string       `json:"title"`
	Date          time.Time    `json:"date"`
	Attendees     []string     `json:"attendees"`
	Summary       string       `json:"summary"`
	Decisions     []string     `json:"decisions"`
	ActionItems   []ActionItem `json:"actionItems"`
	OpenQuestions []string     `json:"openQuestions"`
}

// maxTranscriptChars keeps a very long meeting inside what a model reads at
// once. The start and the end are kept, which is where decisions and actions
// usually are; the middle is cut, and the minutes say so.
const maxTranscriptChars = 120_000

const systemPrompt = `You write the minutes of a work meeting from its transcript.
Be faithful: include only what was said, never invent decisions, names or dates.
Write in English, plainly, in short sentences.
Answer with JSON only, no other text, in exactly this shape:
{"summary": "three to six sentences", "decisions": ["..."], "actionItems": [{"owner": "a person's name, or \"Unassigned\"", "task": "...", "due": "only if a date or time was said"}], "openQuestions": ["..."]}
Use empty lists where there is nothing to put.`

// Transcript is the meeting as lines of "[HH:MM] Name: text", in the meeting's
// own time zone.
func Transcript(segments []Segment, loc *time.Location) string {
	var b strings.Builder
	for _, s := range segments {
		fmt.Fprintf(&b, "[%s] %s: %s\n", s.StartedAt.In(loc).Format("15:04"), s.Speaker, s.Text)
	}
	return b.String()
}

func shorten(transcript string) string {
	if len(transcript) <= maxTranscriptChars {
		return transcript
	}
	half := maxTranscriptChars / 2
	return transcript[:half] + "\n[... the middle of the meeting is left out for length ...]\n" + transcript[len(transcript)-half:]
}

// composeTimeout is how long writing one meeting's minutes may take: up to
// three answers from a model that may think for minutes before each.
const composeTimeout = 30 * time.Minute

// attempts is how many times the model is asked before the minutes are given
// up on. A model answers differently each time, and one that wandered off or
// ran out of room usually answers properly when asked again.
const attempts = 3

// again is added to the question when the last answer could not be used.
const again = "\n\nYour last answer could not be used: it was not the JSON object asked for, or it was cut off. Answer again with that JSON object and nothing else, and keep any thinking short."

// UnusableAnswer is an answer that held no minutes, with its start, so the log
// can show what the model said instead.
type UnusableAnswer struct {
	Err    error
	Sample string
}

func (u *UnusableAnswer) Error() string { return u.Err.Error() }
func (u *UnusableAnswer) Unwrap() error { return u.Err }

// retryPause is how long to wait before asking a service that was busy, slow
// or out of reach again, give or take a quarter at random.
var retryPause = 30 * time.Second

// Write asks the model for the minutes and reads its answer, asking again
// when the answer could not be used (with more room when it ran out of it),
// and, after a pause, when the service was busy, failing, out of reach, or
// stopped part way. A request the service refused outright (a wrong key, a
// model it does not have) is not asked again: that needs a person.
func Write(ctx context.Context, w Writer, title string, date time.Time, attendees []string, segments []Segment, loc *time.Location) (Minutes, error) {
	prompt := fmt.Sprintf("Meeting: %s\nDate: %s\nAttendees: %s\n\nTranscript:\n%s",
		title, date.In(loc).Format("Monday 2 January 2006, 15:04 MST"), strings.Join(attendees, ", "), shorten(Transcript(segments, loc)))
	var last error
	for i := 0; i < attempts; i++ {
		question := prompt
		if i > 0 {
			question += again
		}
		answer, err := w.Write(ctx, systemPrompt, question)
		switch {
		case errors.Is(err, ErrNoRoom):
			if r, ok := w.(roomier); ok {
				w = r.withMoreRoom()
			}
			last = &UnusableAnswer{Err: err, Sample: sample(answer)}
			continue
		case errors.Is(err, ErrNoAnswer):
			last = err
			continue
		case errors.Is(err, ErrUnavailable), errors.Is(err, ErrStalled):
			last = err
			if i+1 < attempts {
				// With some randomness, so meetings turned away together do not
				// all come back in the same second and be turned away again.
				if !sleep(ctx, jitter(retryPause)) {
					return Minutes{}, err
				}
			}
			continue
		case err != nil:
			return Minutes{}, err
		}
		m, err := parseMinutes(answer)
		if err != nil {
			last = &UnusableAnswer{Err: err, Sample: sample(answer)}
			continue
		}
		m.Title, m.Date, m.Attendees = title, date, attendees
		return m, nil
	}
	return Minutes{}, last
}

// sample is the start of an answer, short enough for a log line.
func sample(answer string) string {
	const most = 600
	runes := []rune(strings.TrimSpace(answer))
	if len(runes) > most {
		return string(runes[:most]) + "\u2026"
	}
	return string(runes)
}

// parseMinutes reads the model's JSON, allowing for the code fence or the
// sentence some models put around it.
func parseMinutes(answer string) (Minutes, error) {
	// A reasoning model may think aloud first, braces and all.
	if i := strings.LastIndex(answer, "</think>"); i >= 0 {
		answer = answer[i+len("</think>"):]
	}
	start := strings.Index(answer, "{")
	end := strings.LastIndex(answer, "}")
	if start < 0 || end <= start {
		return Minutes{}, fmt.Errorf("the model did not answer with minutes")
	}
	var m Minutes
	if err := json.Unmarshal([]byte(answer[start:end+1]), &m); err != nil {
		return Minutes{}, fmt.Errorf("the model's minutes could not be read: %w", err)
	}
	if strings.TrimSpace(m.Summary) == "" {
		return Minutes{}, fmt.Errorf("the model's minutes had no summary")
	}
	if m.Decisions == nil {
		m.Decisions = []string{}
	}
	if m.ActionItems == nil {
		m.ActionItems = []ActionItem{}
	}
	if m.OpenQuestions == nil {
		m.OpenQuestions = []string{}
	}
	return m, nil
}

// Text is the minutes as plain text, for email.
func (m Minutes) Text(loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Minutes: %s\n%s\n\nAttendees: %s\n\n", m.Title, m.Date.In(loc).Format("Monday 2 January 2006, 15:04"), strings.Join(m.Attendees, ", "))
	fmt.Fprintf(&b, "Summary\n%s\n", m.Summary)
	list := func(heading string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", heading)
		for _, it := range items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	list("Decisions", m.Decisions)
	if len(m.ActionItems) > 0 {
		b.WriteString("\nAction items\n")
		for _, a := range m.ActionItems {
			due := ""
			if a.Due != "" {
				due = " (by " + a.Due + ")"
			}
			fmt.Fprintf(&b, "- %s: %s%s\n", a.Owner, a.Task, due)
		}
	}
	list("Open questions", m.OpenQuestions)
	return b.String()
}
