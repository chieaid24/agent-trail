package reposettings

import (
	"encoding/json"
	"testing"
)

func TestParseDefaultsAndOverrides(t *testing.T) {
	for _, raw := range []string{"", "{}", `{"default_policy":""}`} {
		got, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if got != Defaults() {
			t.Fatalf("Parse(%q) = %+v, want defaults", raw, got)
		}
	}

	got, err := Parse([]byte(`{"default_policy":"restricted","validation_file":".ci/checks.yaml","max_attempts":7}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{DefaultPolicy: "restricted", ValidationFile: ".ci/checks.yaml", MaxAttempts: 7}
	if got != want {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
}

func TestParseRejectsOutOfBoundsAndMalformed(t *testing.T) {
	for _, raw := range []string{`{"max_attempts":0}`, `{"max_attempts":21}`, `{"max_attempts":-1}`, `{"max_attempts":"5"}`, `not json`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("Parse(%q) accepted", raw)
		}
	}
}

func TestSettingsRoundTripJSON(t *testing.T) {
	raw, err := json.Marshal(Settings{DefaultPolicy: "p", ValidationFile: "v", MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxAttempts != 3 || got.DefaultPolicy != "p" || got.ValidationFile != "v" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestValidateMaxAttemptsBounds(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, 5: true, 20: true, 21: false} {
		if err := ValidateMaxAttempts(n); (err == nil) != ok {
			t.Errorf("ValidateMaxAttempts(%d) err = %v, want ok=%v", n, err, ok)
		}
	}
}
