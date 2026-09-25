package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/chieaid24/agent-trail/apps/api/internal/gitworkspace"
	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const CheckRunName = "Agent Trail Task"

const processTimeout = 30 * time.Second

type Delivery struct {
	ID        string
	EventType string
	Action    string
	TraceID   string
}

type TaskService interface {
	Create(ctx context.Context, p task.CreateParams) (task.Task, error)
	ActiveTaskForIssue(ctx context.Context, repositoryID string, issueNumber int64) (task.Task, bool, error)
	TaskForBranch(ctx context.Context, repositoryID, workingBranch string) (task.Task, bool, error)
	Transition(ctx context.Context, id string, p task.TransitionParams) (task.Task, error)
	RequestRevision(ctx context.Context, id string, p task.RevisionParams) (task.Task, task.Attempt, error)
	Attempts(ctx context.Context, taskID string) ([]task.Attempt, error)
	PublishedAt(ctx context.Context, taskID string) (*time.Time, error)
	RecordTriggerCheckRun(ctx context.Context, taskID string, checkRunID int64) error
	AppendEvent(ctx context.Context, taskID, eventType, source string, payload map[string]string) error
}

type API interface {
	ListInstallationRepositories(ctx context.Context, installationID int64) ([]Repository, error)
	CollaboratorPermission(ctx context.Context, installationID int64, owner, repo, username string) (string, error)
	BranchHeadSHA(ctx context.Context, installationID int64, owner, repo, branch string) (string, error)
	CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, issueNumber int64, body string) error
	CreateCheckRun(ctx context.Context, installationID int64, owner, repo string, p CheckRunParams) (int64, error)
	GetPullRequest(ctx context.Context, installationID int64, owner, repo string, number int64) (PullRequestDetail, error)
	ListPullRequestReviews(ctx context.Context, installationID int64, owner, repo string, number int64) ([]Review, error)
	ListPullRequestReviewComments(ctx context.Context, installationID int64, owner, repo string, number int64, since time.Time) ([]ReviewComment, error)
	ListIssueComments(ctx context.Context, installationID int64, owner, repo string, number int64, since time.Time) ([]IssueComment, error)
}

type Processor struct {
	store  *Store
	tasks  TaskService
	api    API
	logger *slog.Logger

	tasksCreated *observability.Counter

	wg sync.WaitGroup
}

func NewProcessor(store *Store, tasks TaskService, api API, logger *slog.Logger, metrics *observability.Registry) *Processor {
	return &Processor{
		store:  store,
		tasks:  tasks,
		api:    api,
		logger: logger,
		tasksCreated: metrics.Counter("agent_trail_task_created_total",
			"Tasks created."),
	}
}

func (p *Processor) Dispatch(d Delivery, payload []byte) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				p.logger.LogAttrs(context.Background(), slog.LevelError,
					"delivery processing panicked",
					slog.String("event", "webhook_process_panicked"),
					slog.String("trace_id", d.TraceID),
					slog.String("delivery_id", d.ID),
					slog.String("panic", fmt.Sprint(r)),
				)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
		defer cancel()
		pctx := observability.WithTraceParent(
			observability.WithTraceID(ctx, d.TraceID), d.TraceID)
		pctx, span := observability.Tracer().Start(pctx, "webhook.process",
			trace.WithAttributes(
				attribute.String("github.delivery_id", d.ID),
				attribute.String("github.event", d.EventType),
			))
		defer span.End()
		p.process(pctx, d, payload)
	}()
}

func (p *Processor) Wait() { p.wg.Wait() }

func (p *Processor) process(ctx context.Context, d Delivery, payload []byte) {
	status, err := p.handle(ctx, d, payload)
	failure := ""
	if err != nil {
		status = "failed"
		failure = err.Error()
		p.logger.LogAttrs(ctx, slog.LevelError, "delivery processing failed",
			slog.String("event", "webhook_process_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("delivery_id", d.ID),
			slog.String("event_type", d.EventType),
			slog.String("error", failure),
		)
	} else {
		p.logger.LogAttrs(ctx, slog.LevelInfo, "delivery processed",
			slog.String("event", "webhook_processed"),
			slog.String("trace_id", d.TraceID),
			slog.String("delivery_id", d.ID),
			slog.String("event_type", d.EventType),
			slog.String("status", status),
		)
	}
	// fresh deadline: ledger update must land even if handle spent the budget, else row stuck pending forever
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := p.store.MarkDelivery(markCtx, d.ID, status, failure); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelError, "delivery status not recorded",
			slog.String("event", "webhook_mark_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("delivery_id", d.ID),
			slog.String("error", err.Error()),
		)
	}
}

