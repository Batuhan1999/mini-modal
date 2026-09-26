package controlplane

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"

	"github.com/prometheus/client_golang/prometheus"
)

func TestServiceRecordsSubmittedJobMetric(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	service := newTestServiceWithMetrics(t, nil, metrics)

	if _, err := service.Submit(context.Background(), jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "echo",
	}); err != nil {
		t.Fatal(err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "mini_modal_jobs_submitted_total" {
			continue
		}
		if len(family.Metric) != 1 || family.Metric[0].GetCounter().GetValue() != 1 {
			t.Fatalf("submitted metric = %+v, want 1", family.Metric)
		}
		return
	}
	t.Fatal("mini_modal_jobs_submitted_total was not gathered")
}

func TestServiceSchedulesAndReleasesResources(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{
		{ID: "node-a", GPUType: "A100", TotalGPUs: 2, TotalCPUs: 16, TotalMemoryGB: 64},
	})

	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", GPUType: "A100", GPUs: 2, CPUs: 4, MemoryGB: 8,
		Image: "alpine:3.20", Command: "echo",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	job, err = service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(job.Status) != jobmodel.Assigned {
		t.Fatalf("got status %q after scheduling, want %q", job.Status, jobmodel.Assigned)
	}

	nodes, err := service.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodes[0].AvailableGPUs; got != 0 {
		t.Fatalf("got %d available GPUs while assigned, want 0", got)
	}

	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected assigned work for node-a")
	}
	if assignment.JobID != job.ID || assignment.NodeID != "node-a" {
		t.Fatalf("unexpected assignment: %#v", assignment)
	}
	if assignment.Image != "alpine:3.20" || assignment.CPUs != 4 || assignment.MemoryGB != 8 {
		t.Fatalf("assignment runtime configuration = %#v", assignment)
	}
	queries := sqlitedb.New(service.db)
	submissionEvents, err := queries.ListJobEvents(ctx, sqlitedb.ListJobEventsParams{
		JobID:   job.ID,
		Attempt: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submissionEvents) != 1 || submissionEvents[0].Event != "submitted" {
		t.Fatalf("submission events = %#v", submissionEvents)
	}
	events, err := queries.ListJobEvents(ctx, sqlitedb.ListJobEventsParams{
		JobID:   job.ID,
		Attempt: assignment.Attempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 ||
		events[0].Event != "assigned" ||
		events[1].Event != "claimed" ||
		events[1].NodeID != "node-a" {
		t.Fatalf("claim events = %#v", events)
	}

	running, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(running.Status) != jobmodel.Running {
		t.Fatalf("got status %q after claim, want %q", running.Status, jobmodel.Running)
	}
	if _, found, err := service.Claim(ctx, "node-a"); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("claimed the same assignment twice")
	}

	if err := service.Complete(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	nodes, err = service.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodes[0].AvailableGPUs; got != 2 {
		t.Fatalf("got %d available GPUs after completion, want 2", got)
	}
	completed, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(completed.Status) != jobmodel.Succeeded {
		t.Fatalf("got status %q, want %q", completed.Status, jobmodel.Succeeded)
	}
	events, err = queries.ListJobEvents(ctx, sqlitedb.ListJobEventsParams{
		JobID:   job.ID,
		Attempt: assignment.Attempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].Event != "completed" {
		t.Fatalf("completed job events = %#v", events)
	}
}

func TestServiceRejectsInvalidJobs(t *testing.T) {
	tests := []struct {
		name string
		job  jobmodel.Spec
	}{
		{name: "missing ID", job: jobmodel.Spec{CPUs: 1, MemoryGB: 1}},
		{name: "blank ID", job: jobmodel.Spec{ID: "   ", CPUs: 1, MemoryGB: 1}},
		{name: "zero CPUs", job: jobmodel.Spec{ID: "job-1", MemoryGB: 1}},
		{name: "zero memory", job: jobmodel.Spec{ID: "job-1", CPUs: 1}},
		{name: "negative GPUs", job: jobmodel.Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, GPUs: -1}},
		{name: "negative duration", job: jobmodel.Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, DurationSeconds: -1}},
		{name: "missing image", job: jobmodel.Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, Command: "echo"}},
		{name: "blank image", job: jobmodel.Spec{ID: "job-1", CPUs: 1, MemoryGB: 1, Command: "echo", Image: "   "}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service := newTestService(t, nil)

			if _, err := service.Submit(ctx, test.job); err == nil {
				t.Fatal("expected invalid job to be rejected")
			}
			jobs, err := service.Jobs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 0 {
				t.Fatalf("stored %d invalid jobs, want 0", len(jobs))
			}
		})
	}
}

func TestServiceKeepsRunningJobsAfterRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "mini-modal.db")
	node := domain.Node{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}

	db, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(NewRuntimeState(), sqlitedb.New(db), db, newTestMetrics())
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "echo",
	}); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	firstAssignment, found, err := service.Claim(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected the first assignment")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	restarted := NewService(NewRuntimeState(), sqlitedb.New(db), db, newTestMetrics())
	if err := restarted.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	running, err := restarted.Job(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(running.Status) != jobmodel.Running {
		t.Fatalf("status after restart = %q, want %q", running.Status, jobmodel.Running)
	}
	if running.Attempt != firstAssignment.Attempt {
		t.Fatalf("attempt after restart = %d, want %d", running.Attempt, firstAssignment.Attempt)
	}

	if err := restarted.RegisterNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	shouldCancel, err := restarted.RenewAssignment(ctx, firstAssignment)
	if err != nil {
		t.Fatalf("renew assignment after restart: %v", err)
	}
	if shouldCancel {
		t.Fatal("renewed running assignment requested cancellation")
	}
	if err := restarted.Complete(ctx, firstAssignment); err != nil {
		t.Fatal(err)
	}
}

func TestServiceCancelsRunningAssignment(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}})
	if _, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "sleep", Args: []string{"30"},
	}); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil || !found {
		t.Fatalf("claim assignment: found=%v err=%v", found, err)
	}

	if err := service.CancelJob(ctx, assignment.JobID); err != nil {
		t.Fatal(err)
	}
	events := listTestJobEvents(t, service, assignment.JobID, assignment.Attempt)
	if len(events) != 3 || events[2].Event != "cancellation_requested" {
		t.Fatalf("events after cancellation request = %#v", events)
	}
	shouldCancel, err := service.RenewAssignment(ctx, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldCancel {
		t.Fatal("renewal did not return cancellation request")
	}
	if err := service.Cancel(ctx, domain.Assignment{
		JobID: assignment.JobID, Attempt: assignment.Attempt + 1,
	}); err == nil {
		t.Fatal("stale cancellation acknowledgement was accepted")
	}
	if err := service.Cancel(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	events = listTestJobEvents(t, service, assignment.JobID, assignment.Attempt)
	if len(events) != 4 || events[3].Event != "cancelled" {
		t.Fatalf("events after cancellation acknowledgement = %#v", events)
	}

	record, err := service.Job(ctx, assignment.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(record.Status) != jobmodel.Cancelled || record.NodeID != "" || record.LeaseExpiresAt != 0 {
		t.Fatalf("cancelled job = %+v", record)
	}
}

func TestServiceCancelsPendingJobImmediately(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, nil)
	if _, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "echo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelJob(ctx, "job-1"); err != nil {
		t.Fatal(err)
	}
	record, err := service.Job(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(record.Status) != jobmodel.Cancelled {
		t.Fatalf("status = %q, want %q", record.Status, jobmodel.Cancelled)
	}
	events := listTestJobEvents(t, service, "job-1", 0)
	if len(events) != 2 || events[0].Event != "submitted" || events[1].Event != "cancelled" {
		t.Fatalf("pending cancellation events = %#v", events)
	}
}

func TestServiceRecordsFailedJobEvent(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}})
	if _, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "false", MaxAttempts: 1,
	}); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil || !found {
		t.Fatalf("claim assignment: found=%v err=%v", found, err)
	}
	if err := service.Fail(ctx, assignment, domain.FailureExecutionError, "executor failed"); err != nil {
		t.Fatal(err)
	}

	events := listTestJobEvents(t, service, assignment.JobID, assignment.Attempt)
	if len(events) != 3 || events[2].Event != "failed" {
		t.Fatalf("failed job events = %#v", events)
	}
}

