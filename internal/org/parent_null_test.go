package org

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// null and "" both have to mean "lift this team to the top level". null is the
// obvious JSON spelling and was silently ignored, which made the three-state
// parent unreachable over HTTP for the one case it was designed for.
func TestParentFromJSONDistinguishesNullFromAnID(t *testing.T) {
	id := uuid.New()

	for _, tc := range []struct {
		name string
		raw  string
		want *uuid.UUID
	}{
		{"null lifts to the top level", `null`, nil},
		{"empty string lifts to the top level", `""`, nil},
		{"an id files it under that team", `"` + id.String() + `"`, &id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parentFromJSON(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("parentFromJSON(%s): %v", tc.raw, err)
			}
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("got %s, want nil (top level)", got)
			case tc.want != nil && got == nil:
				t.Errorf("got nil, want %s", tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	if _, err := parentFromJSON(json.RawMessage(`"not-a-uuid"`)); err == nil {
		t.Error("a malformed id must be refused, not treated as top level")
	}
}
