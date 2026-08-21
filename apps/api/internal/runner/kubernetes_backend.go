package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	batchapi "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	batchclient "k8s.io/client-go/kubernetes/typed/batch/v1"
	"sigs.k8s.io/yaml"

	"github.com/chieaid24/agent-trail/apps/api/internal/task"
)

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managedByValue = "agent-trail-controller"
	attemptIDLabel = "agent-trail.dev/task-attempt-id"
)

// KubernetesBackend schedules one Kubernetes Job per dispatchable attempt.
type KubernetesBackend struct {
	Jobs            batchclient.JobInterface
	Store           *Store
	Tasks           *task.Store
	Logger          *slog.Logger
	Template        []byte
	Namespace       string
	RunnerImage     string
	JobTTL          time.Duration
	DefaultRuntime  time.Duration
	Poll            time.Duration
	WorkerIdleExit  time.Duration
	AgentProvider   string
	AgentModel      string
	PermissionMode  string
	AgentCLIVersion string
	OTLPEndpoint    string
	GitHubAPIBase   string

	observed map[string]string
}

// Run dispatches work and observes Job status until ctx ends.
func (b *KubernetesBackend) Run(ctx context.Context) error {
	if err := b.validate(); err != nil {
		return err
	}
	if b.observed == nil {
		b.observed = map[string]string{}
	}
	for ctx.Err() == nil {
		if err := b.dispatch(ctx); err != nil {
			b.Logger.LogAttrs(ctx, slog.LevelWarn, "job dispatch failed",
				slog.String("event", "runner_job_dispatch_failed"),
				slog.String("error", err.Error()))
			sleep(ctx, b.Poll)
			continue
		}
		if err := b.watchOnce(ctx); err != nil && ctx.Err() == nil {
			b.Logger.LogAttrs(ctx, slog.LevelWarn, "job watch failed",
				slog.String("event", "runner_job_watch_failed"),
				slog.String("error", err.Error()))
			sleep(ctx, b.Poll)
		}
	}
	return nil
}

func (b *KubernetesBackend) validate() error {
	switch {
	case b.Jobs == nil:
		return errors.New("kubernetes backend requires a Job client")
	case b.Store == nil || b.Tasks == nil:
		return errors.New("kubernetes backend requires stores")
	case b.Logger == nil:
		return errors.New("kubernetes backend requires a logger")
	case len(b.Template) == 0:
		return errors.New("kubernetes backend requires a Job template")
	case b.Namespace == "":
		return errors.New("kubernetes backend requires a namespace")
	case b.RunnerImage == "":
		return errors.New("kubernetes backend requires a runner image")
	case b.JobTTL <= 0 || b.DefaultRuntime <= 0 || b.Poll <= 0:
		return errors.New("kubernetes backend durations must be positive")
	}
	return nil
}

func (b *KubernetesBackend) dispatch(ctx context.Context) error {
	jobs, err := b.Jobs.List(ctx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return fmt.Errorf("list runner Jobs: %w", err)
	}
	existing := make(map[string]struct{}, len(jobs.Items))
	for i := range jobs.Items {
		b.observe(ctx, &jobs.Items[i])
		attemptID := jobs.Items[i].Labels[attemptIDLabel]
		if attemptID != "" {
			existing[attemptID] = struct{}{}
		}
	}

	attempts, err := b.Store.DispatchableAttempts(ctx, b.DefaultRuntime, 100)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if _, ok := existing[attempt.AttemptID]; ok {
			continue
		}
		job, err := b.jobFor(attempt)
		if err != nil {
			return err
		}
		created, err := b.Jobs.Create(ctx, job, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create runner Job for attempt %s: %w", attempt.AttemptID, err)
		}
		b.Logger.LogAttrs(ctx, slog.LevelInfo, "runner Job created",
			slog.String("event", "runner_job_created"),
			slog.String("task_id", attempt.TaskID),
			slog.String("task_attempt_id", attempt.AttemptID),
			slog.String("job", created.Name))
		if err := b.Tasks.AppendAttemptEvent(ctx, attempt.AttemptID,
			"runner.job_created", "system", map[string]string{"job": created.Name}); err != nil {
			b.Logger.LogAttrs(ctx, slog.LevelWarn, "runner Job event failed",
				slog.String("event", "runner_job_event_failed"),
				slog.String("task_attempt_id", attempt.AttemptID),
				slog.String("error", err.Error()))
		}
		existing[attempt.AttemptID] = struct{}{}
	}
	return nil
}

