package conflict

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

type edit struct {
	path    string
	line    int      // 1-based line to replace; 0 writes content wholesale
	content []string // nil deletes the file
}

type fixtureCase struct {
	label     string
	a, b      []edit
	wantKinds []Kind
	wantFiles []string
}

func fixtureCases() []fixtureCase {
	changed := func(marker string) []string { return []string{"changed by " + marker} }
	return []fixtureCase{
		{
			label:     "disjoint files merge cleanly",
			a:         []edit{{path: "lib.go", line: 2, content: changed("a")}},
			b:         []edit{{path: "docs.md", line: 2, content: changed("b")}},
			wantKinds: nil,
		},
		{
			label:     "same file distant lines",
			a:         []edit{{path: "app.go", line: 2, content: changed("a")}},
			b:         []edit{{path: "app.go", line: 12, content: changed("b")}},
			wantKinds: []Kind{KindFileOverlap},
			wantFiles: []string{"app.go"},
		},
		{
			label:     "adjacent lines conflict on merge",
			a:         []edit{{path: "app.go", line: 5, content: changed("a")}},
			b:         []edit{{path: "app.go", line: 6, content: changed("b")}},
			wantKinds: []Kind{KindFileOverlap, KindAdjacentLines, KindMergeConflict},
			wantFiles: []string{"app.go"},
		},
		{
			label:     "delete against edit",
			a:         []edit{{path: "lib.go"}}, // delete
			b:         []edit{{path: "lib.go", line: 6, content: changed("b")}},
			wantKinds: []Kind{KindFileOverlap, KindAdjacentLines, KindMergeConflict},
			wantFiles: []string{"lib.go"},
		},
		{
			label: "migration collision across different files",
			a: []edit{{path: "migrations/00002_add_a.sql",
				content: []string{"CREATE TABLE a (id INT);"}}},
			b: []edit{{path: "migrations/00002_add_b.sql",
				content: []string{"CREATE TABLE b (id INT);"}}},
			wantKinds: []Kind{KindMigration},
			wantFiles: []string{
				"migrations/00002_add_a.sql",
				"migrations/00002_add_b.sql",
			},
		},
		{
			label:     "shared dependency manifest",
			a:         []edit{{path: "go.mod", line: 3, content: changed("a")}},
			b:         []edit{{path: "go.mod", line: 11, content: changed("b")}},
			wantKinds: []Kind{KindFileOverlap, KindDependency},
			wantFiles: []string{"go.mod"},
		},
	}
}

func TestDetectorFixtureSet(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	for _, tc := range fixtureCases() {
		t.Run(tc.label, func(t *testing.T) {
			f := buildFixture(t, tc)
			records := &fakeRecords{siblings: []Sibling{
				{TaskID: "task-b", Title: "sibling", BaseSHA: f.base, FinalSHA: f.finalB},
			}}
			d := &Detector{Git: f.manager, Records: records, Logger: testLogger()}

			detections, err := d.Detect(ctx, f.repo, "repo-uuid", "task-a", f.base, f.finalA)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}

			if tc.wantKinds == nil {
				if len(detections) != 0 || len(records.detections) != 0 {
					t.Fatalf("clean pair produced detections %v / %v",
						detections, records.detections)
				}
				if records.reconciles != 1 {
					t.Fatalf("clean pair reconciles = %d, want 1", records.reconciles)
				}
				return
			}
			if len(detections) != 1 || len(records.detections) != 1 {
				t.Fatalf("detections = %v / %v, want one each",
					detections, records.detections)
			}
			got := detections[0]
			if got.OtherTaskID != "task-b" {
				t.Errorf("other task = %q, want task-b", got.OtherTaskID)
			}
			if !reflect.DeepEqual(got.Kinds, tc.wantKinds) {
				t.Errorf("kinds = %v, want %v", got.Kinds, tc.wantKinds)
			}
			if !reflect.DeepEqual(got.Files, tc.wantFiles) {
				t.Errorf("files = %v, want %v", got.Files, tc.wantFiles)
			}
			if !reflect.DeepEqual(records.detections[0], Detection{
				OtherTaskID: "task-b", OtherTaskTitle: "sibling",
				Kinds: tc.wantKinds, Files: tc.wantFiles,
			}) {
				t.Errorf("reconciled detection = %+v", records.detections[0])
			}
		})
	}
}

func TestDetectorSkipsSiblingMissingFromMirror(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	f := buildFixture(t, fixtureCases()[0])
	records := &fakeRecords{siblings: []Sibling{
		{TaskID: "task-x", Title: "elsewhere", BaseSHA: f.base,
			FinalSHA: strings.Repeat("b", 40)},
	}}
	d := &Detector{Git: f.manager, Records: records, Logger: testLogger()}

	detections, err := d.Detect(ctx, f.repo, "repo-uuid", "task-a", f.base, f.finalA)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detections) != 0 || len(records.detections) != 0 || records.reconciles != 1 {
		t.Errorf("missing sibling commits must clear stale state, got %v / %v / %d",
			detections, records.detections, records.reconciles)
	}
}

