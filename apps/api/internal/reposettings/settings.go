// repositories.settings_json contract; the dashboard read model and the webhook processor share it
package reposettings

import (
	"encoding/json"
	"fmt"
)

const (
	DefaultPolicy         = "platform default"
	DefaultValidationFile = ".agent-trail/validation.yaml"
	DefaultMaxAttempts    = 5
	MinMaxAttempts        = 1
	MaxMaxAttempts        = 20
)

type Settings struct {
	DefaultPolicy  string `json:"default_policy"`
	ValidationFile string `json:"validation_file"`
	// revision limit: a revise command on a task already holding this many attempts is refused
	MaxAttempts int `json:"max_attempts"`
}

func Defaults() Settings {
	return Settings{
		DefaultPolicy:  DefaultPolicy,
		ValidationFile: DefaultValidationFile,
		MaxAttempts:    DefaultMaxAttempts,
	}
}

// absent fields take defaults; a stored max_attempts outside bounds (zero included) is a corrupt row, never clamped
func Parse(raw []byte) (Settings, error) {
	settings := Defaults()
	if len(raw) == 0 {
		return settings, nil
	}
	var stored struct {
		DefaultPolicy  string `json:"default_policy"`
		ValidationFile string `json:"validation_file"`
		MaxAttempts    *int   `json:"max_attempts"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return Settings{}, err
	}
	if stored.DefaultPolicy != "" {
		settings.DefaultPolicy = stored.DefaultPolicy
	}
	if stored.ValidationFile != "" {
		settings.ValidationFile = stored.ValidationFile
	}
	if stored.MaxAttempts != nil {
		settings.MaxAttempts = *stored.MaxAttempts
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (s Settings) Validate() error {
	return ValidateMaxAttempts(s.MaxAttempts)
}

func ValidateMaxAttempts(n int) error {
	if n < MinMaxAttempts || n > MaxMaxAttempts {
		return fmt.Errorf("max_attempts must be between %d and %d", MinMaxAttempts, MaxMaxAttempts)
	}
	return nil
}
