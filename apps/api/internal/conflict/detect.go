package conflict

import (
	"path"
	"sort"
	"strings"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
)

// Three lines matches the default diff context.
const adjacencyWindow = 3

// dependencyManifests identifies high-contention dependency files.
var dependencyManifests = map[string]bool{
	"go.mod": true, "go.sum": true,
	"package.json": true, "package-lock.json": true,
	"npm-shrinkwrap.json": true, "pnpm-lock.yaml": true,
	"yarn.lock": true, "bun.lock": true, "bun.lockb": true,
	"requirements.txt": true, "Pipfile": true, "Pipfile.lock": true,
	"pyproject.toml": true, "poetry.lock": true, "uv.lock": true,
	"Cargo.toml": true, "Cargo.lock": true,
	"Gemfile": true, "Gemfile.lock": true,
	"composer.json": true, "composer.lock": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"settings.gradle": true, "settings.gradle.kts": true, "gradle.lockfile": true,
	"packages.lock.json": true, "Directory.Packages.props": true,
	"Package.swift": true, "Package.resolved": true,
	"mix.exs": true, "mix.lock": true,
	"pubspec.yaml": true, "pubspec.lock": true,
}

// ChangeSet contains changed paths and base-side hunks.
type ChangeSet struct {
	Files []string
	Hunks map[string][]gitworkspace.LineRange
}

// Overlap returns sorted path and hunk conflicts without repository access.
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
