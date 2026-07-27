package agent

import "testing"

func TestNewSelectsProvider(t *testing.T) {
	cases := []struct {
		provider string
		wantName string
	}{
		{"", "fake"},
		{"fake", "fake"},
		{"claude-code", ClaudeProvider},
	}
	for _, tc := range cases {
		ad, err := New(Options{Provider: tc.provider})
		if err != nil {
			t.Fatalf("New(%q): %v", tc.provider, err)
		}
		if ad.Name() != tc.wantName {
			t.Errorf("New(%q).Name() = %q, want %q", tc.provider, ad.Name(), tc.wantName)
		}
	}

	if _, err := New(Options{Provider: "bogus"}); err == nil {
		t.Fatal("New with unknown provider returned no error")
	}
}
