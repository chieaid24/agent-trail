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

type RepositoryResolver interface {
	RepositoryContextByID(ctx context.Context, repositoryID string) (github.RepositoryContext, error)
}

// github caps check output at 65535 chars
const checkOutputLimit = 60000

type publishTarget struct {
	repo github.RepositoryContext
}

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

// first recorded base/branch win so a recovered attempt reuses them
func (e *Executor) provisionWorkspace(ctx context.Context, c *Claim, t task.Task, pub *publishTarget) (gitworkspace.Workspace, error) {
	attempt, err := e.Tasks.Attempt(ctx, c.AttemptID)
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	if attempt.Number > 1 {
		return e.provisionRevision(ctx, c, t, pub, attempt)
	}
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

	repoRef, err := e.repoRef(ctx, c, rc)
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	params := gitworkspace.CreateParams{
		Repo:        repoRef,
		AttemptID:   c.AttemptID,
		BaseSHA:     base,
		BranchLabel: strings.TrimPrefix(branch, gitworkspace.BranchPrefix),
	}
	return e.createWorktreeRetryingStale(ctx, c, repoRef, branch, params)
}

// continues the pushed branch from the head recorded at trigger; a moved tip fails the attempt,
// so the platform never rebases and never force-pushes (ADR 0001)
func (e *Executor) provisionRevision(ctx context.Context, c *Claim, t task.Task, pub *publishTarget, attempt task.Attempt) (gitworkspace.Workspace, error) {
	if t.WorkingBranch == nil || attempt.BaseCommitSHA == nil {
		return gitworkspace.Workspace{}, e.failTask(ctx, c, "publish_state_missing",
			"revision attempt has no recorded working branch or base commit")
	}
	branch, base := *t.WorkingBranch, *attempt.BaseCommitSHA
	repoRef, err := e.repoRef(ctx, c, pub.repo)
	if err != nil {
		return gitworkspace.Workspace{}, err
	}
	params := gitworkspace.CreateParams{
		Repo:             repoRef,
		AttemptID:        c.AttemptID,
		BaseSHA:          base,
		BranchLabel:      strings.TrimPrefix(branch, gitworkspace.BranchPrefix),
		RequireBranchTip: true,
	}
	ws, err := e.createWorktreeRetryingStale(ctx, c, repoRef, branch, params)
	if errors.Is(err, gitworkspace.ErrBranchTipMoved) {
		if ctx.Err() != nil {
			return gitworkspace.Workspace{}, context.Cause(ctx)
		}
		return gitworkspace.Workspace{}, e.failTask(ctx, c, "base_mismatch", err.Error())
	}
	return ws, err
}

// dead prior owner may have left worktree/branch behind: clear both, retry once; cleanup failure must not mask err
func (e *Executor) createWorktreeRetryingStale(ctx context.Context, c *Claim, repoRef gitworkspace.RepoRef, branch string, params gitworkspace.CreateParams) (gitworkspace.Workspace, error) {
	ws, err := e.createWorktree(ctx, c, params)
	if err == nil || errors.Is(err, gitworkspace.ErrBranchTipMoved) {
		return ws, err
	}
	if fenceErr := e.fenceLeaseOwnership(ctx, c); fenceErr != nil {
		return gitworkspace.Workspace{}, errors.Join(err,
			fmt.Errorf("fence stale workspace cleanup: %w", fenceErr))
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), e.leaseOperationTimeout())
	defer cleanupCancel()
	if cleanupErr := e.Workspaces.CleanupStale(cleanupCtx, repoRef, c.AttemptID, branch); cleanupErr != nil {
		return gitworkspace.Workspace{}, errors.Join(err, cleanupErr)
	}
	return e.createWorktree(ctx, c, params)
}

// fresh installation token embedded in clone url; never logged, redacted from errors
func (e *Executor) repoRef(ctx context.Context, c *Claim, rc github.RepositoryContext) (gitworkspace.RepoRef, error) {
	tokenCtx, span := startSpan(ctx, "github.token_exchange", c)
	token, err := e.GitHub.InstallationToken(tokenCtx, rc.InstallationID)
	endSpan(span, err)
	if err != nil {
		return gitworkspace.RepoRef{}, fmt.Errorf("mint installation token: %w", err)
	}
	cloneURL, err := credentialedCloneURL(rc.CloneURL, token)
	if err != nil {
		return gitworkspace.RepoRef{}, err
	}
	return gitworkspace.RepoRef{ID: rc.ID, CloneURL: cloneURL}, nil
}

