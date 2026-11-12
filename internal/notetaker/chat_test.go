package notetaker

import (
	"testing"
	"time"
)

func TestTheChatIsWovenIntoWhatWasSaidByTime(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 12, 10, m, 0, 0, time.UTC) }
	spoken := []Segment{{Speaker: "Asha", StartedAt: at(0), Text: "Let us start"}, {Speaker: "Ben", StartedAt: at(5), Text: "The numbers are in"}}
	chat := []Segment{{Speaker: "Ben (in the chat)", StartedAt: at(5), Text: "https://example.test/sheet"}, {Speaker: "Asha (in the chat)", StartedAt: at(2), Text: "Agenda in the doc"}}
	chat[0], chat[1] = chat[1], chat[0]

	got := withChat(spoken, chat)
	want := []string{"Let us start", "Agenda in the doc", "The numbers are in", "https://example.test/sheet"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines", len(got))
	}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("line %d is %q, want %q", i, got[i].Text, w)
		}
	}
	if out := withChat(spoken, nil); len(out) != 2 {
		t.Errorf("no chat leaves the transcript as it was: %d", len(out))
	}
}
