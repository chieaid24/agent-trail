package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/chieaid24/agent-trail/apps/api/internal/evidence"
	"github.com/chieaid24/agent-trail/apps/api/internal/github"
	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
	"github.com/chieaid24/agent-trail/apps/api/internal/validation"
)

// PublishGitHub is the slice of the GitHub client publishing calls;
// implemented by *github.Client, faked in tests.
type PublishGitHub interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
	BranchHeadSHA(ctx context.Context, installationID int64, owner, repo, branch string) (string, error)
	CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, issueNumber int64, body string) error
	FindPullRequestByHead(ctx context.Context, installationID int64, owner, repo, headOwner, branch string) (*github.PullRequest, error)
	CreateDraftPullRequest(ctx context.Context, installationID int64, owner, repo string, p github.PullRequestParams) (github.PullRequest, error)
	UpdatePullRequestBody(ctx context.Context, installationID int64, owner, repo string, number int64, body string) error
	CreateCheckRun(ctx context.Context, installationID int64, owner, repo string, p github.CheckRunParams) (int64, error)
	UpdateCheckRun(ctx context.Context, installationID int64, owner, repo string, checkRunID int64, p github.CheckRunParams) error
	ListCheckRuns(ctx context.Context, installationID int64, owner, repo, ref, checkName string) ([]github.CheckRun, error)
}

// RepositoryResolver resolves a task's repository publishing context;
// implemented by *github.Store.
type RepositoryResolver interface {
	RepositoryContextByID(ctx context.Context, repositoryID string) (github.RepositoryContext, error)
}

// checkOutputLimit bounds the check-run output summary (GitHub caps it at
// 65535 characters).
const checkOutputLimit = 60000

// publishTarget carries the resolved repository context of a publishable
// task. nil means the fake local flow: no repository, or publishing
// dependencies not configured.
type publishTarget struct {
	repo github.RepositoryContext
}

// publishTarget resolves whether and where the task publishes. A repository
// that cannot be resolved (deleted, or its installation gone or suspended)
// fails the task: it was asked to publish and never can.
func (e *Executor) publishTarget(ctx context.Context, c *Claim, t task.Task) (*publishTarget, error) {
	if t.RepositoryID == nil || e.Workspaces == nil || e.GitHub == nil || e.Repos == nil {
		return nil, nil
	}
	rc, err := e.Repos.RepositoryContextByID(ctx, *t.RepositoryID)
	if errors.Is(err, github.ErrRepositoryNotFound) || errors.Is(err, github.ErrNoInstallation) {
		return nil, e.failTask(ctx, c, "publishing_unavailable", err.Error())
	}
	if err != nil {
		return nil, fmt.Errorf("resolve repository: %w", err)
	}
	return &publishTarget{repo: rc}, nil
}

// provisionWorkspace resolves the base commit and working branch (first
// recorded values win, so a recovered attempt reuses them), records them,
// and cuts the attempt's git worktree from the repository mirror.
func (e *Executor) provisionWorkspace(ctx context.Context, c *Claim, t task.Task, pub *publishTarget) (gitworkspace.Workspace, error) {
	rc := pub.repo
	base := ""
	if t.BaseCommitSHA != nil {
		base = *t.BaseCommitSHA
	}
	if base == "" {
		head, err := e.GitHub.BranchHeadSHA(ctx, rc.InstallationID, rc.Owner, rc.Name, t.BaseBranch)
		if err != nil {
			return gitworkspace.Workspace{}, fmt.Errorf("resolve base sha: %w", err)
		}
		base = head
	}
	branch, err := gitworkspace.SanitizeBranch(branchLabel(t))
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	base, branch, err = e.Tasks.EnsureGitContext(ctx, c.TaskID, base, branch)
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	if err := e.Store.RecordAttemptBase(ctx, c.AttemptID, base); err != nil {
		return gitworkspace.Workspace{}, err
	}

	repoRef, err := e.repoRef(ctx, rc)
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	params := gitworkspace.CreateParams{
		Repo:        repoRef,
		AttemptID:   c.AttemptID,
		BaseSHA:     base,
		BranchLabel: strings.TrimPrefix(branch, gitworkspace.BranchPrefix),
	}
	ws, err := e.Workspaces.CreateWorktree(ctx, params)
	if err != nil {
		// A dead previous owner on this host may have left the worktree or
		// its branch behind; clear both and retry once.
		if cleanupErr := e.Workspaces.CleanupStale(ctx, repoRef, c.AttemptID, branch); cleanupErr != nil {
			return gitworkspace.Workspace{}, cleanupErr
		}
		ws, err = e.Workspaces.CreateWorktree(ctx, params)
		if err != nil {
			return gitworkspace.Workspace{}, err
		}
	}
	return ws, nil
}

