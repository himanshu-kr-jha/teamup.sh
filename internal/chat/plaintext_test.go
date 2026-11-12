package chat

import "testing"

func TestAPushShowsAMessageWithoutItsFormattingMarks(t *testing.T) {
	cases := map[string]string{
		"*Hello* _team_, ~not~ this":             "Hello team, not this",
		"**bold** and ~~gone~~":                  "bold and gone",
		"*one* *two*":                            "one two",
		"see [the plan](https://x.com/p)":        "see the plan",
		"run `make check` now":                   "run make check now",
		"```\nx := 1\n```":                       "x := 1",
		"snake_case_name and 2*3*4":              "snake_case_name and 2*3*4",
		"a * b * c":                              "a * b * c",
		"https://example.com/a_b_c/ stays as is": "https://example.com/a_b_c/ stays as is",
	}
	for in, want := range cases {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}
