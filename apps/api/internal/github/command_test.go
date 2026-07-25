package github

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		known     bool
		addressed bool
	}{
		{"bare run", "/agent-trail run", true, true},
		{"run with whitespace", "  /agent-trail   run  ", true, true},
		{"run on later line", "please\n/agent-trail run\nthanks", true, true},
		{"unknown subcommand", "/agent-trail deploy", false, true},
		{"missing subcommand", "/agent-trail", false, true},
		{"extra arguments", "/agent-trail run --base main", false, true},
		{"not addressed", "run the agent please", false, false},
		{"mid-line mention", "use /agent-trail run here", false, false},
		{"empty body", "", false, false},
		{"prefixed word", "/agent-trailing run", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCommand(tc.body)
			if got.Known != tc.known || got.Addressed != tc.addressed {
				t.Fatalf("ParseCommand(%q) = %+v, want known=%v addressed=%v",
					tc.body, got, tc.known, tc.addressed)
			}
		})
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got := truncateUTF8("hello", 10); got != "hello" {
		t.Fatalf("short string changed: %q", got)
	}
	if got := truncateUTF8("hello", 3); got != "hel" {
		t.Fatalf("ascii truncation: %q", got)
	}
	// Multi-byte rune straddling the cut must be dropped whole.
	s := "aé" // 'é' is 2 bytes; cut at 2 lands mid-rune
	if got := truncateUTF8(s, 2); got != "a" {
		t.Fatalf("rune-splitting truncation: %q", got)
	}
}
