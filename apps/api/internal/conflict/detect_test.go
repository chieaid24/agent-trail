package conflict

import (
	"reflect"
	"testing"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
)

func TestOverlap(t *testing.T) {
	cases := []struct {
		label     string
		a, b      ChangeSet
		wantKinds []Kind
		wantFiles []string
	}{
		{
			label:     "disjoint files",
			a:         ChangeSet{Files: []string{"lib.go"}},
			b:         ChangeSet{Files: []string{"docs.md"}},
			wantKinds: nil,
			wantFiles: []string{},
		},
		{
			label: "same file, distant lines",
			a: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 2, End: 2}},
			}},
			b: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 12, End: 12}},
			}},
			wantKinds: []Kind{KindFileOverlap},
			wantFiles: []string{"app.go"},
		},
		{
			label: "same file, adjacent lines",
			a: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 5, End: 5}},
			}},
			b: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 7, End: 7}},
			}},
			wantKinds: []Kind{KindFileOverlap, KindAdjacentLines},
			wantFiles: []string{"app.go"},
		},
		{
			label: "insertion adjacent to an edit",
			a: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 4, End: 4}}, // pure insertion after line 4
			}},
			b: ChangeSet{Files: []string{"app.go"}, Hunks: map[string][]gitworkspace.LineRange{
				"app.go": {{Start: 6, End: 6}},
			}},
			wantKinds: []Kind{KindFileOverlap, KindAdjacentLines},
			wantFiles: []string{"app.go"},
		},
		{
			label:     "migration collision across different files",
			a:         ChangeSet{Files: []string{"apps/api/migrations/00007_a.sql"}},
			b:         ChangeSet{Files: []string{"apps/api/migrations/00007_b.sql"}},
			wantKinds: []Kind{KindMigration},
			wantFiles: []string{
				"apps/api/migrations/00007_a.sql",
				"apps/api/migrations/00007_b.sql",
			},
		},
		{
			label:     "sql outside a migrations directory is not a migration",
			a:         ChangeSet{Files: []string{"queries/report.sql"}},
			b:         ChangeSet{Files: []string{"queries/other.sql"}},
			wantKinds: nil,
			wantFiles: []string{},
		},
		{
			label: "shared dependency manifest",
			a: ChangeSet{Files: []string{"go.mod"}, Hunks: map[string][]gitworkspace.LineRange{
				"go.mod": {{Start: 3, End: 3}},
			}},
			b: ChangeSet{Files: []string{"go.mod"}, Hunks: map[string][]gitworkspace.LineRange{
				"go.mod": {{Start: 11, End: 11}},
			}},
			wantKinds: []Kind{KindFileOverlap, KindDependency},
			wantFiles: []string{"go.mod"},
		},
		{
			label: "different dependency manifests",
			a:     ChangeSet{Files: []string{"go.mod"}},
			b:     ChangeSet{Files: []string{"apps/web/package.json"}},
			// Different ecosystems do not contend.
			wantKinds: nil,
			wantFiles: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			kinds, files := Overlap(tc.a, tc.b)
			if !reflect.DeepEqual(kinds, tc.wantKinds) {
				t.Errorf("kinds = %v, want %v", kinds, tc.wantKinds)
			}
			if !reflect.DeepEqual(files, tc.wantFiles) {
				t.Errorf("files = %v, want %v", files, tc.wantFiles)
			}
			// Symmetric by construction: both directions must agree.
			revKinds, revFiles := Overlap(tc.b, tc.a)
			if !reflect.DeepEqual(revKinds, tc.wantKinds) || !reflect.DeepEqual(revFiles, tc.wantFiles) {
				t.Errorf("Overlap is not symmetric: reversed = %v %v", revKinds, revFiles)
			}
		})
	}
}

func TestRangesAdjacentBoundary(t *testing.T) {
	at := func(n int) []gitworkspace.LineRange {
		return []gitworkspace.LineRange{{Start: n, End: n}}
	}
	if !rangesAdjacent(at(5), at(8)) {
		t.Error("gap of 3 lines must be adjacent")
	}
	if rangesAdjacent(at(5), at(9)) {
		t.Error("gap of 4 lines must not be adjacent")
	}
}
