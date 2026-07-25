package github

import "strings"

// Command is the parsed /agent-trail command from a comment body.
type Command struct {
	// Known is true for the one supported command: "/agent-trail run".
	Known bool
	// Addressed is true when any line starts with "/agent-trail"; an
	// addressed-but-unknown command earns a usage reply instead of silence.
	Addressed bool
}

// commandUsage is the reply to an addressed comment that is not a supported
// command.
const commandUsage = "Unknown command. Supported: `/agent-trail run`."

// ParseCommand scans the comment body for an /agent-trail command line. The
// first line starting with "/agent-trail" decides; MVP syntax accepts
// exactly "run" with no arguments (docs/architecture/github-app.md).
func ParseCommand(body string) Command {
	for line := range strings.Lines(body) {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "/agent-trail" {
			continue
		}
		return Command{
			Known:     len(fields) == 2 && fields[1] == "run",
			Addressed: true,
		}
	}
	return Command{}
}
