// Package validation implements trusted platform validation
// (docs/architecture/validation.md): parsing the repository validation
// file, running its checks in the attempt workspace after editing ends,
// and storing the results. Platform-run results carry
// trusted_execution=true; agent-reported checks never do, and no agent
// output can change a recorded exit code.
package validation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// FileName is the repository validation file, relative to the
	// workspace root.
	FileName = ".agent-trail/validation.yaml"

	// MaxChecks bounds the command count per file.
	MaxChecks = 20
	// MaxCommandArgs bounds one check's argument-array length.
	MaxCommandArgs = 64
	// MaxFileBytes bounds the validation file size.
	MaxFileBytes = 1 << 20

	// DefaultTimeoutSeconds applies when a check omits timeout_seconds.
	DefaultTimeoutSeconds = 300
	// MaxTimeoutSeconds caps one check's timeout.
	MaxTimeoutSeconds = 1800
	// MaxTotalTimeoutSeconds caps the file's summed effective timeouts,
	// bounding the whole trusted-validation phase.
	MaxTotalTimeoutSeconds = 3600
)

// Categories mirrors the validation_results category CHECK constraint
// (docs/architecture/data-model.md).
var Categories = map[string]bool{
	"unit_test":        true,
	"integration_test": true,
	"lint":             true,
	"format":           true,
	"typecheck":        true,
	"security":         true,
	"dependency":       true,
	"migration":        true,
	"build":            true,
	"custom":           true,
}

var checkNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,99}$`)

// Check is one configured validation command.
type Check struct {
	Name           string   `yaml:"name"`
	Category       string   `yaml:"category"`
	Command        []string `yaml:"command"`
	TimeoutSeconds int      `yaml:"timeout_seconds"`
}

// EffectiveTimeoutSeconds returns the check timeout with the default
// applied.
func (c Check) EffectiveTimeoutSeconds() int {
	if c.TimeoutSeconds == 0 {
		return DefaultTimeoutSeconds
	}
	return c.TimeoutSeconds
}

// File is the parsed repository validation file.
type File struct {
	Version    int     `yaml:"version"`
	Validation []Check `yaml:"validation"`
}

// Parse strictly decodes and validates a repository validation file.
func Parse(data []byte) (File, error) {
	if len(data) > MaxFileBytes {
		return File{}, fmt.Errorf("validation file exceeds %d bytes", MaxFileBytes)
	}
	var f File
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parse validation file: %w", err)
	}
	if err := f.validate(); err != nil {
		return File{}, err
	}
	return f, nil
}

// Load reads and parses the validation file under workspaceDir. The second
// return is false when the repository has no validation file; a present but
// invalid file is an error.
func Load(workspaceDir string) (File, bool, error) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("read validation file: %w", err)
	}
	f, err := Parse(data)
	if err != nil {
		return File{}, true, err
	}
	return f, true, nil
}

func (f File) validate() error {
	if f.Version != 1 {
		return fmt.Errorf("unsupported validation file version %d (want 1)", f.Version)
	}
	if len(f.Validation) == 0 {
		return errors.New("validation file declares no checks")
	}
	if len(f.Validation) > MaxChecks {
		return fmt.Errorf("validation file declares %d checks (limit %d)",
			len(f.Validation), MaxChecks)
	}
	seen := make(map[string]bool, len(f.Validation))
	total := 0
	for i, c := range f.Validation {
		if !checkNameRe.MatchString(c.Name) {
			return fmt.Errorf("check %d: name %q must match %s",
				i, c.Name, checkNameRe.String())
		}
		if seen[c.Name] {
			return fmt.Errorf("duplicate check name %q", c.Name)
		}
		seen[c.Name] = true
		if !Categories[c.Category] {
			return fmt.Errorf("check %q: unknown category %q", c.Name, c.Category)
		}
		if len(c.Command) == 0 || c.Command[0] == "" {
			return fmt.Errorf("check %q: command must be a non-empty argument array", c.Name)
		}
		if len(c.Command) > MaxCommandArgs {
			return fmt.Errorf("check %q: command has %d arguments (limit %d)",
				c.Name, len(c.Command), MaxCommandArgs)
		}
		if c.TimeoutSeconds < 0 || c.TimeoutSeconds > MaxTimeoutSeconds {
			return fmt.Errorf("check %q: timeout_seconds must be between 1 and %d",
				c.Name, MaxTimeoutSeconds)
		}
		total += c.EffectiveTimeoutSeconds()
	}
	if total > MaxTotalTimeoutSeconds {
		return fmt.Errorf("summed check timeouts %ds exceed the %ds budget",
			total, MaxTotalTimeoutSeconds)
	}
	return nil
}