// repoRef builds the mirror reference with a fresh installation token
// embedded in the clone URL. The URL is never logged; gitworkspace redacts
// it from errors.
func (e *Executor) repoRef(ctx context.Context, rc github.RepositoryContext) (gitworkspace.RepoRef, error) {
	token, err := e.GitHub.InstallationToken(ctx, rc.InstallationID)
	if err != nil {
		return gitworkspace.RepoRef{}, fmt.Errorf("mint installation token: %w", err)
	}
	cloneURL, err := credentialedCloneURL(rc.CloneURL, token)
	if err != nil {
		return gitworkspace.RepoRef{}, err
	}
	return gitworkspace.RepoRef{ID: rc.ID, CloneURL: cloneURL}, nil
}

// credentialedCloneURL embeds an installation token into an https clone
// URL. Non-https URLs (local mirrors in tests) pass through untouched: the
// GitHub API only ever serves https clone URLs, so production repositories
// always carry the credential.
func credentialedCloneURL(cloneURL, token string) (string, error) {
	u, err := url.Parse(cloneURL)
	if err != nil {
		return "", fmt.Errorf("parse clone url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return cloneURL, nil
	}
	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}

// branchLabel derives the deterministic working-branch label for a task.
// Deterministic so retries and recovered owners land on one branch (a
// publishing idempotency key); the task-id suffix keeps successive tasks on
// one issue from colliding.
func branchLabel(t task.Task) string {
	id := strings.ReplaceAll(t.ID, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	title := t.Title
	if len(title) > 48 {
		title = title[:48]
	}
	if t.SourceIssueNumber != nil {
		return fmt.Sprintf("issue-%d-%s-%s", *t.SourceIssueNumber, title, id)
	}
	return fmt.Sprintf("task-%s-%s", title, id)
}

// publishFromWorkspace publishes a live worktree: commit, push, then the
// GitHub surface. A clean tree whose HEAD equals the base is the no-change
// outcome; a clean tree whose HEAD moved is a recovered owner's commit.
func (e *Executor) publishFromWorkspace(ctx context.Context, c *Claim, t task.Task, pub *publishTarget, ws gitworkspace.Workspace, summary string) (task.Status, error) {
	if t.BaseCommitSHA != nil && ws.BaseSHA != *t.BaseCommitSHA {
		return "", e.failTask(ctx, c, "base_mismatch", fmt.Sprintf(
			"workspace base %s does not match recorded base %s",
			ws.BaseSHA, *t.BaseCommitSHA))
	}

	provider := e.Adapter.Name()
	if t.AgentProvider != nil {
		provider = *t.AgentProvider
	}
	model := ""
	if t.AgentModel != nil {
		model = *t.AgentModel
	}
	sha, err := e.Workspaces.Commit(ctx, ws, gitworkspace.CommitParams{
		Message:  t.Title,
		TaskID:   t.ID,
		Provider: provider,
		Model:    model,
	})
	if errors.Is(err, gitworkspace.ErrNothingToCommit) {
		head, headErr := e.Workspaces.Head(ctx, ws)
		if headErr != nil {
			return "", headErr
		}
		if head == ws.BaseSHA {
			return "", e.publishNoChange(ctx, c, t, pub, ws.BaseSHA, summary)
		}
		sha = head // a recovered owner already committed
	} else if err != nil {
		return "", e.publishFailure(ctx, c, "commit", err)
	}
	if err := e.Store.RecordFinalCommit(ctx, c.AttemptID, sha); err != nil {
		return "", err
	}
	if err := e.append(ctx, c, "commit.created", "runner", map[string]any{
		"final_commit_sha": sha,
	}); err != nil {
		return "", err
	}

	if err := e.Workspaces.Push(ctx, ws, gitworkspace.PushParams{}); err != nil {
		return "", e.publishFailure(ctx, c, "push", err)
	}
	if err := e.append(ctx, c, "branch.pushed", "runner", map[string]any{
		"branch": ws.Branch,
	}); err != nil {
		return "", err
	}
	return e.publishToGitHub(ctx, c, t, pub, ws.Branch, ws.BaseSHA, sha)
}

// publishRecovered resumes publishing for an attempt claimed at status
// publishing: reattach the surviving worktree when it exists, otherwise
// publish from the already-pushed branch, otherwise the work is gone.
func (e *Executor) publishRecovered(ctx context.Context, c *Claim, t task.Task, pub *publishTarget) (task.Status, error) {
	if t.WorkingBranch == nil || t.BaseCommitSHA == nil {
		return "", e.failTask(ctx, c, "publish_state_missing",
			"task reached publishing without a recorded branch and base commit")
	}
	branch, base := *t.WorkingBranch, *t.BaseCommitSHA
	rc := pub.repo

	repoRef, err := e.repoRef(ctx, rc)
	if err != nil {
		return "", err
	}
	if ws, ok := e.Workspaces.Lookup(c.AttemptID, repoRef, branch, base); ok {
		// Refresh the mirror's stored credential before reusing its remote.
		if _, err := e.Workspaces.EnsureMirror(ctx, repoRef); err != nil {
			return "", err
		}
		return e.publishFromWorkspace(ctx, c, t, pub, ws, "")
	}

	head, err := e.GitHub.BranchHeadSHA(ctx, rc.InstallationID, rc.Owner, rc.Name, branch)
	var apiErr *github.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return "", e.failTask(ctx, c, "workspace_lost",
			"the workspace was lost before its work was pushed")
	}
	if err != nil {
		return "", e.publishFailure(ctx, c, "resolve pushed branch", err)
	}
	if err := e.Store.RecordFinalCommit(ctx, c.AttemptID, head); err != nil {
		return "", err
	}
	return e.publishToGitHub(ctx, c, t, pub, branch, base, head)
}

// publishToGitHub drives the GitHub surface from a pushed branch: one draft
// PR (found or created by head branch), one check run (found or created by
// external id), the issue comment, then awaiting_review. Every step is safe
// to replay.
func (e *Executor) publishToGitHub(ctx context.Context, c *Claim, t task.Task, pub *publishTarget, branch, baseSHA, finalSHA string) (task.Status, error) {
	if err := e.detectConflicts(ctx, c, t, baseSHA, finalSHA); err != nil {
		return "", err
	}
	rc := pub.repo
	report, markdown, err := e.storedReport(ctx, c.TaskID)
	if err != nil {
		return "", err
	}
	body := evidence.PRBody(report, finalSHA)

	pr, err := e.GitHub.FindPullRequestByHead(ctx, rc.InstallationID, rc.Owner,
		rc.Name, rc.Owner, branch)
	if err != nil {
		return "", e.publishFailure(ctx, c, "find pull request", err)
	}
	if pr == nil {
		created, err := e.GitHub.CreateDraftPullRequest(ctx, rc.InstallationID,
			rc.Owner, rc.Name, github.PullRequestParams{
				Title: t.Title, Head: branch, Base: t.BaseBranch, Body: body,
			})
		if err != nil {
			return "", e.publishFailure(ctx, c, "create pull request", err)
		}
		pr = &created
		if err := e.append(ctx, c, "pull_request.created", "runner", map[string]any{
			"number": pr.Number, "url": pr.HTMLURL, "draft": true,
		}); err != nil {
			return "", err
		}
	} else {
		if err := e.GitHub.UpdatePullRequestBody(ctx, rc.InstallationID,
			rc.Owner, rc.Name, pr.Number, body); err != nil {
			return "", e.publishFailure(ctx, c, "update pull request", err)
		}
		if err := e.append(ctx, c, "pull_request.updated", "runner", map[string]any{
			"number": pr.Number, "url": pr.HTMLURL,
		}); err != nil {
			return "", err
		}
	}
	if err := e.Store.RecordPullRequest(ctx, c.AttemptID, pr.Number); err != nil {
		return "", err
	}

	if err := e.upsertCheckRun(ctx, c, rc, finalSHA, github.CheckRunParams{
		Name:       github.CheckRunName,
		HeadSHA:    finalSHA,
		ExternalID: c.AttemptID,
		Status:     "completed",
		Conclusion: checkConclusion(report),
		Title:      "Agent Trail evidence",
		Summary:    truncateRunes(markdown, checkOutputLimit),
	}); err != nil {
		return "", err
	}

	if t.SourceIssueNumber != nil {
		comment := fmt.Sprintf(
			"Agent Trail opened draft pull request #%d for this issue. "+
				"The PR body carries the evidence report; the `%s` check "+
				"holds the verified results.", pr.Number, github.CheckRunName)
		if err := e.GitHub.CreateIssueComment(ctx, rc.InstallationID, rc.Owner,
			rc.Name, *t.SourceIssueNumber, comment); err != nil {
			return "", e.publishFailure(ctx, c, "post issue comment", err)
		}
		if err := e.append(ctx, c, "github.comment.posted", "runner", map[string]any{
			"kind": "published",
		}); err != nil {
			return "", err
		}
	}

	return e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
}

// publishNoChange settles a clean worktree: no PR, a neutral check on the
// base commit, an explaining comment, and a no_change failure that keeps
// the explanation (docs/architecture/publishing.md: empty diff means no
// pull request; the task is marked no-change via the failed state).
func (e *Executor) publishNoChange(ctx context.Context, c *Claim, t task.Task, pub *publishTarget, baseSHA, summary string) error {
	rc := pub.repo
	explanation := "the agent session ended without modifying the workspace"
	if summary != "" {
		explanation += "; agent summary: " + summary
	}
	if err := e.append(ctx, c, "publishing.no_change", "runner", map[string]any{
		"reason": explanation,
	}); err != nil {
		return err
	}
	if err := e.upsertCheckRun(ctx, c, rc, baseSHA, github.CheckRunParams{
		Name:       github.CheckRunName,
		HeadSHA:    baseSHA,
		ExternalID: c.AttemptID,
		Status:     "completed",
		Conclusion: "neutral",
		Title:      "No changes produced",
		Summary:    explanation,
	}); err != nil {
		return err
	}
	if t.SourceIssueNumber != nil {
		comment := "Agent Trail produced no changes for this issue, so no " +
			"pull request was opened: " + explanation + "."
		if err := e.GitHub.CreateIssueComment(ctx, rc.InstallationID, rc.Owner,
			rc.Name, *t.SourceIssueNumber, comment); err != nil {
			return e.publishFailure(ctx, c, "post no-change comment", err)
		}
		if err := e.append(ctx, c, "github.comment.posted", "runner", map[string]any{
			"kind": "no_change",
		}); err != nil {
			return err
		}
	}
	return e.failTask(ctx, c, "no_change", explanation)
}

// detectConflicts records warnings without failing publication.
func (e *Executor) detectConflicts(ctx context.Context, c *Claim, t task.Task, baseSHA, finalSHA string) error {
	if e.Conflicts == nil || t.RepositoryID == nil {
		return nil
	}
	repo := gitworkspace.RepoRef{ID: *t.RepositoryID}
	detections, err := e.Conflicts.Detect(ctx, repo, *t.RepositoryID, t.ID, baseSHA, finalSHA)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.Logger.LogAttrs(ctx, slog.LevelWarn, "conflict detection failed",
			slog.String("event", "conflict_detection_failed"),
			slog.String("task_id", t.ID),
			slog.String("error", err.Error()),
		)
		return nil
	}
	for _, det := range detections {
		if err := e.append(ctx, c, "conflict.detected", "runner", map[string]any{
			"other_task_id":    det.OtherTaskID,
			"other_task_title": det.OtherTaskTitle,
			"kinds":            det.Kinds,
			"files":            det.Files,
		}); err != nil {
			return err
		}
	}
	return nil
}

