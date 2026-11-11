package notetaker

import (
	"testing"

	"github.com/teamup/teamup/internal/calls"
)

// The calls package leaves the note-taker out when it counts who is in a
// meeting, by this prefix; the two must not drift apart.
func TestTheNoteTakersIdentityIsTheOneCallsLeavesOut(t *testing.T) {
	if IdentityPrefix != calls.NoteTakerIdentityPrefix {
		t.Fatalf("notetaker joins as %q but calls leaves out %q", IdentityPrefix, calls.NoteTakerIdentityPrefix)
	}
}
