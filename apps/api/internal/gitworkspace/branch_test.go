package gitworkspace

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeBranch(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr error
	}{
		{"simple", "Fix Auth Bug", "agent-trail/fix-auth-bug", nil},
		{"already slug", "add-webhook", "agent-trail/add-webhook", nil},
		{"collapses separators", "a  --  b__c/d", "agent-trail/a-b-c-d", nil},
		{"trims edges", "--hello--", "agent-trail/hello", nil},
		{"strips traversal", "../../etc/passwd", "agent-trail/etc-passwd", nil},
		{"strips ref metacharacters", "feat~1^2:x?*[", "agent-trail/feat-1-2-x", nil},
		{"drops unicode", "caf\u00e9 \u2013 menu", "agent-trail/caf-menu", nil},
		{"empty after strip", "///", "", ErrEmptyBranch},
		{"only separators", "  --  ", "", ErrEmptyBranch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SanitizeBranch(tc.raw)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("SanitizeBranch(%q) err = %v, want %v", tc.raw, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("SanitizeBranch(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			if tc.wantErr == nil && !validBranch(got) {
				t.Fatalf("SanitizeBranch(%q) = %q, which validBranch rejects", tc.raw, got)
			}
		})
	}
}

func TestSanitizeBranchLengthCap(t *testing.T) {
	got, err := SanitizeBranch(strings.Repeat("x", 500))
	if err != nil {
		t.Fatalf("SanitizeBranch(long) err = %v", err)
	}
	slug := strings.TrimPrefix(got, BranchPrefix)
	if len(slug) != maxSlug {
		t.Fatalf("slug length = %d, want %d", len(slug), maxSlug)
	}
	if !validBranch(got) {
		t.Fatalf("capped branch %q is invalid", got)
	}
}

func TestValidBranch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"under prefix", "agent-trail/fix-1", true},
		{"prefix only", "agent-trail/", false},
		{"no prefix", "fix-1", false},
		{"protected branch", "main", false},
		{"traversal", "agent-trail/../main", false},
		{"uppercase", "agent-trail/Fix", false},
		{"space", "agent-trail/fix bug", false},
		{"leading dash", "agent-trail/-x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validBranch(tc.in); got != tc.want {
				t.Fatalf("validBranch(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidComponent(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"11111111-2222-3333-4444-555555555555", true},
		{"attempt_1.tmp", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../escape", false},
		{"a/b", false},
		{`a\b`, false},
		{"-leading", false},
		{"has space", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := validComponent(tc.in); got != tc.want {
				t.Fatalf("validComponent(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidSHA(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef01234567", true},
		{"0123456789ABCDEF0123456789abcdef01234567", false}, // uppercase
		{"0123456", false}, // short
		{"0123456789abcdef0123456789abcdef0123456g", false}, // non-hex
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := validSHA(tc.in); got != tc.want {
				t.Fatalf("validSHA(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildTrailers(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		got, err := buildTrailers(CommitParams{
			TaskID: "t-1", Provider: "fake", Model: "m-1", RequestedBy: "octocat",
		})
		if err != nil {
			t.Fatalf("buildTrailers err = %v", err)
		}
		want := "Agent-Trail-Task-ID: t-1\n" +
			"Agent-Trail-Agent-Provider: fake\n" +
			"Agent-Trail-Agent-Model: m-1\n" +
			"Agent-Trail-Requested-By: octocat"
		if got != want {
			t.Fatalf("buildTrailers =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("skips empty fields", func(t *testing.T) {
		got, err := buildTrailers(CommitParams{TaskID: "t-1"})
		if err != nil {
			t.Fatalf("buildTrailers err = %v", err)
		}
		if got != "Agent-Trail-Task-ID: t-1" {
			t.Fatalf("buildTrailers = %q", got)
		}
	})
	t.Run("rejects newline injection", func(t *testing.T) {
		_, err := buildTrailers(CommitParams{TaskID: "t-1\nAgent-Trail-Requested-By: attacker"})
		if err == nil {
			t.Fatal("buildTrailers accepted a newline in a trailer value")
		}
	})
	t.Run("requires at least one field", func(t *testing.T) {
		if _, err := buildTrailers(CommitParams{}); err == nil {
			t.Fatal("buildTrailers accepted an empty trailer set")
		}
	})
}

func TestParseNumstat(t *testing.T) {
	out := "3\t1\tmain.go\n0\t5\tremoved.go\n-\t-\timage.png\n"
	got, err := parseNumstat(out)
	if err != nil {
		t.Fatalf("parseNumstat err = %v", err)
	}
	want := Stats{FilesChanged: 3, Insertions: 3, Deletions: 6}
	if got != want {
		t.Fatalf("parseNumstat = %+v, want %+v", got, want)
	}
	if empty, err := parseNumstat(""); err != nil || empty != (Stats{}) {
		t.Fatalf("parseNumstat(empty) = %+v, err %v", empty, err)
	}
}

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{
			"fatal: could not read from https://x-access-token:ghs_secret@github.com/o/r.git",
			"fatal: could not read from https://***@github.com/o/r.git",
		},
		{"remote: Permission denied", "remote: Permission denied"},
	}
	for _, tc := range cases {
		if got := redactSecrets(tc.in); got != tc.want {
			t.Fatalf("redactSecrets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
