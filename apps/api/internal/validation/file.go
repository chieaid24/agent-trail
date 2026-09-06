package validation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	FileName = ".agent-trail/validation.yaml"

	MaxChecks      = 20
	MaxCommandArgs = 64
	MaxFileBytes   = 1 << 20

	DefaultTimeoutSeconds  = 300
	MaxTimeoutSeconds      = 1800
	MaxTotalTimeoutSeconds = 3600
)

// mirrors validation_results category check constraint
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

type Check struct {
	Name           string   `yaml:"name"`
	Category       string   `yaml:"category"`
	Command        []string `yaml:"command"`
	TimeoutSeconds int      `yaml:"timeout_seconds"`
}

func (c Check) EffectiveTimeoutSeconds() int {
	if c.TimeoutSeconds == 0 {
		return DefaultTimeoutSeconds
	}
	return c.TimeoutSeconds
}

type File struct {
	Version    int     `yaml:"version"`
	Validation []Check `yaml:"validation"`
}

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

// file is agent-editable input: confined to workspace, regular file only, size-bounded
func Load(workspaceDir string) (File, bool, error) {
	root, err := os.OpenRoot(workspaceDir)
	if err != nil {
		return File{}, false, fmt.Errorf("open workspace: %w", err)
	}
	defer root.Close()

	fi, err := root.Lstat(FileName)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("stat validation file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return File{}, true, errors.New("validation file must be a regular file")
	}
	if fi.Size() > MaxFileBytes {
		return File{}, true, fmt.Errorf("validation file exceeds %d bytes", MaxFileBytes)
	}

	vf, err := root.Open(FileName)
	if err != nil {
		return File{}, true, fmt.Errorf("read validation file: %w", err)
	}
	defer vf.Close()
	data, err := io.ReadAll(io.LimitReader(vf, MaxFileBytes+1))
	if err != nil {
		return File{}, true, fmt.Errorf("read validation file: %w", err)
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
			return fmt.Errorf("check %q: timeout_seconds must be between 0 and %d (0 applies the %ds default)",
				c.Name, MaxTimeoutSeconds, DefaultTimeoutSeconds)
		}
		total += c.EffectiveTimeoutSeconds()
	}
	if total > MaxTotalTimeoutSeconds {
		return fmt.Errorf("summed check timeouts %ds exceed the %ds budget",
			total, MaxTotalTimeoutSeconds)
	}
	return nil
}
