package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	batchapi "k8s.io/api/batch/v1"
	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testJobTemplate(t *testing.T) []byte {
	t.Helper()
	template, err := os.ReadFile("../../../../deploy/k8s/runner/job.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return template
}

func testKubernetesBackend(t *testing.T) (*KubernetesBackend, *fake.Clientset, string) {
	t.Helper()
	_, store, tasks := testStores(t)
	tk := mustCreateTask(t, tasks)
	client := fake.NewSimpleClientset()
	backend := &KubernetesBackend{
		Jobs:           client.BatchV1().Jobs("agent-trail-runners"),
		Store:          store,
		Tasks:          tasks,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Template:       testJobTemplate(t),
		Namespace:      "agent-trail-runners",
		RunnerImage:    "agent-trail/runner:test",
		JobTTL:         5 * time.Minute,
		DefaultRuntime: 45 * time.Minute,
		Poll:           20 * time.Millisecond,
		WorkerIdleExit: 2 * time.Minute,
		AgentProvider:  "fake",
		PermissionMode: "acceptEdits",
		OTLPEndpoint:   "off",
		GitHubAPIBase:  "http://fixture:8080",
	}
	return backend, client, tk.ID
}

func TestKubernetesBackendCreatesOneHardenedJobPerAttempt(t *testing.T) {
	backend, client, taskID := testKubernetesBackend(t)
	ctx := context.Background()
	if err := backend.dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err := backend.dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	jobs, err := client.BatchV1().Jobs("agent-trail-runners").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("Jobs = %d, want 1", len(jobs.Items))
	}
	job := jobs.Items[0]
	if job.Labels[managedByLabel] != managedByValue || job.Labels[attemptIDLabel] == "" {
		t.Errorf("Job labels = %v", job.Labels)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 300 {
		t.Errorf("ttlSecondsAfterFinished = %v, want 300", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 2760 {
		t.Errorf("activeDeadlineSeconds = %v, want 2760", job.Spec.ActiveDeadlineSeconds)
	}
	pod := job.Spec.Template.Spec
	container := pod.Containers[0]
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken ||
		container.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("runner Job lost its security boundary")
	}
	env := map[string]string{}
	for _, item := range container.Env {
		env[item.Name] = item.Value
	}
	if env["TASK_ATTEMPT_ID"] != job.Labels[attemptIDLabel] || env["RUNNER_TYPE"] != "kubernetes" {
		t.Errorf("target env = %v", env)
	}
	assertSubsequence(t, timelineTypes(t, backend.Tasks, taskID), []string{"runner.job_created"})
}

func TestKubernetesBackendWatchesRunningJob(t *testing.T) {
	backend, client, taskID := testKubernetesBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- backend.Run(ctx) }()

	var jobName string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := client.BatchV1().Jobs("agent-trail-runners").List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs.Items) == 1 {
			job := jobs.Items[0]
			job.Status.Active = 1
			if _, err := client.BatchV1().Jobs("agent-trail-runners").UpdateStatus(ctx, &job, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			jobName = job.Name
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if jobName == "" {
		t.Fatal("controller did not create a Job")
	}

	for time.Now().Before(deadline) {
		for _, eventType := range timelineTypes(t, backend.Tasks, taskID) {
			if eventType == "runner.job_running" {
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("controller did not observe the running Job")
}

func TestRenderJobTemplateRejectsUnsetVariables(t *testing.T) {
	_, err := renderJobTemplate([]byte("value: ${MISSING}"), nil)
	if err == nil {
		t.Fatal("unset template variable accepted")
	}
}

func TestJobStateReportsTerminalConditions(t *testing.T) {
	for _, tc := range []struct {
		condition batchapi.JobConditionType
		want      string
	}{
		{batchapi.JobComplete, "completed"},
		{batchapi.JobFailed, "failed"},
	} {
		job := &batchapi.Job{Status: batchapi.JobStatus{Conditions: []batchapi.JobCondition{{
			Type: tc.condition, Status: coreapi.ConditionTrue, Reason: "observed",
		}}}}
		got, reason := jobState(job)
		if got != tc.want || reason != "observed" {
			t.Errorf("jobState(%s) = %q/%q", tc.condition, got, reason)
		}
	}
}
