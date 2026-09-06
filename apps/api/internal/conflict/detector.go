package conflict

import (
	"context"
	"log/slog"
	"sort"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
)

type GitOps interface {
	EnsureMirror(ctx context.Context, repo gitworkspace.RepoRef) (string, error)
	HasCommit(ctx context.Context, repo gitworkspace.RepoRef, sha string) (bool, error)
	ChangedFiles(ctx context.Context, repo gitworkspace.RepoRef, base, head string) ([]string, error)
	DiffHunks(ctx context.Context, repo gitworkspace.RepoRef, base, head string) (map[string][]gitworkspace.LineRange, error)
	Diff(ctx context.Context, repo gitworkspace.RepoRef, base, head string) (string, error)
	MergeTree(ctx context.Context, repo gitworkspace.RepoRef, commitA, commitB string) (bool, []string, error)
}

type Records interface {
	ActiveSiblings(ctx context.Context, repositoryID, excludeTaskID string) ([]Sibling, error)
	Reconcile(ctx context.Context, repositoryID, taskID string, detections []Detection) error
}

type Detector struct {
	Git      GitOps
	Records  Records
	Logger   *slog.Logger
	Semantic SemanticAssessor
}

func (d *Detector) Detect(ctx context.Context, repo gitworkspace.RepoRef, repositoryID, taskID, taskTitle, base, final string) ([]Detection, error) {
	siblings, err := d.Records.ActiveSiblings(ctx, repositoryID, taskID)
	if err != nil {
		return nil, err
	}
	if len(siblings) == 0 {
		return nil, nil
	}
	if _, err := d.Git.EnsureMirror(ctx, repo); err != nil {
		return nil, err
	}

	self, err := d.changeSet(ctx, repo, base, final)
	if err != nil {
		return nil, err
	}
	var selfDiff string
	if d.Semantic != nil {
		selfDiff, err = d.Git.Diff(ctx, repo, base, final)
		if err != nil {
			d.logSemanticFailure(ctx, taskID, "", err)
		}
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

		var semantic SemanticVerdict
		if d.Semantic != nil && selfDiff != "" {
			otherDiff, diffErr := d.Git.Diff(ctx, repo, sib.BaseSHA, sib.FinalSHA)
			if diffErr != nil {
				d.logSemanticFailure(ctx, taskID, sib.TaskID, diffErr)
			} else {
				semantic, diffErr = d.Semantic.Assess(ctx, SemanticRequest{
					TaskID: taskID, TaskTitle: taskTitle, TaskDiff: selfDiff,
					OtherTaskID: sib.TaskID, OtherTaskTitle: sib.Title, OtherTaskDiff: otherDiff,
				})
				if diffErr == nil {
					diffErr = validateVerdict(semantic)
				}
				if diffErr != nil {
					d.logSemanticFailure(ctx, taskID, sib.TaskID, diffErr)
					semantic = SemanticVerdict{}
				}
			}
		}
		if semantic.Conflicts {
			kinds = append(kinds, KindSemantic)
		}

		if len(kinds) > 0 {
			detections = append(detections, Detection{
				OtherTaskID:         sib.TaskID,
				OtherTaskTitle:      sib.Title,
				Kinds:               kinds,
				Files:               files,
				SemanticSeverity:    semantic.Severity,
				SemanticExplanation: semantic.Explanation,
				SemanticEvidence:    semantic.Evidence,
			})
		}
	}
	if err := d.Records.Reconcile(ctx, repositoryID, taskID, detections); err != nil {
		return nil, err
	}
	return detections, nil
}

func (d *Detector) logSemanticFailure(ctx context.Context, taskID, siblingTaskID string, err error) {
	if d.Logger == nil {
		return
	}
	d.Logger.LogAttrs(ctx, slog.LevelWarn, "semantic conflict detection failed",
		slog.String("event", "semantic_conflict_detection_failed"),
		slog.String("trace_id", observability.TraceIDFrom(ctx)),
		slog.String("task_id", taskID),
		slog.String("sibling_task_id", siblingTaskID),
		slog.String("error", err.Error()),
	)
}

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
