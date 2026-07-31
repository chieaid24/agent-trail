package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Conflict-detection plumbing (docs/architecture/conflict-detection.md).
// Every operation is a read-only comparison run in the bare mirror cache, so
// no worktree or network access is needed; the commits under comparison are
// present locally because every attempt of a repository commits through the
// mirror's shared object store. HasCommit guards the cases where they are
// not (an attempt published from another host, or a pruned object).

// ErrMirrorMissing reports that the repository has no local mirror, so there
// is nothing to compare against.
var ErrMirrorMissing = errors.New("gitworkspace: repository mirror missing")

// LineRange is a 1-based inclusive span of lines in one file, in the
// coordinates of a diff's base side.
type LineRange struct {
	Start int
	End   int
}

// hunkHeaderRe captures the base-side "-start,count" of a unified hunk
// header, count omitted meaning 1.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+`)

// mirrorPath returns the existing mirror of repo, without touching the
// network or creating anything.
func (m *Manager) mirrorPath(repo RepoRef) (string, error) {
	if !validComponent(repo.ID) {
		return "", fmt.Errorf("gitworkspace: repository id %q is not a safe path component", repo.ID)
	}
	mirror := filepath.Join(m.reposDir, repo.ID, "repo.git")
	if _, err := os.Stat(filepath.Join(mirror, "HEAD")); err != nil {
		return "", ErrMirrorMissing
	}
	return mirror, nil
}

// HasCommit reports whether the mirror holds sha as a commit.
func (m *Manager) HasCommit(ctx context.Context, repo RepoRef, sha string) (bool, error) {
	mirror, err := m.mirrorPath(repo)
	if err != nil {
		return false, err
	}
	if !validSHA(sha) {
		return false, fmt.Errorf("gitworkspace: %q is not a full commit sha", sha)
	}
	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	_, code, err := m.git.runExit(ctx, mirror, []int{1, 128},
		"cat-file", "-e", sha+"^{commit}")
	if err != nil {
		return false, fmt.Errorf("gitworkspace: check commit: %w", err)
	}
	return code == 0, nil
}

// ChangedFiles lists the paths that differ between base and head, renames
// split into delete plus add so every touched path appears.
func (m *Manager) ChangedFiles(ctx context.Context, repo RepoRef, base, head string) ([]string, error) {
	mirror, err := m.diffTarget(repo, base, head)
	if err != nil {
		return nil, err
	}
	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	out, err := m.git.run(ctx, mirror, "diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		return nil, fmt.Errorf("gitworkspace: changed files: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// DiffHunks returns, per changed path, the base-side line ranges the diff
// touches. A pure insertion (base-side count 0) is recorded as the single
// line it inserts after, so adjacency to surrounding edits still registers.
// Binary files appear in ChangedFiles but carry no hunks.
func (m *Manager) DiffHunks(ctx context.Context, repo RepoRef, base, head string) (map[string][]LineRange, error) {
	mirror, err := m.diffTarget(repo, base, head)
	if err != nil {
		return nil, err
	}
	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	out, err := m.git.run(ctx, mirror, "diff", "--unified=0", "--no-renames", base, head)
	if err != nil {
		return nil, fmt.Errorf("gitworkspace: diff hunks: %w", err)
	}
	return parseHunks(out)
}

// MergeTree attempts a temporary in-memory merge of the two commits
// (git merge-tree --write-tree, real merge-ort machinery, no worktree) and
// returns whether it is clean plus the conflicted paths when it is not.
func (m *Manager) MergeTree(ctx context.Context, repo RepoRef, commitA, commitB string) (bool, []string, error) {
	mirror, err := m.diffTarget(repo, commitA, commitB)
	if err != nil {
		return false, nil, err
	}
	lock := m.lockFor(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	// Exit 0 is a clean merge, 1 a conflicted one; anything else (e.g.
	// unrelated histories) is an error. Output is the merged tree OID, then
	// with --name-only one conflicted path per line.
	out, code, err := m.git.runExit(ctx, mirror, []int{1},
		"merge-tree", "--write-tree", "--no-messages", "--name-only", commitA, commitB)
	if err != nil {
		return false, nil, fmt.Errorf("gitworkspace: merge-tree: %w", err)
	}
	if code == 0 {
		return true, nil, nil
	}
	lines := strings.Split(out, "\n")
	var conflicted []string
	for _, line := range lines[1:] { // first line is the tree OID
		if line != "" {
			conflicted = append(conflicted, line)
		}
	}
	return false, conflicted, nil
}

// diffTarget validates a two-commit comparison and returns the mirror path.
func (m *Manager) diffTarget(repo RepoRef, a, b string) (string, error) {
	mirror, err := m.mirrorPath(repo)
	if err != nil {
		return "", err
	}
	for _, sha := range []string{a, b} {
		if !validSHA(sha) {
			return "", fmt.Errorf("gitworkspace: %q is not a full commit sha", sha)
		}
	}
	return mirror, nil
}

// parseHunks reads --unified=0 output into base-side ranges per path.
func parseHunks(out string) (map[string][]LineRange, error) {
	hunks := map[string][]LineRange{}
	var oldPath, newPath, current string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			oldPath = strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/")
			current = ""
		case strings.HasPrefix(line, "+++ "):
			newPath = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			// A deletion has no new side; fall back to the old path.
			current = newPath
			if newPath == "/dev/null" {
				current = oldPath
			}
		case strings.HasPrefix(line, "@@ "):
			if current == "" || current == "/dev/null" {
				return nil, fmt.Errorf("gitworkspace: hunk header before file header: %q", line)
			}
			match := hunkHeaderRe.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("gitworkspace: malformed hunk header: %q", line)
			}
			start, err := strconv.Atoi(match[1])
			if err != nil {
				return nil, fmt.Errorf("gitworkspace: hunk start %q: %w", match[1], err)
			}
			count := 1
			if match[2] != "" {
				if count, err = strconv.Atoi(match[2]); err != nil {
					return nil, fmt.Errorf("gitworkspace: hunk count %q: %w", match[2], err)
				}
			}
			r := LineRange{Start: start, End: start + count - 1}
			if count == 0 {
				// Pure insertion after line start: a zero-width range at the
				// insertion point.
				r = LineRange{Start: start, End: start}
			}
			hunks[current] = append(hunks[current], r)
		}
	}
	return hunks, nil
}
