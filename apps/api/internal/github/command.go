package github

import "strings"

type Command struct {
	Known bool
	// addressed-but-unknown earns a usage reply instead of silence
	Addressed bool
}

const commandUsage = "Unknown command. Supported: `/agent-trail run`."

// first line starting with "/agent-trail" decides; grammar accepts exactly "run", no args
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
