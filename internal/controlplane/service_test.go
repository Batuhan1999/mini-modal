package controlplane

import (
	"context"
	"path/filepath"
	"testing"

	"mini-modal/internal/domain"
	jobmodel "mini-modal/internal/job"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

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
	service := NewService(NewRuntimeState(), sqlitedb.New(db))
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
	restarted := NewService(NewRuntimeState(), sqlitedb.New(db))
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
	assignment, found, err := service.Claim(ctx, "node-a")
	if err != nil || !found {
		t.Fatalf("claim assignment: found=%v err=%v", found, err)
	}

	if err := service.CancelJob(ctx, assignment.JobID); err != nil {
		t.Fatal(err)
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
}

func newTestService(t *testing.T, nodes []domain.Node) *Service {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	service := NewService(NewRuntimeState(), sqlitedb.New(db))
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