func (p *Processor) handle(ctx context.Context, d Delivery, payload []byte) (string, error) {
	switch d.EventType {
	case "installation":
		return p.handleInstallation(ctx, d, payload)
	case "installation_repositories":
		return p.handleInstallationRepositories(ctx, payload)
	case "issue_comment":
		return p.handleIssueComment(ctx, d, payload)
	case "pull_request":
		return p.handlePullRequest(ctx, d, payload)
	default:
		return "ignored", nil
	}
}

type installationPayload struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		Permissions map[string]string `json:"permissions"`
		Events      []string          `json:"events"`
	} `json:"installation"`
}

func (p *Processor) handleInstallation(ctx context.Context, d Delivery, payload []byte) (string, error) {
	var ev installationPayload
	if err := json.Unmarshal(payload, &ev); err != nil {
		return "", fmt.Errorf("parse installation payload: %w", err)
	}
	id := ev.Installation.ID
	if id == 0 {
		return "", errors.New("installation payload without installation id")
	}

	switch ev.Action {
	case "created", "new_permissions_accepted", "unsuspend":
		if err := p.syncInstallation(ctx, ev); err != nil {
			return "", err
		}
		return "processed", nil
	case "suspend":
		if err := p.store.SetInstallationSuspended(ctx, id, true); err != nil {
			return "", err
		}
		return "processed", nil
	case "deleted":
		if err := p.store.DeleteInstallation(ctx, id); err != nil {
			return "", err
		}
		return "processed", nil
	default:
		return "ignored", nil
	}
}

// webhook payloads lack default_branch/clone_url; api list is source of truth
func (p *Processor) syncInstallation(ctx context.Context, ev installationPayload) error {
	err := p.store.UpsertInstallation(ctx, InstallationParams{
		GitHubInstallationID: ev.Installation.ID,
		AccountID:            ev.Installation.Account.ID,
		AccountLogin:         ev.Installation.Account.Login,
		AccountType:          ev.Installation.Account.Type,
		Permissions:          ev.Installation.Permissions,
		Events:               ev.Installation.Events,
	})
	if err != nil {
		return err
	}
	repos, err := p.api.ListInstallationRepositories(ctx, ev.Installation.ID)
	if err != nil {
		return fmt.Errorf("list installation repositories: %w", err)
	}
	return p.store.SyncRepositories(ctx, ev.Installation.ID, repos)
}

func (p *Processor) handleInstallationRepositories(ctx context.Context, payload []byte) (string, error) {
	var ev installationPayload
	if err := json.Unmarshal(payload, &ev); err != nil {
		return "", fmt.Errorf("parse installation_repositories payload: %w", err)
	}
	if ev.Installation.ID == 0 {
		return "", errors.New("installation_repositories payload without installation id")
	}
	// added and removed both resync the full list
	if err := p.syncInstallation(ctx, ev); err != nil {
		return "", err
	}
	return "processed", nil
}

