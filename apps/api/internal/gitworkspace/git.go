package gitworkspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const gitBin = "git"

// carries subcommand + redacted stderr, never the argv, so a token-bearing clone url cannot leak
type CommandError struct {
	Op       string
	ExitCode int
	Stderr   string
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s: exit %d", e.Op, e.ExitCode)
	}
	return fmt.Sprintf("git %s: exit %d: %s", e.Op, e.ExitCode, e.Stderr)
}

type runner struct{}

func (r runner) run(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := r.runExit(ctx, dir, nil, args...)
	return out, err
}

func (runner) runExit(ctx context.Context, dir string, okExits []int, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, gitBin, args...)
	cmd.Dir = dir
	cmd.Env = hardenedEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		ce := &CommandError{Op: subcommand(args), Stderr: redactSecrets(stderr.String())}
		if exit, ok := err.(*exec.ExitError); ok {
			ce.ExitCode = exit.ExitCode()
		} else {
			ce.ExitCode = -1
			ce.Stderr = strings.TrimSpace(redactSecrets(err.Error()) + " " + ce.Stderr)
		}
		for _, code := range okExits {
			if ce.ExitCode == code {
				return strings.TrimRight(stdout.String(), "\n"), ce.ExitCode, nil
			}
		}
		return "", ce.ExitCode, ce
	}
	return strings.TrimRight(stdout.String(), "\n"), 0, nil
}

// isolate from host config + prompts; lc_all=c keeps porcelain output parseable
func hardenedEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"LC_ALL=C",
	}
}

func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "command"
}

// url userinfo (token in clone url) stripped from error output
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

func redactSecrets(s string) string {
	return strings.TrimSpace(credentialInURL.ReplaceAllString(s, "$1***@"))
}
