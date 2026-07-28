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

	"github.com/chieaid24/agent-trail/apps/api/internal/observability"
	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

// checkRunName is the check run created for each GitHub-sourced task.
const checkRunName = "Agent Trail Task"

// processTimeout bounds the asynchronous handling of one delivery.
const processTimeout = 30 * time.Second

// Delivery identifies one accepted webhook delivery being processed.
type Delivery struct {
	ID        string
	EventType string
	Action    string
	TraceID   string
}

// TaskService is the slice of the task domain the integration consumes;
// implemented by *task.Store.
type TaskService interface {
	Create(ctx context.Context, p task.CreateParams) (task.Task, error)
	ActiveTaskForIssue(ctx context.Context, repositoryID string, issueNumber int64) (task.Task, bool, error)
	AppendEvent(ctx context.Context, taskID, eventType, source string, payload map[string]string) error
}

// API is the slice of the GitHub client the processor calls; implemented by
// *Client, faked in tests.
type API interface {
	ListInstallationRepositories(ctx context.Context, installationID int64) ([]Repository, error)
	CollaboratorPermission(ctx context.Context, installationID int64, owner, repo, username string) (string, error)
	BranchHeadSHA(ctx context.Context, installationID int64, owner, repo, branch string) (string, error)
	CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, issueNumber int64, body string) error
	CreateCheckRun(ctx context.Context, installationID int64, owner, repo string, p CheckRunParams) (int64, error)
}

// Processor handles accepted deliveries asynchronously: installation and
// repository sync, and the /agent-trail run command flow.
type Processor struct {
	store  *Store
	tasks  TaskService
	api    API
	logger *slog.Logger

	tasksCreated *observability.Counter // agent_trail_task_created_total

	wg sync.WaitGroup
}

// NewProcessor wires a Processor.
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

// Dispatch processes the delivery off the webhook request goroutine.
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
		p.process(observability.WithTraceID(ctx, d.TraceID), d, payload)
	}()
}

// Wait blocks until every dispatched delivery finishes; called on shutdown.
func (p *Processor) Wait() { p.wg.Wait() }

// process routes one delivery and records its outcome in the ledger.
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
	// Fresh deadline: if handle consumed the processing budget, the ledger
	// update must still land or the row is stuck pending forever.
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

// handle processes one delivery; the returned status is processed or
// ignored.
func (p *Processor) handle(ctx context.Context, d Delivery, payload []byte) (string, error) {
	switch d.EventType {
	case "installation":
		return p.handleInstallation(ctx, d, payload)
	case "installation_repositories":
		return p.handleInstallationRepositories(ctx, payload)
	case "issue_comment":
		return p.handleIssueComment(ctx, d, payload)
	default:
		// ping, issues, pull_request, ... - subscribed for later
		// milestones, recorded and skipped.
		return "ignored", nil
	}
}

// installationPayload is the slice of installation* events the sync uses.
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

// syncInstallation upserts the installation and refreshes its repository
// list from the API (webhook payloads lack default_branch and clone_url, so
// the API list is the source of truth).
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
	// added and removed both resync the full list.
	if err := p.syncInstallation(ctx, ev); err != nil {
		return "", err
	}
	return "processed", nil
}

// issueCommentPayload is the slice of issue_comment events the command flow
// uses.
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
		PullRequest json.RawMessage `json:"pull_request"` // non-nil on PR comments
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
	// A bot comment can never run commands; without this, the ack comment
	// of a bot-authored command could loop.
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
	// Known limitation: usage, PR, and disabled-repo replies post before the
	// permission check, so any commenter can draw one bounded reply. Running
	// a task stays gated on write access below.
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
	if len(ev.Issue.PullRequest) > 0 {
		if err := reply("`/agent-trail run` works on issues, not pull requests."); err != nil {
			return "", fmt.Errorf("post pull-request reply: %w", err)
		}
		return "processed", nil
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
		if err := reply("Only users with write access can run Agent Trail tasks."); err != nil {
			return "", fmt.Errorf("post unauthorized reply: %w", err)
		}
		return "processed", nil
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
	})
	if errors.Is(err, task.ErrActiveTaskExists) {
		// Lost a race with a concurrent command on the same issue.
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

	// Side effects after the durable task: their failure is logged and
	// recorded on the timeline, never unwinds the task.
	p.createCheckRun(ctx, d, instID, repo, created)
	ack := fmt.Sprintf(
		"Agent Trail queued task `%s` for this issue (requested by @%s). "+
			"The `%s` check tracks progress.",
		created.ID, ev.Comment.User.Login, checkRunName)
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

// repositoryForCommand resolves the comment's repository, self-healing a
// missing row by resyncing the installation once (covers apps installed
// before the webhook endpoint existed). The installation account is the
// repository owner: an installation only ever covers its own account's
// repositories.
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

func (p *Processor) createCheckRun(ctx context.Context, d Delivery, instID int64, repo StoredRepository, created task.Task) {
	headSHA, err := p.api.BranchHeadSHA(ctx, instID, repo.Owner, repo.Name,
		repo.DefaultBranch)
	if err == nil {
		var checkRunID int64
		checkRunID, err = p.api.CreateCheckRun(ctx, instID, repo.Owner,
			repo.Name, CheckRunParams{
				Name:       checkRunName,
				HeadSHA:    headSHA,
				ExternalID: created.ID,
				Status:     "queued",
			})
		if err == nil {
			p.appendTaskEvent(ctx, created.ID, "github.check_run.created",
				map[string]string{
					"check_run_id": strconv.FormatInt(checkRunID, 10),
					"head_sha":     headSHA,
				})
			return
		}
	}
	p.logger.LogAttrs(ctx, slog.LevelWarn, "check run creation failed",
		slog.String("event", "github_check_run_failed"),
		slog.String("trace_id", d.TraceID),
		slog.String("task_id", created.ID),
		slog.String("error", err.Error()),
	)
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

// composeInstructions builds the task instructions from the issue and the
// triggering comment, bounded to the tasks.instructions limit.
func composeInstructions(ev issueCommentPayload) string {
	const limit = 100000
	full := fmt.Sprintf("%s\n\n%s\n\n---\nTriggering comment by @%s:\n\n%s",
		ev.Issue.Title, ev.Issue.Body, ev.Comment.User.Login, ev.Comment.Body)
	return truncateUTF8(full, limit)
}

// truncateUTF8 bounds s to max bytes without splitting a rune (the DB check
// counts characters, and bytes >= characters).
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