// upsertCheckRun creates the check run or, when a replayed publish already
// created one with this external id on the commit, updates it.
func (e *Executor) upsertCheckRun(ctx context.Context, c *Claim, rc github.RepositoryContext, sha string, p github.CheckRunParams) error {
	runs, err := e.GitHub.ListCheckRuns(ctx, rc.InstallationID, rc.Owner,
		rc.Name, sha, p.Name)
	if err != nil {
		return e.publishFailure(ctx, c, "list check runs", err)
	}
	for _, run := range runs {
		if run.ExternalID != p.ExternalID {
			continue
		}
		if err := e.GitHub.UpdateCheckRun(ctx, rc.InstallationID, rc.Owner,
			rc.Name, run.ID, p); err != nil {
			return e.publishFailure(ctx, c, "update check run", err)
		}
		return e.append(ctx, c, "github.check_run.updated", "runner", map[string]any{
			"check_run_id": run.ID, "conclusion": p.Conclusion,
		})
	}
	id, err := e.GitHub.CreateCheckRun(ctx, rc.InstallationID, rc.Owner, rc.Name, p)
	if err != nil {
		return e.publishFailure(ctx, c, "create check run", err)
	}
	return e.append(ctx, c, "github.check_run.created", "runner", map[string]any{
		"check_run_id": id, "head_sha": sha, "conclusion": p.Conclusion,
	})
}

