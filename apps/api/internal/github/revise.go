package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// bound for run and revision instructions alike; the composed text is truncated on a rune boundary
const instructionLimit = 100000

const notAgentTrailPullRequest = "This pull request is not managed by Agent Trail, so there is nothing to revise."

// one reviewer item with its text; the stored FeedbackItem omits the body
type feedbackEntry struct {
	item task.FeedbackItem
	body string
	hunk string
}

// every guard replies once and creates nothing; only a task awaiting review gains an attempt
func (p *Processor) handleRevise(ctx context.Context, d Delivery, ev issueCommentPayload, repo StoredRepository, reply func(string) error) (string, error) {
	instID := ev.Installation.ID
	number := ev.Issue.Number
	login := ev.Comment.User.Login
	if login == "" {
		return "", errors.New("issue_comment payload without commenter login")
	}

	pr, err := p.api.GetPullRequest(ctx, instID, repo.Owner, repo.Name, number)
	if err != nil {
		return "", fmt.Errorf("get pull request: %w", err)
	}
	// a fork can name any branch, so only heads pushed to this repository map to tasks
	if !gitworkspace.ValidBranch(pr.Head.Ref) || pr.Head.RepoID != repo.GitHubRepositoryID {
		return p.replyProcessed(reply, notAgentTrailPullRequest)
	}
	t, found, err := p.tasks.TaskForBranch(ctx, repo.ID, pr.Head.Ref)
	if err != nil {
		return "", err
	}
	if !found {
		return p.replyProcessed(reply, notAgentTrailPullRequest)
	}
	// redelivered command: its attempt already exists, whatever state the task is in now
	attempts, err := p.tasks.Attempts(ctx, t.ID)
	if err != nil {
		return "", err
	}
	for _, a := range attempts {
		if a.TriggerCommentID != nil && *a.TriggerCommentID == ev.Comment.ID {
			return "ignored", nil
		}
	}
	switch t.Phase {
	case task.PhaseTerminal:
		return p.replyProcessed(reply, fmt.Sprintf(
			"Task `%s` is %s and cannot be revised. Close this pull request and "+
				"run the issue again to start a new task.", t.ID, t.Status))
	case task.PhasePending, task.PhaseRunning:
		return p.replyProcessed(reply, fmt.Sprintf(
			"Task `%s` is still running (%s). Wait for it to publish before "+
				"requesting a revision.", t.ID, t.Status))
	}
	if !gitworkspace.ValidSHA(pr.Head.SHA) {
		return "", fmt.Errorf("pull request #%d head sha %q is not a commit", number, pr.Head.SHA)
	}

	// feedback up to the latest revision's trigger was already given to it; the first revision takes everything
	var cutoff *time.Time
	if last := attempts[len(attempts)-1]; last.Number > 1 {
		at := last.CreatedAt
		cutoff = &at
	}
	entries, err := p.gatherFeedback(ctx, instID, repo, number, cutoff, ev)
	if err != nil {
		return "", err
	}
	items := make([]task.FeedbackItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.item)
	}
	limit := repo.Settings.MaxAttempts
	revised, attempt, err := p.tasks.RequestRevision(ctx, t.ID, task.RevisionParams{
		Instructions:     composeRevisionInstructions(t, entries),
		BaseCommitSHA:    pr.Head.SHA,
		RequestedByLogin: login,
		TriggerCommentID: ev.Comment.ID,
		Feedback:         items,
		MaxAttempts:      limit,
		IdempotencyKey:   fmt.Sprintf("revise:%d", ev.Comment.ID),
		Reason: fmt.Sprintf("revision requested by @%s in pull request #%d",
			login, number),
	})
	var invalid *task.InvalidTransitionError
	switch {
	case errors.Is(err, task.ErrRevisionReplayed):
		return "ignored", nil
	case errors.Is(err, task.ErrRevisionLimit):
		return p.replyProcessed(reply, fmt.Sprintf(
			"Task `%s` has reached the repository revision limit of %d attempts. "+
				"Close this pull request and run the issue again to start a new task.",
			t.ID, limit))
	case errors.As(err, &invalid):
		// task left review between the lookup and the row lock
		return p.replyProcessed(reply, fmt.Sprintf(
			"Task `%s` is %s and cannot be revised right now.", t.ID, invalid.From))
	case err != nil:
		return "", fmt.Errorf("request revision: %w", err)
	}
	p.logger.LogAttrs(ctx, slog.LevelInfo, "revision requested from pull request comment",
		slog.String("event", "github_revision_requested"),
		slog.String("trace_id", d.TraceID),
		slog.String("delivery_id", d.ID),
		slog.String("task_id", revised.ID),
		slog.Int("attempt_number", attempt.Number),
		slog.String("repository", repo.FullName),
		slog.Int64("pull_request", number),
		slog.String("commenter", login),
		slog.Int("feedback_items", len(items)),
	)

	// side effects after the durable attempt: failures logged, never unwind the attempt
	p.createTriggerCheckRun(ctx, d, instID, repo, revised.ID, pr.Head.SHA)
	ack := fmt.Sprintf(
		"Agent Trail queued revision attempt %d for this pull request "+
			"(requested by @%s with %d review feedback item(s)). The `%s` check tracks progress.",
		attempt.Number, login, len(items), CheckRunName)
	if err := reply(ack); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "ack comment failed",
			slog.String("event", "github_ack_comment_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("task_id", revised.ID),
			slog.String("error", err.Error()),
		)
	} else {
		p.appendTaskEvent(ctx, revised.ID, "github.comment.posted", map[string]string{
			"kind": "revision_ack", "pull_request": strconv.FormatInt(number, 10),
		})
	}
	return "processed", nil
}

