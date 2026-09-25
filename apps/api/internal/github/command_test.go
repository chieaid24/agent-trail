package github

import (
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		known     bool
		addressed bool
		verb      Verb
	}{
		{"bare run", "/agent-trail run", true, true, VerbRun},
		{"run with whitespace", "  /agent-trail   run  ", true, true, VerbRun},
		{"run on later line", "please\n/agent-trail run\nthanks", true, true, VerbRun},
		{"bare revise", "/agent-trail revise", true, true, VerbRevise},
		{"revise with feedback below", "/agent-trail revise\nplease rename the helper", true, true, VerbRevise},
		{"revise with arguments", "/agent-trail revise now", false, true, ""},
		{"unknown subcommand", "/agent-trail deploy", false, true, ""},
		{"missing subcommand", "/agent-trail", false, true, ""},
		{"extra arguments", "/agent-trail run --base main", false, true, ""},
		{"not addressed", "run the agent please", false, false, ""},
		{"mid-line mention", "use /agent-trail run here", false, false, ""},
		{"empty body", "", false, false, ""},
		{"prefixed word", "/agent-trailing run", false, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCommand(tc.body)
			if got.Known != tc.known || got.Addressed != tc.addressed || got.Verb != tc.verb {
				t.Fatalf("ParseCommand(%q) = %+v, want known=%v addressed=%v verb=%q",
					tc.body, got, tc.known, tc.addressed, tc.verb)
			}
		})
	}
	if !strings.Contains(commandUsage, "run") || !strings.Contains(commandUsage, "revise") {
		t.Fatalf("usage must list both commands: %q", commandUsage)
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got := truncateUTF8("hello", 10); got != "hello" {
		t.Fatalf("short string changed: %q", got)
	}
	if got := truncateUTF8("hello", 3); got != "hel" {
		t.Fatalf("ascii truncation: %q", got)
	}
	s := "aé" // 2-byte rune; cut at 2 lands mid-rune, must drop whole
	if got := truncateUTF8(s, 2); got != "a" {
		t.Fatalf("rune-splitting truncation: %q", got)
	}
}
