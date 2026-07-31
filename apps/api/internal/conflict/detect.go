package conflict

import (
	"path"
	"sort"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
)

// adjacencyWindow is how close two edits to one file must be, in base-side
// lines, to count as adjacent. Three lines mirrors the default diff context:
// edits that close are near-certain to conflict textually or semantically.
const adjacencyWindow = 3

// dependencyManifests are the dependency files whose concurrent edits merge
// badly enough to warrant a dedicated warning (lockfiles especially).
var dependencyManifests = map[string]bool{
	"go.mod": true, "go.sum": true,
	"package.json": true, "package-lock.json": true,
	"pnpm-lock.yaml": true, "yarn.lock": true,
	"requirements.txt": true, "Pipfile": true, "Pipfile.lock": true,
	"pyproject.toml": true, "poetry.lock": true,
	"Cargo.toml": true, "Cargo.lock": true,
	"Gemfile": true, "Gemfile.lock": true,
	"composer.json": true, "composer.lock": true,
}

// ChangeSet is one task's published diff, reduced to what overlap detection
// needs: the changed paths and the base-side line ranges each path touched.
type ChangeSet struct {
	Files []string
	Hunks map[string][]gitworkspace.LineRange
}

// Overlap runs every pairwise detector that needs no repository access and
// returns the kinds that fired plus the implicated files, both sorted.
// Merge-conflict detection needs git and lives in the Detector.
//
// Adjacency compares base-side coordinates. The two diffs may sit on
// different base commits of one branch, so the coordinates are approximate
// when the bases diverge around the compared lines; the temporary-merge
// detector catches what this approximation misses.
func Overlap(a, b ChangeSet) ([]Kind, []string) {
	var kinds []Kind
	files := map[string]bool{}

	shared := intersect(a.Files, b.Files)
	if len(shared) > 0 {
		kinds = append(kinds, KindFileOverlap)
		for _, f := range shared {
			files[f] = true
		}
	}

	adjacent := false
	for _, f := range shared {
		if rangesAdjacent(a.Hunks[f], b.Hunks[f]) {
			adjacent = true
			files[f] = true
		}
	}
	if adjacent {
		kinds = append(kinds, KindAdjacentLines)
	}

	aMigrations, bMigrations := migrationPaths(a.Files), migrationPaths(b.Files)
	if len(aMigrations) > 0 && len(bMigrations) > 0 {
		kinds = append(kinds, KindMigration)
		for _, f := range append(aMigrations, bMigrations...) {
			files[f] = true
		}
	}

	dependency := false
	for _, f := range shared {
		if dependencyManifests[path.Base(f)] {
			dependency = true
			files[f] = true
		}
	}
	if dependency {
		kinds = append(kinds, KindDependency)
	}

	return kinds, sortedKeys(files)
}

// intersect returns the paths present in both lists.
func intersect(a, b []string) []string {
	inA := map[string]bool{}
	for _, f := range a {
		inA[f] = true
	}
	var out []string
	for _, f := range b {
		if inA[f] {
			out = append(out, f)
			inA[f] = false // each shared path once
		}
	}
	sort.Strings(out)
	return out
}

// rangesAdjacent reports whether any range in a sits within adjacencyWindow
// lines of any range in b.
func rangesAdjacent(a, b []gitworkspace.LineRange) bool {
	for _, ra := range a {
		for _, rb := range b {
			if ra.Start <= rb.End+adjacencyWindow && rb.Start <= ra.End+adjacencyWindow {
				return true
			}
		}
	}
	return false
}

// migrationPaths returns the paths that look like schema migrations: SQL
// files under a migrations directory.
func migrationPaths(files []string) []string {
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".sql") {
			continue
		}
		for _, dir := range strings.Split(path.Dir(f), "/") {
			if dir == "migrations" {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
