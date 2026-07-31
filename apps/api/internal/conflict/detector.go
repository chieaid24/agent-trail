package conflict

import (
	"context"
	"log/slog"
	"sort"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

// GitOps is the slice of gitworkspace the detector needs; implemented by
// *gitworkspace.Manager.
type GitOps interface {
	HasCommit(ctx context.Context, repo gitworkspace.RepoRef, sha string) (bool, error)
	ChangedFiles(ctx context.Context, repo gitworkspace.RepoRef, base, head string) ([]string, error)
	DiffHunks(ctx context.Context, repo gitworkspace.RepoRef, base, head string) (map[string][]gitworkspace.LineRange, error)
	MergeTree(ctx context.Context, repo gitworkspace.RepoRef, commitA, commitB string) (bool, []string, error)
}

// Records is the store slice the detector reads and writes; implemented by
// *Store, faked in tests.
type Records interface {
	ActiveSiblings(ctx context.Context, repositoryID, excludeTaskID string) ([]Sibling, error)
	Upsert(ctx context.Context, repositoryID, taskID, otherTaskID string, kinds []Kind, files []string) error
	DeletePair(ctx context.Context, taskID, otherTaskID string) error
}

// Detector compares one published diff against every active sibling in the
// repository and persists the resulting warnings.
type Detector struct {
	Git     GitOps
	Records Records
	Logger  *slog.Logger
}

// Detect runs after taskID publishes base..final: it recomputes this task's
// pair rows against every active sibling, upserting pairs that conflict and
// deleting pairs that no longer do. A sibling whose commits are not in the
// local mirror (published from another host, or pruned) is skipped and
// logged, never guessed about. The returned detections carry only the pairs
// that conflict.
func (d *Detector) Detect(ctx context.Context, repo gitworkspace.RepoRef, repositoryID, taskID, base, final string) ([]Detection, error) {
	siblings, err := d.Records.ActiveSiblings(ctx, repositoryID, taskID)
	if err != nil {
		return nil, err
	}
	if len(siblings) == 0 {
		return nil, nil
	}

	self, err := d.changeSet(ctx, repo, base, final)
	if err != nil {
		return nil, err
	}

	var detections []Detection
	for _, sib := range siblings {
		present, err := d.commitsPresent(ctx, repo, sib.BaseSHA, sib.FinalSHA)
		if err != nil {
			return detections, err
		}
		if !present {
			d.Logger.LogAttrs(ctx, slog.LevelWarn, "conflict check skipped a sibling",
				slog.String("event", "conflict_sibling_skipped"),
				slog.String("trace_id", observability.TraceIDFrom(ctx)),
				slog.String("task_id", taskID),
				slog.String("sibling_task_id", sib.TaskID),
				slog.String("reason", "sibling commits not in the local mirror"),
			)
			continue
		}
		other, err := d.changeSet(ctx, repo, sib.BaseSHA, sib.FinalSHA)
		if err != nil {
			return detections, err
		}

		kinds, files := Overlap(self, other)
		clean, conflicted, err := d.Git.MergeTree(ctx, repo, final, sib.FinalSHA)
		if err != nil {
			return detections, err
		}
		if !clean {
			kinds = append(kinds, KindMergeConflict)
			files = mergeSorted(files, conflicted)
		}

		if len(kinds) == 0 {
			if err := d.Records.DeletePair(ctx, taskID, sib.TaskID); err != nil {
				return detections, err
			}
			continue
		}
		if err := d.Records.Upsert(ctx, repositoryID, taskID, sib.TaskID, kinds, files); err != nil {
			return detections, err
		}
		detections = append(detections, Detection{
			OtherTaskID:    sib.TaskID,
			OtherTaskTitle: sib.Title,
			Kinds:          kinds,
			Files:          files,
		})
	}
	return detections, nil
}

// changeSet loads one diff's files and base-side hunks.
func (d *Detector) changeSet(ctx context.Context, repo gitworkspace.RepoRef, base, final string) (ChangeSet, error) {
	files, err := d.Git.ChangedFiles(ctx, repo, base, final)
	if err != nil {
		return ChangeSet{}, err
	}
	hunks, err := d.Git.DiffHunks(ctx, repo, base, final)
	if err != nil {
		return ChangeSet{}, err
	}
	return ChangeSet{Files: files, Hunks: hunks}, nil
}

// commitsPresent reports whether every sha is in the local mirror.
func (d *Detector) commitsPresent(ctx context.Context, repo gitworkspace.RepoRef, shas ...string) (bool, error) {
	for _, sha := range shas {
		ok, err := d.Git.HasCommit(ctx, repo, sha)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// mergeSorted unions two path lists, sorted and deduplicated.
func mergeSorted(a, b []string) []string {
	seen := map[string]bool{}
	for _, f := range a {
		seen[f] = true
	}
	for _, f := range b {
		seen[f] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
