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

// gitBin is the git executable, resolved from PATH.
const gitBin = "git"

// CommandError is a failed git invocation. It carries the subcommand and the
// process stderr (credential-redacted) but never the full argument vector, so
// a clone URL bearing an access token cannot leak into logs or error strings.
type CommandError struct {
	Op       string // git subcommand, e.g. "clone", "worktree"
	ExitCode int
	Stderr   string
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s: exit %d", e.Op, e.ExitCode)
	}
	return fmt.Sprintf("git %s: exit %d: %s", e.Op, e.ExitCode, e.Stderr)
}

// runner invokes git with argument arrays only; it never spawns a shell, so no
// input is ever interpreted by one.
type runner struct{}

// run executes git args in dir (empty means the process working directory) and
// returns trimmed stdout. On a non-zero exit it returns a *CommandError with
// credential-redacted stderr.
func (r runner) run(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := r.runExit(ctx, dir, nil, args...)
	return out, err
}

// runExit is run for commands whose listed non-zero exit codes carry data
// rather than failure (merge-tree exits 1 on a conflicting merge): it returns
// stdout and the exit code when the exit is 0 or listed in okExits, and a
// *CommandError otherwise.
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
			// git not found, context cancelled, or the process could not start.
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

// hardenedEnv isolates git from host configuration and interactive prompts so
// runs are reproducible and a hostile repository cannot lean on ambient config
// or block on a credential prompt. PATH is preserved so git finds its helpers;
// LC_ALL=C keeps porcelain output parseable regardless of the host locale.
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

// subcommand returns the first non-flag argument, used only as an error label.
func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "command"
}

// credentialInURL matches the userinfo of a URL (e.g. an access token in a
// clone URL) so it can be stripped from error output.
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// redactSecrets removes URL userinfo (tokens, passwords) from git output before
// it reaches an error string or a log.
func redactSecrets(s string) string {
	return strings.TrimSpace(credentialInURL.ReplaceAllString(s, "$1***@"))
}