func TestServiceRetriesFailedJobAfterBackoff(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}})
	if _, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "false",
		MaxAttempts: 2, RetryBackoffSeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil || !found {
		t.Fatalf("claim assignment: found=%v err=%v", found, err)
	}

	if err := service.Fail(ctx, assignment, domain.FailureExecutionError, "executor failed"); err != nil {
		t.Fatal(err)
	}
	retrying, err := service.Job(ctx, assignment.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(retrying.Status) != jobmodel.Retrying {
		t.Fatalf("status after retryable failure = %q, want %q", retrying.Status, jobmodel.Retrying)
	}
	if retrying.LastError != "executor failed" || retrying.NextAttemptAt == 0 {
		t.Fatalf("retry metadata = %+v", retrying)
	}

	released, err := service.releaseDueRetriesLocked(ctx, time.UnixMilli(retrying.NextAttemptAt))
	if err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatalf("released retries = %d, want 1", released)
	}
	pending, err := service.Job(ctx, assignment.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(pending.Status) != jobmodel.Pending {
		t.Fatalf("status after backoff = %q, want %q", pending.Status, jobmodel.Pending)
	}

	events := listTestJobEvents(t, service, assignment.JobID, assignment.Attempt)
	want := []string{"assigned", "claimed", "attempt_failed", "retry_scheduled", "retry_ready"}
	if len(events) != len(want) {
		t.Fatalf("retry events = %#v", events)
	}
	for i, event := range events {
		if event.Event != want[i] {
			t.Fatalf("retry event %d = %q, want %q", i, event.Event, want[i])
		}
	}
}

func TestServiceRecordsExpiredLeaseEvent(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "sleep",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)

	expired, err := service.expireAssignmentsLocked(ctx, time.Now().Add(assignmentLeaseDuration+time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("expired assignments = %d, want 1", expired)
	}
	events := listTestJobEvents(t, service, job.ID, 1)
	if len(events) != 3 ||
		events[1].Event != "lease_expired" ||
		events[1].NodeID != "node-a" ||
		events[2].Event != "retry_scheduled" {
		t.Fatalf("expired assignment events = %#v", events)
	}
	retrying, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(retrying.Status) != jobmodel.Retrying {
		t.Fatalf("status after lease expiration = %q, want %q", retrying.Status, jobmodel.Retrying)
	}
}

func newTestService(t *testing.T, nodes []domain.Node) *Service {
	return newTestServiceWithMetrics(t, nodes, newTestMetrics())
}

func scheduleTestJobs(t *testing.T, service *Service, ctx context.Context) {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.scheduleLocked(ctx); err != nil {
		t.Fatal(err)
	}
}

func newTestServiceWithMetrics(t *testing.T, nodes []domain.Node, metrics *Metrics) *Service {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	service := NewService(NewRuntimeState(), sqlitedb.New(db), db, metrics)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if err := service.RegisterNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	return service
}

func newTestMetrics() *Metrics {
	return NewMetrics(prometheus.NewRegistry())
}

func listTestJobEvents(t *testing.T, service *Service, jobID string, attempt int64) []sqlitedb.JobEvent {
	t.Helper()
	events, err := sqlitedb.New(service.db).ListJobEvents(context.Background(), sqlitedb.ListJobEventsParams{
		JobID:   jobID,
		Attempt: attempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}
