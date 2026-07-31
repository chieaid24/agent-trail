package gitworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func buildComparisonOrigin(t *testing.T) (origin, base, editA, editB string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q", "-b", "main")
	writeLines(t, src, "app.go", numberedLines(12))
	writeLines(t, src, "lib.go", numberedLines(12))
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "base")
	base = runGit(t, src, "rev-parse", "HEAD")

	runGit(t, src, "checkout", "-q", "-b", "a", base)
	lines := numberedLines(12)
	lines[4] = "line05 changed by a"
	writeLines(t, src, "app.go", lines)
	writeLines(t, src, "added.md", []string{"new"})
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "edit a")
	editA = runGit(t, src, "rev-parse", "HEAD")

	runGit(t, src, "checkout", "-q", "-b", "b", base)
	lines = numberedLines(12)
	lines[5] = "line06 changed by b"
	writeLines(t, src, "app.go", lines)
	runGit(t, src, "rm", "-q", "lib.go")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "edit b")
	editB = runGit(t, src, "rev-parse", "HEAD")

	origin = filepath.Join(dir, "origin.git")
	runGit(t, dir, "clone", "-q", "--bare", src, "origin.git")
	return origin, base, editA, editB
}

func numberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line" + string(rune('0'+(i+1)/10)) + string(rune('0'+(i+1)%10))
	}
	return lines
}

func writeLines(t *testing.T, dir, name string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConflictOps(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	m := newTestManager(t)
	origin, base, editA, editB := buildComparisonOrigin(t)
	repo := RepoRef{ID: "repo-1", CloneURL: origin}
	if _, err := m.EnsureMirror(ctx, repo); err != nil {
		t.Fatalf("EnsureMirror: %v", err)
	}

	t.Run("changed files", func(t *testing.T) {
		files, err := m.ChangedFiles(ctx, repo, base, editA)
		if err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
		want := []string{"added.md", "app.go"}
		if !reflect.DeepEqual(files, want) {
			t.Errorf("files = %v, want %v", files, want)
		}
	})

	t.Run("diff hunks", func(t *testing.T) {
		hunks, err := m.DiffHunks(ctx, repo, base, editB)
		if err != nil {
			t.Fatalf("DiffHunks: %v", err)
		}
		want := map[string][]LineRange{
			"app.go": {{Start: 6, End: 6}},
			"lib.go": {{Start: 1, End: 12}}, // deletion spans the whole file
		}
		if !reflect.DeepEqual(hunks, want) {
			t.Errorf("hunks = %v, want %v", hunks, want)
		}
	})

	t.Run("merge tree conflict", func(t *testing.T) {
		clean, conflicted, err := m.MergeTree(ctx, repo, editA, editB)
		if err != nil {
			t.Fatalf("MergeTree: %v", err)
		}
		if clean {
			t.Fatal("adjacent edits to one line region must conflict")
		}
		if !reflect.DeepEqual(conflicted, []string{"app.go"}) {
			t.Errorf("conflicted = %v, want [app.go]", conflicted)
		}
	})

	t.Run("merge tree clean", func(t *testing.T) {
		clean, conflicted, err := m.MergeTree(ctx, repo, base, editA)
		if err != nil {
			t.Fatalf("MergeTree: %v", err)
		}
		if !clean || conflicted != nil {
			t.Errorf("ancestor merge = clean %v conflicted %v, want clean", clean, conflicted)
		}
	})

	t.Run("has commit", func(t *testing.T) {
		ok, err := m.HasCommit(ctx, repo, editA)
		if err != nil || !ok {
			t.Fatalf("HasCommit(present) = %v, %v; want true", ok, err)
		}
		absent := strings.Repeat("a", 40)
		ok, err = m.HasCommit(ctx, repo, absent)
		if err != nil || ok {
			t.Fatalf("HasCommit(absent) = %v, %v; want false", ok, err)
		}
	})

	t.Run("missing mirror", func(t *testing.T) {
		_, err := m.ChangedFiles(ctx, RepoRef{ID: "no-such"}, base, editA)
		if !errors.Is(err, ErrMirrorMissing) {
			t.Errorf("err = %v, want ErrMirrorMissing", err)
		}
	})

	t.Run("rejects short sha", func(t *testing.T) {
		if _, err := m.ChangedFiles(ctx, repo, "abc123", editA); err == nil {
			t.Error("short sha must be rejected")
		}
	})
}

func TestParseHunksInsertion(t *testing.T) {
	out := strings.Join([]string{
		"diff --git a/app.go b/app.go",
		"index 111..222 100644",
		"--- a/app.go",
		"+++ b/app.go",
		"@@ -4,0 +5,2 @@ context",
		"+inserted one",
		"+inserted two",
		"diff --git a/new.md b/new.md",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.md",
		"@@ -0,0 +1 @@",
		"+hello",
	}, "\n")
	hunks, err := parseHunks(out)
	if err != nil {
		t.Fatalf("parseHunks: %v", err)
	}
	want := map[string][]LineRange{
		"app.go": {{Start: 4, End: 4}},
		"new.md": {{Start: 0, End: 0}},
	}
	if !reflect.DeepEqual(hunks, want) {
		t.Errorf("hunks = %v, want %v", hunks, want)
	}
}