func (p *Processor) replyProcessed(reply func(string) error, body string) (string, error) {
	if err := reply(body); err != nil {
		return "", fmt.Errorf("post reply: %w", err)
	}
	return "processed", nil
}

// human items after the cutoff, oldest first; the revise comment itself is the last entry.
// other addressed commands (refused or replayed) are noise, not feedback
func (p *Processor) gatherFeedback(ctx context.Context, instID int64, repo StoredRepository, number int64, cutoff *time.Time, ev issueCommentPayload) ([]feedbackEntry, error) {
	// github stamps to the second, so the cutoff is the publish second, inclusive
	var since time.Time
	if cutoff != nil {
		since = cutoff.UTC().Truncate(time.Second)
	}
	after := func(at time.Time) bool { return cutoff == nil || !at.Before(since) }

	var entries []feedbackEntry
	reviews, err := p.api.ListPullRequestReviews(ctx, instID, repo.Owner, repo.Name, number)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	for _, r := range reviews {
		if r.User.Bot() || strings.TrimSpace(r.Body) == "" || !after(r.SubmittedAt) {
			continue
		}
		entries = append(entries, feedbackEntry{
			item: task.FeedbackItem{Kind: task.FeedbackReview, Author: r.User.Login,
				PostedAt: r.SubmittedAt, Location: strings.ToLower(r.State)},
			body: r.Body,
		})
	}
	comments, err := p.api.ListPullRequestReviewComments(ctx, instID, repo.Owner, repo.Name, number, since)
	if err != nil {
		return nil, fmt.Errorf("list review comments: %w", err)
	}
	for _, c := range comments {
		if c.User.Bot() || strings.TrimSpace(c.Body) == "" || !after(c.CreatedAt) {
			continue
		}
		entries = append(entries, feedbackEntry{
			item: task.FeedbackItem{Kind: task.FeedbackReviewComment, Author: c.User.Login,
				PostedAt: c.CreatedAt, Location: reviewCommentLocation(c)},
			body: c.Body,
			hunk: c.DiffHunk,
		})
	}
	issueComments, err := p.api.ListIssueComments(ctx, instID, repo.Owner, repo.Name, number, since)
	if err != nil {
		return nil, fmt.Errorf("list issue comments: %w", err)
	}
	for _, c := range issueComments {
		if c.User.Bot() || c.ID == ev.Comment.ID || strings.TrimSpace(c.Body) == "" ||
			!after(c.CreatedAt) || ParseCommand(c.Body).Addressed {
			continue
		}
		entries = append(entries, feedbackEntry{
			item: task.FeedbackItem{Kind: task.FeedbackComment, Author: c.User.Login, PostedAt: c.CreatedAt},
			body: c.Body,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].item.PostedAt.Before(entries[j].item.PostedAt)
	})
	postedAt := ev.Comment.CreatedAt
	if postedAt.IsZero() {
		postedAt = time.Now().UTC()
	}
	entries = append(entries, feedbackEntry{
		item: task.FeedbackItem{Kind: task.FeedbackReviseCommand, Author: ev.Comment.User.Login,
			PostedAt: postedAt},
		body: ev.Comment.Body,
	})
	return entries, nil
}

// outdated comments keep original_line; a comment on a deleted file keeps only its path
func reviewCommentLocation(c ReviewComment) string {
	line := c.Line
	if line == nil {
		line = c.OriginalLine
	}
	if line == nil {
		return c.Path
	}
	return fmt.Sprintf("%s:%d", c.Path, *line)
}

func composeRevisionInstructions(t task.Task, entries []feedbackEntry) string {
	var b strings.Builder
	b.WriteString(t.Title + "\n\n" + t.Instructions + "\n\n---\n")
	b.WriteString("Revision: the working branch already contains the previous attempts' work. " +
		"Continue from the branch as it is, never start over, and address the review feedback below.\n")
	if len(entries) > 1 {
		b.WriteString("\nReview feedback, oldest first:\n")
	}
	for i, e := range entries {
		if e.item.Kind == task.FeedbackReviseCommand {
			fmt.Fprintf(&b, "\n---\nRevise command by @%s:\n\n%s\n", e.item.Author, e.body)
			continue
		}
		fmt.Fprintf(&b, "\n%d. [%s by @%s at %s", i+1, e.item.Kind.Label(),
			e.item.Author, e.item.PostedAt.UTC().Format(time.RFC3339))
		if e.item.Location != "" {
			fmt.Fprintf(&b, " on %s", e.item.Location)
		}
		b.WriteString("]\n")
		if e.hunk != "" {
			fmt.Fprintf(&b, "```diff\n%s\n```\n", e.hunk)
		}
		b.WriteString(e.body + "\n")
	}
	return truncateUTF8(b.String(), instructionLimit)
}
