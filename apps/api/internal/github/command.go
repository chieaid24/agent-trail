package github

import "strings"

type Verb string

const (
	VerbRun    Verb = "run"
	VerbRevise Verb = "revise"
)

type Command struct {
	Known bool
	// addressed-but-unknown earns a usage reply instead of silence
	Addressed bool
	Verb      Verb // set only when Known
}

const commandUsage = "Unknown command. Supported: `/agent-trail run` on an issue, " +
	"`/agent-trail revise` on an Agent Trail pull request."

// first line starting with "/agent-trail" decides; grammar accepts exactly one verb, no args
func ParseCommand(body string) Command {
	for line := range strings.Lines(body) {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "/agent-trail" {
			continue
		}
		cmd := Command{Addressed: true}
		if len(fields) == 2 {
			switch Verb(fields[1]) {
			case VerbRun, VerbRevise:
				cmd.Known = true
				cmd.Verb = Verb(fields[1])
			}
		}
		return cmd
	}
	return Command{}
}