type issueCommentPayload struct {
	Action  string `json:"action"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	} `json:"comment"`
	Issue struct {
		Number      int64           `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		PullRequest json.RawMessage `json:"pull_request"` // non-nil on pr comments
	} `json:"issue"`
	Repository struct {
		ID    int64 `json:"id"`
		Owner struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (p *Processor) handleIssueComment(ctx context.Context, d Delivery, payload []byte) (string, error) {
	var ev issueCommentPayload
	if err := json.Unmarshal(payload, &ev); err != nil {
		return "", fmt.Errorf("parse issue_comment payload: %w", err)
	}
	if ev.Action != "created" {
		return "ignored", nil
	}
	// bot comments never run commands, else a bot-authored ack could loop
	if ev.Comment.User.Type == "Bot" {
		return "ignored", nil
	}
	cmd := ParseCommand(ev.Comment.Body)
	if !cmd.Addressed {
		return "ignored", nil
	}

	instID := ev.Installation.ID
	repo, err := p.repositoryForCommand(ctx, ev)
	if err != nil {
		return "", err
	}
	// limitation: usage/pr/disabled replies post before the permission check, so any commenter can draw one bounded reply; running a task stays write-gated
	reply := func(body string) error {
		return p.api.CreateIssueComment(ctx, instID, repo.Owner, repo.Name,
			ev.Issue.Number, body)
	}

	if !cmd.Known {
		if err := reply(commandUsage); err != nil {
			return "", fmt.Errorf("post usage reply: %w", err)
		}
		return "processed", nil
	}
	onPullRequest := len(ev.Issue.PullRequest) > 0
	if cmd.Verb == VerbRun && onPullRequest {
		return p.replyProcessed(reply, "`/agent-trail run` works on issues, not pull requests. "+
			"Use `/agent-trail revise` on an Agent Trail pull request.")
	}
	if cmd.Verb == VerbRevise && !onPullRequest {
		return p.replyProcessed(reply, "`/agent-trail revise` works on Agent Trail pull requests, "+
			"not issues. Use `/agent-trail run` on an issue.")
	}
	if !repo.IsEnabled {
		if err := reply("This repository is not enabled for Agent Trail."); err != nil {
			return "", fmt.Errorf("post disabled reply: %w", err)
		}
		return "processed", nil
	}

	permission, err := p.api.CollaboratorPermission(ctx, instID, repo.Owner,
		repo.Name, ev.Comment.User.Login)
	if err != nil {
		return "", fmt.Errorf("collaborator permission: %w", err)
	}
	if permission != "admin" && permission != "write" {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "command not authorized",
			slog.String("event", "github_command_unauthorized"),
			slog.String("trace_id", d.TraceID),
			slog.String("delivery_id", d.ID),
			slog.String("commenter", ev.Comment.User.Login),
			slog.String("permission", permission),
			slog.Int64("issue", ev.Issue.Number),
		)
		if err := reply("Only users with write access can run Agent Trail commands."); err != nil {
			return "", fmt.Errorf("post unauthorized reply: %w", err)
		}
		return "processed", nil
	}
	if cmd.Verb == VerbRevise {
		return p.handleRevise(ctx, d, ev, repo, reply)
	}

	if existing, active, err := p.tasks.ActiveTaskForIssue(ctx, repo.ID, ev.Issue.Number); err != nil {
		return "", err
	} else if active {
		if err := reply(activeTaskReply(existing.ID)); err != nil {
			return "", fmt.Errorf("post active-task reply: %w", err)
		}
		return "processed", nil
	}

	created, err := p.tasks.Create(ctx, task.CreateParams{
		Title:             truncateUTF8(ev.Issue.Title, 500),
		Instructions:      composeInstructions(ev),
		BaseBranch:        repo.DefaultBranch,
		SourceType:        "github_issue",
		SourceIssueNumber: &ev.Issue.Number,
		SourceCommentID:   &ev.Comment.ID,
		OrganizationID:    &repo.OrganizationID,
		RepositoryID:      &repo.ID,
		RequestedByLogin:  ev.Comment.User.Login,
	})
	if errors.Is(err, task.ErrActiveTaskExists) {
		// lost a race with a concurrent command on the same issue
		existing, active, lookupErr := p.tasks.ActiveTaskForIssue(ctx, repo.ID, ev.Issue.Number)
		if lookupErr != nil || !active {
			return "", fmt.Errorf("issue already has an active task; lookup: %w", lookupErr)
		}
		if err := reply(activeTaskReply(existing.ID)); err != nil {
			return "", fmt.Errorf("post active-task reply: %w", err)
		}
		return "processed", nil
	}
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	p.tasksCreated.Inc()
	p.logger.LogAttrs(ctx, slog.LevelInfo, "task created from issue comment",
		slog.String("event", "github_task_created"),
		slog.String("trace_id", d.TraceID),
		slog.String("delivery_id", d.ID),
		slog.String("task_id", created.ID),
		slog.String("repository", repo.FullName),
		slog.Int64("issue", ev.Issue.Number),
		slog.String("commenter", ev.Comment.User.Login),
	)

	// side effects after the durable task: failures logged, never unwind the task
	headSHA, err := p.api.BranchHeadSHA(ctx, instID, repo.Owner, repo.Name, repo.DefaultBranch)
	if err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "check run creation failed",
			slog.String("event", "github_check_run_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("task_id", created.ID),
			slog.String("error", err.Error()),
		)
	} else {
		p.createTriggerCheckRun(ctx, d, instID, repo, created.ID, headSHA)
	}
	ack := fmt.Sprintf(
		"Agent Trail queued task `%s` for this issue (requested by @%s). "+
			"The `%s` check tracks progress.",
		created.ID, ev.Comment.User.Login, CheckRunName)
	if err := reply(ack); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "ack comment failed",
			slog.String("event", "github_ack_comment_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("task_id", created.ID),
			slog.String("error", err.Error()),
		)
	} else {
		p.appendTaskEvent(ctx, created.ID, "github.comment.posted", map[string]string{
			"kind": "task_ack", "issue": strconv.FormatInt(ev.Issue.Number, 10),
		})
	}
	return "processed", nil
}

type pullRequestPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number int64 `json:"number"`
		Merged bool  `json:"merged"`
		Head   struct {
			Ref  string `json:"ref"`
			Repo *struct {
				ID int64 `json:"id"`
			} `json:"repo"` // null once a fork is deleted
		} `json:"head"`
	} `json:"pull_request"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// merged -> completed, closed unmerged -> cancelled; only review-phase tasks move
func (p *Processor) handlePullRequest(ctx context.Context, d Delivery, payload []byte) (string, error) {
	var ev pullRequestPayload
	if err := json.Unmarshal(payload, &ev); err != nil {
		return "", fmt.Errorf("parse pull_request payload: %w", err)
	}
	if ev.Action != "closed" {
		return "ignored", nil
	}
	number := ev.PullRequest.Number
	if number <= 0 || ev.Repository.ID == 0 {
		return "", errors.New("pull_request payload without number or repository id")
	}
	branch := ev.PullRequest.Head.Ref
	// a fork can name any branch, so only heads pushed to this repository map to tasks
	if !gitworkspace.ValidBranch(branch) || ev.PullRequest.Head.Repo == nil ||
		ev.PullRequest.Head.Repo.ID != ev.Repository.ID {
		return "ignored", nil
	}
	repo, err := p.store.RepositoryByGitHubID(ctx, ev.Repository.ID)
	if errors.Is(err, ErrRepositoryNotFound) {
		return "ignored", nil
	}
	if err != nil {
		return "", err
	}
	t, found, err := p.tasks.TaskForBranch(ctx, repo.ID, branch)
	if err != nil {
		return "", err
	}
	if !found || t.Status.Terminal() {
		return "ignored", nil
	}
	logInfo := func(msg, event string, status task.Status) {
		p.logger.LogAttrs(ctx, slog.LevelInfo, msg,
			slog.String("event", event),
			slog.String("trace_id", d.TraceID),
			slog.String("delivery_id", d.ID),
			slog.String("task_id", t.ID),
			slog.String("repository", repo.FullName),
			slog.Int64("pull_request", number),
			slog.Bool("merged", ev.PullRequest.Merged),
			slog.String("task_status", string(status)),
		)
	}
	if t.Phase != task.PhaseReview {
		logInfo("pull request closed before task reached review",
			"github_pull_request_closed_ignored", t.Status)
		return "ignored", nil
	}

	params := task.TransitionParams{
		To:             task.StatusCompleted,
		Source:         "system",
		Reason:         fmt.Sprintf("pull request #%d merged", number),
		IdempotencyKey: fmt.Sprintf("pull_request:%d:closed", number),
	}
	if !ev.PullRequest.Merged {
		params.To = task.StatusCancelled
		params.Reason = fmt.Sprintf("pull request #%d closed without merge", number)
	}
	next, err := p.tasks.Transition(ctx, t.ID, params)
	var invalid *task.InvalidTransitionError
	if errors.As(err, &invalid) {
		// task left review between the lookup and the row lock
		logInfo("pull request closed after task left review",
			"github_pull_request_closed_ignored", invalid.From)
		return "ignored", nil
	}
	if err != nil {
		return "", fmt.Errorf("finalize task %s: %w", t.ID, err)
	}
	logInfo("task finalized from pull request", "github_task_finalized", next.Status)
	return "processed", nil
}

// self-heals a missing row by one installation resync; installation account is the repo owner
func (p *Processor) repositoryForCommand(ctx context.Context, ev issueCommentPayload) (StoredRepository, error) {
	repo, err := p.store.RepositoryByGitHubID(ctx, ev.Repository.ID)
	if !errors.Is(err, ErrRepositoryNotFound) {
		return repo, err
	}
	err = p.store.UpsertInstallation(ctx, InstallationParams{
		GitHubInstallationID: ev.Installation.ID,
		AccountID:            ev.Repository.Owner.ID,
		AccountLogin:         ev.Repository.Owner.Login,
		AccountType:          ev.Repository.Owner.Type,
	})
	if err != nil {
		return StoredRepository{}, err
	}
	repos, err := p.api.ListInstallationRepositories(ctx, ev.Installation.ID)
	if err != nil {
		return StoredRepository{}, fmt.Errorf("list installation repositories: %w", err)
	}
	if err := p.store.SyncRepositories(ctx, ev.Installation.ID, repos); err != nil {
		return StoredRepository{}, err
	}
	return p.store.RepositoryByGitHubID(ctx, ev.Repository.ID)
}

// queued on the trigger head; the runner completes it with the attempt's conclusion
func (p *Processor) createTriggerCheckRun(ctx context.Context, d Delivery, instID int64, repo StoredRepository, taskID, headSHA string) {
	checkRunID, err := p.api.CreateCheckRun(ctx, instID, repo.Owner, repo.Name, CheckRunParams{
		Name:       CheckRunName,
		HeadSHA:    headSHA,
		ExternalID: taskID,
		Status:     "queued",
	})
	if err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "check run creation failed",
			slog.String("event", "github_check_run_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("task_id", taskID),
			slog.String("error", err.Error()),
		)
		return
	}
	if err := p.tasks.RecordTriggerCheckRun(ctx, taskID, checkRunID); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "trigger check run not recorded",
			slog.String("event", "github_check_run_record_failed"),
			slog.String("trace_id", d.TraceID),
			slog.String("task_id", taskID),
			slog.String("error", err.Error()),
		)
	}
	p.appendTaskEvent(ctx, taskID, "github.check_run.created", map[string]string{
		"check_run_id": strconv.FormatInt(checkRunID, 10),
		"head_sha":     headSHA,
	})
}

func (p *Processor) appendTaskEvent(ctx context.Context, taskID, eventType string, payload map[string]string) {
	if err := p.tasks.AppendEvent(ctx, taskID, eventType, "system", payload); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelWarn, "task event not appended",
			slog.String("event", "task_event_append_failed"),
			slog.String("trace_id", observability.TraceIDFrom(ctx)),
			slog.String("task_id", taskID),
			slog.String("event_type", eventType),
			slog.String("error", err.Error()),
		)
	}
}

func activeTaskReply(taskID string) string {
	return fmt.Sprintf("This issue already has an active task (`%s`). "+
		"Cancel it before starting another.", taskID)
}

func composeInstructions(ev issueCommentPayload) string {
	const limit = 100000
	full := fmt.Sprintf("%s\n\n%s\n\n---\nTriggering comment by @%s:\n\n%s",
		ev.Issue.Title, ev.Issue.Body, ev.Comment.User.Login, ev.Comment.Body)
	return truncateUTF8(full, limit)
}

// byte bound without splitting a rune; db check counts chars and bytes >= chars
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