func (e *Executor) createWorktree(ctx context.Context, c *Claim, p gitworkspace.CreateParams) (gitworkspace.Workspace, error) {
	fetchCtx, span := startSpan(ctx, "git.fetch", c)
	ws, err := e.Workspaces.CreateWorktree(fetchCtx, p)
	endSpan(span, err)
	return ws, err
}

// non-https (test mirrors) pass through; github only serves https so production always carries the credential
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

// deterministic so retries/recovered owners land on one branch; task-id suffix avoids per-issue collisions
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

// clean tree at base = no-change; clean tree with moved head = recovered owner's commit
func (e *Executor) publishFromWorkspace(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, pub *publishTarget, ws gitworkspace.Workspace, summary string) (task.Status, error) {
	attempt, err := e.Tasks.Attempt(ctx, c.AttemptID)
	if err != nil {
		return "", err
	}
	if attempt.BaseCommitSHA != nil && ws.BaseSHA != *attempt.BaseCommitSHA {
		return "", e.failTask(ctx, c, "base_mismatch", fmt.Sprintf(
			"workspace base %s does not match recorded base %s",
			ws.BaseSHA, *attempt.BaseCommitSHA))
	}

	provider := e.Adapter.Name()
	if t.AgentProvider != nil {
		provider = *t.AgentProvider
	}
	model := ""
	if t.AgentModel != nil {
		model = *t.AgentModel
	}
	message := t.Title
	if attempt.Number > 1 {
		message = fmt.Sprintf("%s (attempt %d)", t.Title, attempt.Number)
	}
	requestedBy := ""
	if attempt.RequestedByLogin != nil {
		requestedBy = *attempt.RequestedByLogin
	}
	sha, err := e.Workspaces.Commit(ctx, ws, gitworkspace.CommitParams{
		Message:     message,
		TaskID:      t.ID,
		Provider:    provider,
		Model:       model,
		RequestedBy: requestedBy,
	})
	if errors.Is(err, gitworkspace.ErrNothingToCommit) {
		head, headErr := e.Workspaces.Head(ctx, ws)
		if headErr != nil {
			return "", headErr
		}
		if head == ws.BaseSHA {
			return "", e.publishNoChange(ctx, c, t, pub, attempt, ws.BaseSHA, summary)
		}
		sha = head
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

	pushCtx, pushSpan := startSpan(ctx, "git.push", c)
	err = e.Workspaces.Push(pushCtx, ws, gitworkspace.PushParams{})
	endSpan(pushSpan, err)
	if err != nil {
		return "", e.publishFailure(ctx, c, "push", err)
	}
	if err := e.append(ctx, c, "branch.pushed", "runner", map[string]any{
		"branch": ws.Branch,
	}); err != nil {
		return "", err
	}
	return e.publishToGitHub(ctx, log, c, t, pub, attempt, ws.Branch, ws.BaseSHA, sha)
}

// reattach surviving worktree, else publish from pushed branch, else the work is gone
func (e *Executor) publishRecovered(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, pub *publishTarget) (st task.Status, retErr error) {
	attempt, err := e.Tasks.Attempt(ctx, c.AttemptID)
	if err != nil {
		return "", err
	}
	// a revision's base is the pull request head at trigger, recorded on the attempt
	baseSHA := attempt.BaseCommitSHA
	if baseSHA == nil {
		baseSHA = t.BaseCommitSHA
	}
	if t.WorkingBranch == nil || baseSHA == nil {
		return "", e.failTask(ctx, c, "publish_state_missing",
			"task reached publishing without a recorded branch and base commit")
	}
	branch, base := *t.WorkingBranch, *baseSHA
	rc := pub.repo

	repoRef, err := e.repoRef(ctx, c, rc)
	if err != nil {
		return "", err
	}
	if ws, ok := e.Workspaces.Lookup(c.AttemptID, repoRef, branch, base); ok {
		workspaceCleaned := false
		defer func() {
			if !workspaceCleaned {
				retErr = e.cleanupGitWorkspace(ctx, log, c, ws, retErr)
			}
		}()
		// refresh mirror's stored credential before reusing its remote
		fetchCtx, span := startSpan(ctx, "git.fetch", c)
		_, err := e.Workspaces.EnsureMirror(fetchCtx, repoRef)
		endSpan(span, err)
		if err != nil {
			return "", err
		}
		if _, err := e.publishFromWorkspace(ctx, log, c, t, pub, ws, ""); err != nil {
			return "", err
		}
		if err := e.cleanupGitWorkspace(ctx, log, c, ws, nil); err != nil {
			return "", err
		}
		workspaceCleaned = true
		return e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
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
	// branch still at the attempt base: the prior owner never pushed its commit
	if head == base {
		return "", e.failTask(ctx, c, "workspace_lost",
			"the workspace was lost before its work was pushed")
	}
	if err := e.Store.RecordFinalCommit(ctx, c.AttemptID, head); err != nil {
		return "", err
	}
	if _, err := e.publishToGitHub(ctx, log, c, t, pub, attempt, branch, base, head); err != nil {
		return "", err
	}
	return e.transition(ctx, c, task.StatusAwaitingReview, "runner", "")
}

// every step replay-safe: pr found-or-created by head branch, check run by external id
func (e *Executor) publishToGitHub(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, pub *publishTarget, attempt task.Attempt, branch, baseSHA, finalSHA string) (task.Status, error) {
	if err := e.detectConflicts(ctx, log, c, t, pub.repo, baseSHA, finalSHA); err != nil {
		return "", err
	}
	rc := pub.repo
	report, markdown, err := e.storedReport(ctx, c.AttemptID)
	if err != nil {
		return "", err
	}
	history, err := e.Evidence.AttemptHistory(ctx, c.TaskID)
	if err != nil {
		return "", fmt.Errorf("load attempt history: %w", err)
	}
	body := evidence.PRBody(report, finalSHA, history)

	prCtx, prSpan := startSpan(ctx, "github.pr_create", c)
	pr, err := e.GitHub.FindPullRequestByHead(prCtx, rc.InstallationID, rc.Owner,
		rc.Name, rc.Owner, branch)
	if err != nil {
		endSpan(prSpan, err)
		return "", e.publishFailure(ctx, c, "find pull request", err)
	}
	wasCreated := pr == nil
	if pr == nil {
		created, err := e.GitHub.CreateDraftPullRequest(prCtx, rc.InstallationID,
			rc.Owner, rc.Name, github.PullRequestParams{
				Title: t.Title, Head: branch, Base: t.BaseBranch, Body: body,
			})
		if err != nil {
			endSpan(prSpan, err)
			return "", e.publishFailure(ctx, c, "create pull request", err)
		}
		pr = &created
	} else {
		if err := e.GitHub.UpdatePullRequestBody(prCtx, rc.InstallationID,
			rc.Owner, rc.Name, pr.Number, body); err != nil {
			endSpan(prSpan, err)
			return "", e.publishFailure(ctx, c, "update pull request", err)
		}
	}
	endSpan(prSpan, nil)
	if wasCreated {
		if err := e.append(ctx, c, "pull_request.created", "runner", map[string]any{
			"number": pr.Number, "url": pr.HTMLURL, "draft": true,
		}); err != nil {
			return "", err
		}
	} else {
		if err := e.append(ctx, c, "pull_request.updated", "runner", map[string]any{
			"number": pr.Number, "url": pr.HTMLURL,
		}); err != nil {
			return "", err
		}
	}
	if err := e.Store.RecordPullRequest(ctx, c.AttemptID, pr.Number); err != nil {
		return "", err
	}

	conclusion := checkConclusion(report)
	if err := e.upsertCheckRun(ctx, c, rc, finalSHA, github.CheckRunParams{
		Name:       github.CheckRunName,
		HeadSHA:    finalSHA,
		ExternalID: c.AttemptID,
		Status:     "completed",
		Conclusion: conclusion,
		Title:      "Agent Trail evidence",
		Summary:    truncateRunes(markdown, checkOutputLimit),
	}); err != nil {
		return "", err
	}
	if err := e.completeTriggerCheckRun(ctx, log, c, rc, attempt, github.CheckRunParams{
		Conclusion: conclusion,
		Title:      "Agent Trail evidence",
		Summary:    truncateRunes(markdown, checkOutputLimit),
	}); err != nil {
		return "", err
	}

	if attempt.Number > 1 {
		return task.StatusPublishing, e.postRevisionSummary(ctx, c, rc, attempt, pr.Number, finalSHA, report)
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

	return task.StatusPublishing, nil
}

// one comment per revision on the pull request itself; never a reply inside a review thread
func (e *Executor) postRevisionSummary(ctx context.Context, c *Claim, rc github.RepositoryContext, attempt task.Attempt, prNumber int64, finalSHA string, report evidence.Report) error {
	posted, err := e.commentPosted(ctx, c, "revision_summary")
	if err != nil {
		return err
	}
	if posted {
		return nil
	}
	comment := revisionSummary(attempt, finalSHA, report)
	if err := e.GitHub.CreateIssueComment(ctx, rc.InstallationID, rc.Owner, rc.Name, prNumber, comment); err != nil {
		return e.publishFailure(ctx, c, "post revision summary", err)
	}
	return e.append(ctx, c, "github.comment.posted", "runner", map[string]any{
		"kind": "revision_summary", "pull_request": prNumber,
	})
}

// a recovered owner must not repeat a comment the dead owner already posted
func (e *Executor) commentPosted(ctx context.Context, c *Claim, kind string) (bool, error) {
	events, err := e.Tasks.Events(ctx, c.TaskID, evidenceEventLimit)
	if err != nil {
		return false, fmt.Errorf("check posted comments: %w", err)
	}
	for _, ev := range events {
		if ev.TaskAttemptID != c.AttemptID || ev.EventType != "github.comment.posted" {
			continue
		}
		var p struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil && p.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

func revisionSummary(attempt task.Attempt, finalSHA string, report evidence.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent Trail published revision attempt %d.\n\n", attempt.Number)
	fmt.Fprintf(&b, "- Final commit: `%s`\n", finalSHA)
	fmt.Fprintf(&b, "- Validation: %s\n", validationOutcome(report))
	fmt.Fprintf(&b, "- Review feedback given: %d item(s)\n", len(attempt.Feedback))
	for _, item := range attempt.Feedback {
		fmt.Fprintf(&b, "  - %s by @%s at %s", feedbackKindLabel(item.Kind), item.Author,
			item.PostedAt.UTC().Format("2006-01-02T15:04:05Z"))
		if item.Location != "" {
			fmt.Fprintf(&b, " on %s", item.Location)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nThe pull request body carries the updated evidence report and attempts "+
		"history; the `%s` check holds the verified results.", github.CheckRunName)
	return b.String()
}

func feedbackKindLabel(kind string) string {
	switch kind {
	case "review":
		return "review"
	case "review_comment":
		return "inline review comment"
	case "revise_command":
		return "revise command"
	default:
		return "comment"
	}
}

// counts only platform-executed checks; agent claims never count as verification
func validationOutcome(r evidence.Report) string {
	trusted, passed, failed := 0, 0, 0
	for _, v := range r.Validation {
		if !v.TrustedExecution {
			continue
		}
		trusted++
		switch validation.Status(v.Status) {
		case validation.StatusPassed:
			passed++
		case validation.StatusFailed:
			failed++
		}
	}
	switch {
	case trusted == 0:
		return "no trusted checks ran"
	case failed > 0:
		return fmt.Sprintf("failed (%d of %d trusted checks failed)", failed, trusted)
	case passed == trusted:
		return fmt.Sprintf("passed (%d trusted checks)", trusted)
	default:
		return fmt.Sprintf("incomplete (%d of %d trusted checks passed)", passed, trusted)
	}
}

// the queued trigger check run gains the attempt's conclusion; api failure logs, never blocks
func (e *Executor) completeTriggerCheckRun(ctx context.Context, log *slog.Logger, c *Claim, rc github.RepositoryContext, attempt task.Attempt, p github.CheckRunParams) error {
	if attempt.TriggerCheckRunID == nil || attempt.TriggerCheckRunCompletedAt != nil {
		return nil
	}
	p.Name = github.CheckRunName
	p.Status = "completed"
	id := *attempt.TriggerCheckRunID
	if err := e.GitHub.UpdateCheckRun(ctx, rc.InstallationID, rc.Owner, rc.Name, id, p); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.LogAttrs(ctx, slog.LevelWarn, "trigger check run completion failed",
			slog.String("event", "github_trigger_check_run_failed"),
			slog.Int64("check_run_id", id),
			slog.String("error", err.Error()),
		)
		return nil
	}
	if err := e.Tasks.MarkTriggerCheckRunCompleted(ctx, c.AttemptID); err != nil {
		return err
	}
	return e.append(ctx, c, "github.check_run.updated", "runner", map[string]any{
		"check_run_id": id, "conclusion": p.Conclusion, "kind": "trigger",
	})
}

// no new commit; neutral check on the base commit; no_change failed state keeps the explanation.
// a revision without changes fails the same way and says so on the pull request it was asked on
func (e *Executor) publishNoChange(ctx context.Context, c *Claim, t task.Task, pub *publishTarget, attempt task.Attempt, baseSHA, summary string) error {
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
	if err := e.completeTriggerCheckRun(ctx, e.Logger, c, rc, attempt, github.CheckRunParams{
		Conclusion: "neutral",
		Title:      "No changes produced",
		Summary:    explanation,
	}); err != nil {
		return err
	}
	if attempt.Number > 1 && t.WorkingBranch != nil {
		pr, err := e.GitHub.FindPullRequestByHead(ctx, rc.InstallationID, rc.Owner,
			rc.Name, rc.Owner, *t.WorkingBranch)
		if err != nil {
			return e.publishFailure(ctx, c, "find pull request", err)
		}
		if pr != nil {
			if err := e.Store.RecordPullRequest(ctx, c.AttemptID, pr.Number); err != nil {
				return err
			}
			comment := fmt.Sprintf("Agent Trail produced no changes for revision attempt %d, "+
				"so the branch is unchanged: %s.", attempt.Number, explanation)
			if err := e.GitHub.CreateIssueComment(ctx, rc.InstallationID, rc.Owner,
				rc.Name, pr.Number, comment); err != nil {
				return e.publishFailure(ctx, c, "post no-change comment", err)
			}
			if err := e.append(ctx, c, "github.comment.posted", "runner", map[string]any{
				"kind": "no_change", "pull_request": pr.Number,
			}); err != nil {
				return err
			}
		}
		return e.failTask(ctx, c, "no_change", explanation)
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

// detection failure must not block publishing
func (e *Executor) detectConflicts(ctx context.Context, log *slog.Logger, c *Claim, t task.Task, rc github.RepositoryContext, baseSHA, finalSHA string) error {
	if e.Conflicts == nil || t.RepositoryID == nil {
		return nil
	}
	repo, err := e.repoRef(ctx, c, rc)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.LogAttrs(ctx, slog.LevelWarn, "conflict detection failed",
			slog.String("event", "conflict_detection_failed"),
			slog.String("error", err.Error()),
		)
		return nil
	}
	detector := *e.Conflicts
	detector.Logger = log
	detections, err := detector.Detect(ctx, repo, *t.RepositoryID, t.ID, t.Title, baseSHA, finalSHA)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.LogAttrs(ctx, slog.LevelWarn, "conflict detection failed",
			slog.String("event", "conflict_detection_failed"),
			slog.String("error", err.Error()),
		)
		return nil
	}
	for _, det := range detections {
		if err := e.append(ctx, c, "conflict.detected", "runner", map[string]any{
			"other_task_id":        det.OtherTaskID,
			"other_task_title":     det.OtherTaskTitle,
			"kinds":                det.Kinds,
			"files":                det.Files,
			"semantic_severity":    det.SemanticSeverity,
			"semantic_explanation": det.SemanticExplanation,
			"semantic_evidence":    det.SemanticEvidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

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

func (e *Executor) storedReport(ctx context.Context, attemptID string) (evidence.Report, string, error) {
	stored, err := e.Evidence.GetForAttempt(ctx, attemptID)
	if err != nil {
		return evidence.Report{}, "", fmt.Errorf("load evidence: %w", err)
	}
	var report evidence.Report
	if err := json.Unmarshal(stored.Report, &report); err != nil {
		return evidence.Report{}, "", fmt.Errorf("decode evidence: %w", err)
	}
	return report, stored.SummaryMarkdown, nil
}

// any failed check -> failure; not-all-ran or none ran -> neutral, never success
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

// lease loss / shutdown keep the attempt recoverable; anything else fails the task
func (e *Executor) publishFailure(ctx context.Context, c *Claim, step string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e.failTask(ctx, c, "publish_failed", step+": "+err.Error())
}

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
