package gitworkspace

import (
	"errors"
	"regexp"
	"strings"
)

// BranchPrefix namespaces every branch Agent Trail creates or pushes. The push
// guard refuses any ref outside it (docs/architecture/git-workspaces.md).
const BranchPrefix = "agent-trail/"

// maxSlug bounds the sanitized slug so the full ref stays well under the
// tasks.working_branch limit of 255 and matches the repository slug shape used
// elsewhere in the schema.
const maxSlug = 100

// ErrEmptyBranch is returned when a name sanitizes to nothing usable.
var ErrEmptyBranch = errors.New("gitworkspace: branch name is empty after sanitization")

// safeBranch matches a fully-formed working branch: the required prefix plus a
// slug of lowercase alphanumerics and dashes, first character alphanumeric. The
// shape forbids everything git ref-format rejects - "..", control characters,
// spaces, "~^:?*[\", a leading dot, and a trailing ".lock".
var safeBranch = regexp.MustCompile(`^agent-trail/[a-z0-9][a-z0-9-]{0,99}$`)

// nonSlug matches any run of characters that may not appear in a slug.
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeBranch turns an arbitrary label (an issue title, a task id) into a
// deterministic, ref-safe working branch under BranchPrefix. Disallowed
// characters collapse to single dashes; the result is lowercased, trimmed, and
// length-capped. It returns ErrEmptyBranch when nothing usable remains.
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

// validBranch reports whether name is a working branch this package will create
// or push. The push guard calls it so a hand-built branch cannot bypass
// SanitizeBranch and escape the agent-trail/ namespace.
func validBranch(name string) bool {
	return safeBranch.MatchString(name)
}