// storedReport loads the attempt's stored evidence report and markdown.
func (e *Executor) storedReport(ctx context.Context, taskID string) (evidence.Report, string, error) {
	stored, err := e.Evidence.GetForTask(ctx, taskID)
	if err != nil {
		return evidence.Report{}, "", fmt.Errorf("load evidence: %w", err)
	}
	var report evidence.Report
	if err := json.Unmarshal(stored.Report, &report); err != nil {
		return evidence.Report{}, "", fmt.Errorf("decode evidence: %w", err)
	}
	return report, stored.SummaryMarkdown, nil
}

// checkConclusion maps trusted validation results to a check conclusion:
// any failed check is failure; checks that could not all run (or none ran)
// are neutral, never success (evidence over claims).
func checkConclusion(r evidence.Report) string {
	sawTrusted := false
	conclusion := "success"
	for _, v := range r.Validation {
		if !v.TrustedExecution {
			continue
		}
		sawTrusted = true
		switch validation.Status(v.Status) {
		case validation.StatusFailed:
			return "failure"
		case validation.StatusPassed:
		default:
			conclusion = "neutral"
		}
	}
	if !sawTrusted {
		return "neutral"
	}
	return conclusion
}

// publishFailure settles a failed publishing step. Lease loss and shutdown
// keep the attempt recoverable; anything else fails the task honestly.
func (e *Executor) publishFailure(ctx context.Context, c *Claim, step string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e.failTask(ctx, c, "publish_failed", step+": "+err.Error())
}

// truncateRunes bounds s to max bytes without splitting a rune.
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
