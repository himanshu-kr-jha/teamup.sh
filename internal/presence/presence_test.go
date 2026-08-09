package presence

import "testing"

// What somebody chose, corrected by what is actually true.
func TestEffectiveStatus(t *testing.T) {
	cases := []struct {
		name      string
		chosen    string
		connected bool
		inCall    bool
		want      string
	}{
		{"connected and nothing said", "available", true, false, "available"},
		// A colleague only needs to know they cannot talk, not why.
		{"in a call reads as busy", "available", true, true, "busy"},
		{"in a call reads as busy even if they said busy", "busy", true, true, "busy"},
		{"away is respected while connected", "away", true, false, "away"},
		{"busy is respected while connected", "busy", true, false, "busy"},
		// Not connected and away amount to the same thing: not right now.
		{"not connected reads as away", "available", false, false, "away"},
		{"not connected stays away whatever they chose", "busy", false, false, "away"},
		// Leave is a statement about the days ahead, so it holds even when
		// they are not here — which is exactly when it matters.
		{"on leave shows while away from the keyboard", "on_leave", false, false, "on_leave"},
		{"on leave outranks being connected", "on_leave", true, false, "on_leave"},
		{"on leave outranks being in a call", "on_leave", true, true, "on_leave"},
		{"nothing chosen defaults to available", "", true, false, "available"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Effective(Availability{Status: c.chosen}, c.connected, c.inCall)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestOnlyDeliberateStatusesCanBeChosen(t *testing.T) {
	for _, status := range []string{"available", "away", "busy", "on_leave"} {
		if !choosableAvailability[status] {
			t.Errorf("%q should be choosable", status)
		}
	}
	// These are observed, not chosen: claiming them would let somebody appear
	// to be in a call they are not in.
	for _, status := range []string{"in_call", "offline", "", "anything"} {
		if choosableAvailability[status] {
			t.Errorf("%q should not be choosable", status)
		}
	}
}
