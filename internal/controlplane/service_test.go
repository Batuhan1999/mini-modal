package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	"mini-modal/internal/store/postgres"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"

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

func TestServiceSubmitIdempotency(t *testing.T) {
	ctx := context.Background()
	registry := prometheus.NewRegistry()
	service := newTestServiceWithMetrics(t, nil, NewMetrics(registry))
	spec := jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "echo",
	}

	first, err := service.Submit(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.wakeup:
	default:
		t.Fatal("new submission did not wake scheduler")
	}

	retry := spec
	retry.Args = []string{}
	existing, err := service.Submit(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if existing != first {
		t.Fatalf("retry returned %+v, want existing job %+v", existing, first)
	}

	changed := spec
	changed.CPUs = 2
	if _, err := service.Submit(ctx, changed); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("changed specification error = %v, want ErrSubmissionConflict", err)
	}

	select {
	case <-service.wakeup:
		t.Fatal("duplicate submission woke scheduler")
	default:
	}
	events := listTestJobEvents(t, service, spec.ID, 0)
	if len(events) != 1 || events[0].Event != string(jobmodel.HistorySubmitted) {
		t.Fatalf("submission events = %+v, want one submitted event", events)
	}
	count, err := service.JobCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "mini_modal_jobs_submitted_total" {
			if got := family.Metric[0].GetCounter().GetValue(); got != 1 {
				t.Fatalf("submitted metric = %v, want 1", got)
			}
			return
		}
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
	queries := postgresdb.New(service.db)
	submissionEvents, err := queries.ListJobEvents(ctx, postgresdb.ListJobEventsParams{
		JobID:   job.ID,
		Attempt: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submissionEvents) != 1 || submissionEvents[0].Event != "submitted" {
		t.Fatalf("submission events = %#v", submissionEvents)
	}
	events, err := queries.ListJobEvents(ctx, postgresdb.ListJobEventsParams{
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
	events, err = queries.ListJobEvents(ctx, postgresdb.ListJobEventsParams{
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

func TestDrainingNodeRemainsVisibleButDoesNotReceiveWork(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{
		{ID: "node-a", GPUType: "A100", TotalGPUs: 1, TotalCPUs: 8, TotalMemoryGB: 16},
		{ID: "node-b", TotalCPUs: 8, TotalMemoryGB: 16},
	})
	if err := service.SetNodeDrain(ctx, "node-a", true); err != nil {
		t.Fatal(err)
	}

	nodes, err := service.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ID != "node-a" || nodes[1].ID != "node-b" {
		t.Fatalf("visible nodes = %+v, want both nodes", nodes)
	}

	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "gpu-job", GPUType: "A100", GPUs: 1, CPUs: 1, MemoryGB: 1,
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
	if jobmodel.Status(job.Status) != jobmodel.Pending {
		t.Fatalf("job status = %q, want pending while its only capable node drains", job.Status)
	}
}

func TestDrainingNodeImmediatelyRequeuesUnclaimedAssignment(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{
		{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8},
		{ID: "node-b", TotalCPUs: 4, TotalMemoryGB: 8},
	})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "echo",
		MaxAttempts: 1, RetryBackoffSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assigned, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(assigned.Status) != jobmodel.Assigned || assigned.NodeID != "node-a" || assigned.Attempt != 1 || assigned.ExecutionAttempts != 0 {
		t.Fatalf("initial assignment = %+v, want attempt 1 on node-a", assigned)
	}

	if err := service.SetNodeDrain(ctx, "node-a", true); err != nil {
		t.Fatal(err)
	}
	requeued, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(requeued.Status) != jobmodel.Pending || requeued.NodeID != "" || requeued.LeaseExpiresAt != 0 || requeued.ExecutionAttempts != 0 {
		t.Fatalf("drained assignment = %+v, want pending without an execution attempt", requeued)
	}
	events := listTestJobEvents(t, service, job.ID, assigned.Attempt)
	if len(events) != 2 || events[1].Event != string(jobmodel.HistoryAssignmentRequeued) || events[1].NodeID != "node-a" {
		t.Fatalf("drain events = %+v, want assignment requeued on node-a", events)
	}
	if assignment, found, err := service.Claim(ctx, "node-a"); err != nil || found {
		t.Fatalf("claim on draining node = %+v, found=%v, err=%v; want no assignment", assignment, found, err)
	}
	scheduleTestJobs(t, service, ctx)
	reassigned, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(reassigned.Status) != jobmodel.Assigned || reassigned.NodeID != "node-b" || reassigned.Attempt != 2 || reassigned.ExecutionAttempts != 0 {
		t.Fatalf("reassignment = %+v, want attempt 2 on node-b", reassigned)
	}
	if assignment, found, err := service.Claim(ctx, "node-b"); err != nil || !found || assignment.JobID != job.ID {
		t.Fatalf("claim on node-b = %+v, found=%v, err=%v; want reassigned job", assignment, found, err)
	}
	running, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.ExecutionAttempts != 1 {
		t.Fatalf("execution attempts after claim = %d, want 1", running.ExecutionAttempts)
	}
}

func TestClaimRejectsAlreadyAssignedWorkOnDrainingNode(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "echo",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	// Set the flag directly to exercise the ClaimJob SQL guard independently of SetNodeDrain's requeue.
	if _, err := service.queries.UpdateNodeDrain(ctx, postgresdb.UpdateNodeDrainParams{
		ID: "node-a", ShouldDrain: true,
	}); err != nil {
		t.Fatal(err)
	}
	if assignment, found, err := service.Claim(ctx, "node-a"); err != nil || found {
		t.Fatalf("claim on draining node = %+v, found=%v, err=%v; want no assignment", assignment, found, err)
	}
	stillAssigned, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(stillAssigned.Status) != jobmodel.Assigned || stillAssigned.ExecutionAttempts != 0 {
		t.Fatalf("job after rejected claim = %+v, want assigned with no executions", stillAssigned)
	}
}

func TestDrainingNodeLetsRunningAssignmentComplete(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "echo",
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil || !found {
		t.Fatalf("claim before drain: found=%v err=%v", found, err)
	}
	if err := service.SetNodeDrain(ctx, "node-a", true); err != nil {
		t.Fatal(err)
	}
	running, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(running.Status) != jobmodel.Running || running.NodeID != "node-a" {
		t.Fatalf("job after drain = %+v, want running on node-a", running)
	}
	if err := service.Complete(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(completed.Status) != jobmodel.Succeeded {
		t.Fatalf("job after completion = %+v, want succeeded", completed)
	}
}

func TestFailureBudgetCountsExecutionsNotPlacements(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{
		{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8},
		{ID: "node-b", TotalCPUs: 4, TotalMemoryGB: 8},
	})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "false",
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	if err := service.SetNodeDrain(ctx, "node-a", true); err != nil {
		t.Fatal(err)
	}
	scheduleTestJobs(t, service, ctx)
	assignment, found, err := service.Claim(ctx, "node-b")
	if err != nil || !found {
		t.Fatalf("claim after re-placement: found=%v err=%v", found, err)
	}
	if assignment.Attempt != 2 {
		t.Fatalf("assignment generation = %d, want 2", assignment.Attempt)
	}
	if err := service.Fail(ctx, assignment, domain.FailureExecutionError, "executor failed"); err != nil {
		t.Fatal(err)
	}
	retrying, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(retrying.Status) != jobmodel.Retrying || retrying.ExecutionAttempts != 1 {
		t.Fatalf("job after first execution failure = %+v, want retrying with one execution", retrying)
	}
}

func TestRegisterNodeReturnsPersistedDrainState(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, nil)
	node := domain.Node{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}

	shouldDrain, err := service.RegisterNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	if shouldDrain {
		t.Fatal("new node unexpectedly requested to drain")
	}
	if err := service.SetNodeDrain(ctx, node.ID, true); err != nil {
		t.Fatal(err)
	}

	shouldDrain, err = service.RegisterNode(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldDrain {
		t.Fatal("re-registered node did not receive its persisted drain request")
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
	node := domain.Node{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}

	db, databaseURL := testdb.OpenWithDSN(t)
	service := NewService(NewRuntimeState(), postgresdb.New(db), db, newTestMetrics())
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterNode(ctx, node); err != nil {
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

	db, err = postgres.Open(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	restarted := NewService(NewRuntimeState(), postgresdb.New(db), db, newTestMetrics())
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

	if _, err := restarted.RegisterNode(ctx, node); err != nil {
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

func TestUnclaimedAssignmentLeaseExpiryRequeuesWithoutRetry(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{{
		ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8,
	}})
	job, err := service.Submit(ctx, jobmodel.Spec{
		ID: "job-1", CPUs: 1, MemoryGB: 1,
		Image: "alpine:3.20", Command: "sleep", MaxAttempts: 1,
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
		events[2].Event != "assignment_requeued" {
		t.Fatalf("expired assignment events = %#v", events)
	}
	requeued, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(requeued.Status) != jobmodel.Pending || requeued.ExecutionAttempts != 0 || requeued.NextAttemptAt != 0 {
		t.Fatalf("job after unclaimed lease expiry = %+v, want pending with no execution attempts", requeued)
	}
	scheduleTestJobs(t, service, ctx)
	reassigned, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobmodel.Status(reassigned.Status) != jobmodel.Assigned || reassigned.Attempt != 2 {
		t.Fatalf("reassignment after lease expiry = %+v, want assignment generation 2", reassigned)
	}
}

func TestRunningLeaseExpiryUsesExecutionRetryBudget(t *testing.T) {
	for _, test := range []struct {
		name        string
		maxAttempts int64
		wantStatus  jobmodel.Status
	}{
		{name: "retry available", maxAttempts: 2, wantStatus: jobmodel.Retrying},
		{name: "retry exhausted", maxAttempts: 1, wantStatus: jobmodel.Failed},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service := newTestService(t, []domain.Node{{ID: "node-a", TotalCPUs: 4, TotalMemoryGB: 8}})
			job, err := service.Submit(ctx, jobmodel.Spec{
				ID: "job-1", CPUs: 1, MemoryGB: 1, Image: "alpine:3.20", Command: "sleep",
				MaxAttempts: test.maxAttempts,
			})
			if err != nil {
				t.Fatal(err)
			}
			scheduleTestJobs(t, service, ctx)
			if _, found, err := service.Claim(ctx, "node-a"); err != nil || !found {
				t.Fatalf("claim before lease expiry: found=%v err=%v", found, err)
			}
			running, err := service.Job(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if running.ExecutionAttempts != 1 {
				t.Fatalf("execution attempts = %d, want 1", running.ExecutionAttempts)
			}
			if expired, err := service.expireAssignmentsLocked(ctx, time.UnixMilli(running.LeaseExpiresAt).Add(time.Millisecond)); err != nil || expired != 1 {
				t.Fatalf("expired assignments = %d, err=%v; want 1", expired, err)
			}
			after, err := service.Job(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if jobmodel.Status(after.Status) != test.wantStatus || after.ExecutionAttempts != 1 {
				t.Fatalf("job after running lease expiry = %+v, want %q with one execution", after, test.wantStatus)
			}
		})
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
	db := testdb.Open(t)

	service := NewService(NewRuntimeState(), postgresdb.New(db), db, metrics)
	if err := service.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if _, err := service.RegisterNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	return service
}

func newTestMetrics() *Metrics {
	return NewMetrics(prometheus.NewRegistry())
}

func listTestJobEvents(t *testing.T, service *Service, jobID string, attempt int64) []postgresdb.JobEvent {
	t.Helper()
	events, err := postgresdb.New(service.db).ListJobEvents(context.Background(), postgresdb.ListJobEventsParams{
		JobID:   jobID,
		Attempt: attempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}