func TestDetectorRefreshesRemoteAgentBranches(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	f := buildFixture(t, fixtureCases()[1])
	remoteFinal := commitEdits(t, f.source, f.base, "remote-host",
		[]edit{{path: "app.go", line: 11, content: []string{"changed remotely"}}})
	runGit(t, f.source, "push", "-q", f.origin,
		remoteFinal+":refs/heads/agent-trail/remote-host")
	records := &fakeRecords{siblings: []Sibling{{
		TaskID: "task-remote", Title: "remote", BaseSHA: f.base, FinalSHA: remoteFinal,
	}}}
	d := &Detector{Git: f.manager, Records: records, Logger: testLogger()}

	detections, err := d.Detect(ctx, f.repo, "repo-uuid", "task-a", f.base, f.finalA)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detections) != 1 || detections[0].OtherTaskID != "task-remote" {
		t.Fatalf("detections = %+v, want remote task", detections)
	}
}

func TestDetectorNoSiblings(t *testing.T) {
	records := &fakeRecords{}
	d := &Detector{Records: records, Logger: testLogger()}
	detections, err := d.Detect(context.Background(),
		gitworkspace.RepoRef{ID: "r"}, "repo-uuid", "task-a",
		strings.Repeat("a", 40), strings.Repeat("b", 40))
	if err != nil || detections != nil {
		t.Fatalf("no siblings must be a no-op, got %v, %v", detections, err)
	}
}

type fixture struct {
	manager *gitworkspace.Manager
	repo    gitworkspace.RepoRef
	source  string
	origin  string
	base    string
	finalA  string
	finalB  string
}

func baseFiles() map[string][]string {
	lines := func() []string {
		out := make([]string, 12)
		for i := range out {
			out[i] = "line" + string(rune('0'+(i+1)/10)) + string(rune('0'+(i+1)%10))
		}
		return out
	}
	return map[string][]string{
		"app.go":                    lines(),
		"lib.go":                    lines(),
		"docs.md":                   lines(),
		"go.mod":                    lines(),
		"migrations/00001_init.sql": {"CREATE TABLE init (id INT);"},
	}
}

func buildFixture(t *testing.T, tc fixtureCase) fixture {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q", "-b", "main")
	for path, lines := range baseFiles() {
		writeFixtureFile(t, src, path, lines)
	}
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "base")
	base := runGit(t, src, "rev-parse", "HEAD")

	finalA := commitEdits(t, src, base, "a", tc.a)
	finalB := commitEdits(t, src, base, "b", tc.b)

	origin := filepath.Join(dir, "origin.git")
	runGit(t, dir, "clone", "-q", "--bare", src, "origin.git")

	logger := testLogger()
	manager, err := gitworkspace.New(t.TempDir(), logger, observability.NewRegistry())
	if err != nil {
		t.Fatalf("gitworkspace.New: %v", err)
	}
	repo := gitworkspace.RepoRef{ID: "repo-1", CloneURL: origin}
	if _, err := manager.EnsureMirror(context.Background(), repo); err != nil {
		t.Fatalf("EnsureMirror: %v", err)
	}
	return fixture{manager: manager, repo: repo, source: src, origin: origin,
		base: base, finalA: finalA, finalB: finalB}
}

func commitEdits(t *testing.T, src, base, branch string, edits []edit) string {
	t.Helper()
	runGit(t, src, "checkout", "-q", "-b", branch, base)
	for _, e := range edits {
		full := filepath.Join(src, e.path)
		switch {
		case e.content == nil:
			runGit(t, src, "rm", "-q", e.path)
		case e.line > 0:
			raw, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			lines[e.line-1] = e.content[0]
			writeFixtureFile(t, src, e.path, lines)
		default:
			writeFixtureFile(t, src, e.path, e.content)
		}
	}
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "edits on "+branch)
	return runGit(t, src, "rev-parse", "HEAD")
}

func writeFixtureFile(t *testing.T, root, path string, lines []string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full,
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeRecords struct {
	siblings   []Sibling
	detections []Detection
	reconciles int
}

func (f *fakeRecords) ActiveSiblings(ctx context.Context, repositoryID, excludeTaskID string) ([]Sibling, error) {
	return f.siblings, nil
}

func (f *fakeRecords) Reconcile(ctx context.Context, repositoryID, taskID string, detections []Detection) error {
	f.reconciles++
	f.detections = append(f.detections, detections...)
	return nil
}

func testLogger() *slog.Logger {
	return observability.NewLogger(io.Discard, "test", slog.LevelError)
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
