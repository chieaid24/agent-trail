package migrations

import (
	"strings"
	"testing"
)

// goose refuses unannotated migrations at run time; catch here instead
func TestMigrationsHaveGooseAnnotations(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, e := range entries {
		data, err := FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "+goose Up") {
			t.Errorf("%s: missing +goose Up annotation", e.Name())
		}
	}
}