func (b *KubernetesBackend) watchOnce(ctx context.Context) error {
	watchCtx, cancel := context.WithTimeout(ctx, b.Poll)
	defer cancel()
	watcher, err := b.Jobs.Watch(watchCtx, metav1.ListOptions{
		LabelSelector: managedByLabel + "=" + managedByValue,
	})
	if err != nil {
		return fmt.Errorf("watch runner Jobs: %w", err)
	}
	defer watcher.Stop()
	for {
		select {
		case <-watchCtx.Done():
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return nil
			}
			if event.Type == watch.Error {
				return errors.New("runner Job watch returned an error event")
			}
			job, ok := event.Object.(*batchapi.Job)
			if ok {
				b.observe(ctx, job)
			}
		}
	}
}

func (b *KubernetesBackend) observe(ctx context.Context, job *batchapi.Job) {
	if b.observed == nil {
		b.observed = map[string]string{}
	}
	state, reason := jobState(job)
	if state == "pending" || b.observed[job.Name] == state {
		return
	}
	b.observed[job.Name] = state
	attemptID := job.Labels[attemptIDLabel]
	b.Logger.LogAttrs(ctx, slog.LevelInfo, "runner Job status changed",
		slog.String("event", "runner_job_"+state),
		slog.String("task_attempt_id", attemptID),
		slog.String("job", job.Name),
		slog.String("status", state),
		slog.String("reason", reason))
	if attemptID == "" {
		return
	}
	payload := map[string]string{"job": job.Name, "status": state}
	if reason != "" {
		payload["reason"] = reason
	}
	if err := b.Tasks.AppendAttemptEvent(ctx, attemptID,
		"runner.job_"+state, "system", payload); err != nil {
		b.Logger.LogAttrs(ctx, slog.LevelWarn, "runner Job event failed",
			slog.String("event", "runner_job_event_failed"),
			slog.String("task_attempt_id", attemptID),
			slog.String("error", err.Error()))
	}
}

func jobState(job *batchapi.Job) (string, string) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case batchapi.JobComplete:
			return "completed", condition.Reason
		case batchapi.JobFailed:
			return "failed", condition.Reason
		}
	}
	if job.Status.Active > 0 {
		return "running", ""
	}
	return "pending", ""
}

func (b *KubernetesBackend) jobFor(attempt DispatchAttempt) (*batchapi.Job, error) {
	runtime := attempt.MaxRuntime
	if runtime <= 0 {
		runtime = b.DefaultRuntime
	}
	values := map[string]templateValue{
		"JOB_NAME":                    {jobName(attempt.AttemptID), false},
		"TASK_ATTEMPT_ID":             {attempt.AttemptID, false},
		"RUNNER_IMAGE":                {b.RunnerImage, false},
		"TTL_SECONDS":                 {strconv.FormatInt(int64(b.JobTTL/time.Second), 10), true},
		"ACTIVE_DEADLINE_SECONDS":     {strconv.FormatInt(int64((runtime+time.Minute)/time.Second), 10), true},
		"WORKER_IDLE_EXIT_SECONDS":    {strconv.FormatInt(int64(b.WorkerIdleExit/time.Second), 10), false},
		"AGENT_PROVIDER":              {b.AgentProvider, false},
		"AGENT_MODEL":                 {b.AgentModel, false},
		"AGENT_PERMISSION_MODE":       {b.PermissionMode, false},
		"AGENT_CLI_VERSION":           {b.AgentCLIVersion, false},
		"OTEL_EXPORTER_OTLP_ENDPOINT": {b.OTLPEndpoint, false},
		"GITHUB_API_BASE_URL":         {b.GitHubAPIBase, false},
	}
	rendered, err := renderJobTemplate(b.Template, values)
	if err != nil {
		return nil, err
	}
	rawJSON, err := yaml.YAMLToJSON(rendered)
	if err != nil {
		return nil, fmt.Errorf("parse runner Job template: %w", err)
	}
	var job batchapi.Job
	if err := json.Unmarshal(rawJSON, &job); err != nil {
		return nil, fmt.Errorf("decode runner Job template: %w", err)
	}
	job.Namespace = b.Namespace
	return &job, nil
}

type templateValue struct {
	value   string
	numeric bool
}

func renderJobTemplate(template []byte, values map[string]templateValue) ([]byte, error) {
	rendered := string(template)
	for key, value := range values {
		replacement := value.value
		if !value.numeric {
			replacement = strconv.Quote(value.value)
		}
		rendered = strings.ReplaceAll(rendered, "${"+key+"}", replacement)
	}
	if start := strings.Index(rendered, "${"); start >= 0 {
		end := strings.Index(rendered[start:], "}")
		if end >= 0 {
			return nil, fmt.Errorf("runner Job template variable %s is unset", rendered[start:start+end+1])
		}
		return nil, errors.New("runner Job template has an unterminated variable")
	}
	return []byte(rendered), nil
}

func jobName(attemptID string) string {
	return "agent-trail-" + strings.ToLower(attemptID)
}
