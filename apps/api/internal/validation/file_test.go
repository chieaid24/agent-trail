package validation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const docExample = `version: 1

validation:
  - name: format-check
    category: format
    command: ["npm", "run", "format:check"]
    timeout_seconds: 300

  - name: lint
    category: lint
    command: ["npm", "run", "lint"]
    timeout_seconds: 300

  - name: unit-tests
    category: unit_test
    command: ["npm", "test", "--", "--runInBand"]
    timeout_seconds: 600

  - name: build
    category: build
    command: ["npm", "run", "build"]
    timeout_seconds: 600
`

// TestParseDocExample: the example in docs/architecture/validation.md is a
// valid file.
func TestParseDocExample(t *testing.T) {
	f, err := Parse([]byte(docExample))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Validation) != 4 {
		t.Fatalf("checks = %d, want 4", len(f.Validation))
	}
	c := f.Validation[2]
	if c.Name != "unit-tests" || c.Category != "unit_test" ||
		c.TimeoutSeconds != 600 || len(c.Command) != 4 {
		t.Fatalf("check = %+v", c)
	}
}

func TestParseRejectsInvalidFiles(t *testing.T) {
	check := func(name, category, command string, timeout int) string {
		return fmt.Sprintf(
			"  - name: %s\n    category: %s\n    command: [%s]\n    timeout_seconds: %d\n",
			name, category, command, timeout)
	}
	manyChecks := "version: 1\nvalidation:\n"
	for i := 0; i <= MaxChecks; i++ {
		manyChecks += check(fmt.Sprintf("c%d", i), "custom", `"true"`, 10)
	}
	budgetChecks := "version: 1\nvalidation:\n"
	for i := 0; i < 3; i++ {
		budgetChecks += check(fmt.Sprintf("c%d", i), "custom", `"true"`, MaxTimeoutSeconds)
	}

	cases := map[string]string{
		"not yaml":       "{{",
		"unknown field":  "version: 1\nchecks: []\n",
		"bad version":    "version: 2\nvalidation:\n" + check("a", "custom", `"true"`, 10),
		"no checks":      "version: 1\nvalidation: []\n",
		"bad name":       "version: 1\nvalidation:\n" + check("-bad", "custom", `"true"`, 10),
		"duplicate name": "version: 1\nvalidation:\n" + check("a", "custom", `"true"`, 10) + check("a", "lint", `"true"`, 10),
		"bad category":   "version: 1\nvalidation:\n" + check("a", "tests", `"true"`, 10),
		"empty command":  "version: 1\nvalidation:\n  - name: a\n    category: custom\n    command: []\n",
		"shell string":   "version: 1\nvalidation:\n  - name: a\n    category: custom\n    command: npm test\n",
		"timeout cap":    "version: 1\nvalidation:\n" + check("a", "custom", `"true"`, MaxTimeoutSeconds+1),
		"too many":       manyChecks,
		"budget":         budgetChecks,
	}
	for name, body := range cases {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: Parse accepted:\n%s", name, body)
		}
	}
}

func TestEffectiveTimeoutDefaults(t *testing.T) {
	if got := (Check{}).EffectiveTimeoutSeconds(); got != DefaultTimeoutSeconds {
		t.Fatalf("default timeout = %d, want %d", got, DefaultTimeoutSeconds)
	}
	if got := (Check{TimeoutSeconds: 42}).EffectiveTimeoutSeconds(); got != 42 {
		t.Fatalf("timeout = %d, want 42", got)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := Load(dir); found || err != nil {
		t.Fatalf("Load(empty) = found %v, err %v", found, err)
	}

	path := filepath.Join(dir, FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(docExample), 0o644); err != nil {
		t.Fatal(err)
	}
	f, found, err := Load(dir)
	if !found || err != nil || len(f.Validation) != 4 {
		t.Fatalf("Load = %+v, found %v, err %v", f, found, err)
	}

	if err := os.WriteFile(path, []byte("version: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(dir); !found || err == nil {
		t.Fatalf("Load(invalid) = found %v, err %v; want found with error", found, err)
	}

	if err := os.WriteFile(path, []byte("version: 1\nvalidation:\n  - name: a\n    category: custom\n    command: [\"true\", \""+
		strings.Repeat("x", MaxFileBytes)+"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Fatal("Load accepted an oversized file")
	}
}

// TestLoadRejectsNonRegularFiles: the validation file is agent-editable
// input - a symlink (wherever it points) is an invalid file, never a
// readable one.
func TestLoadRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agent-trail"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.yaml")
	if err := os.WriteFile(target, []byte(docExample), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Load(dir); !found || err == nil {
		t.Fatalf("Load(symlink) = found %v, err %v; want found with error", found, err)
	}
}
