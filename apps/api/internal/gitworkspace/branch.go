package gitworkspace

import (
	"errors"
	"regexp"
	"strings"
)

// push guard refuses any ref outside this prefix
const BranchPrefix = "agent-trail/"

// full ref must stay under the tasks.working_branch 255 limit
const maxSlug = 100

var ErrEmptyBranch = errors.New("gitworkspace: branch name is empty after sanitization")

// shape forbids everything git ref-format rejects
var safeBranch = regexp.MustCompile(`^agent-trail/[a-z0-9][a-z0-9-]{0,99}$`)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func SanitizeBranch(raw string) (string, error) {
	slug := nonSlug.ReplaceAllString(strings.ToLower(raw), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > maxSlug {
		slug = strings.Trim(slug[:maxSlug], "-")
	}
	if slug == "" {
		return "", ErrEmptyBranch
	}
	return BranchPrefix + slug, nil
}

// push guard: a hand-built branch cannot bypass SanitizeBranch and escape the namespace
func validBranch(name string) bool {
	return safeBranch.MatchString(name)
}
