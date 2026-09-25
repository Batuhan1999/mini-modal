package controlplane

import (
	"context"
	"path/filepath"
	"testing"

	"mini-modal/internal/domain"
	"mini-modal/internal/store/sqlite"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

func TestServiceSchedulesAndReleasesResources(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t, []domain.Node{
		{ID: "node-a", GPUType: "A100", TotalGPUs: 2, TotalCPUs: 16, TotalMemoryGB: 64},
	})

	job, err := service.Submit(ctx, sqlitedb.CreateJobParams{
		ID: "job-1", GpuType: "A100", Gpus: 2, Cpus: 4, MemoryGb: 8,
		Command: "echo", Args: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobAssigned {
		t.Fatalf("got status %q after scheduling, want %q", job.Status, JobAssigned)
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

	running, err := service.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != JobRunning {
		t.Fatalf("got status %q after claim, want %q", running.Status, JobRunning)
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
	if completed.Status != JobSucceeded {
		t.Fatalf("got status %q, want %q", completed.Status, JobSucceeded)
	}
}

func TestServiceRejectsInvalidJobs(t *testing.T) {
	tests := []struct {
		name string
		job  sqlitedb.CreateJobParams
	}{
		{name: "missing ID", job: sqlitedb.CreateJobParams{Cpus: 1, MemoryGb: 1}},
		{name: "blank ID", job: sqlitedb.CreateJobParams{ID: "   ", Cpus: 1, MemoryGb: 1}},
		{name: "zero CPUs", job: sqlitedb.CreateJobParams{ID: "job-1", MemoryGb: 1}},
		{name: "zero memory", job: sqlitedb.CreateJobParams{ID: "job-1", Cpus: 1}},
		{name: "negative GPUs", job: sqlitedb.CreateJobParams{ID: "job-1", Cpus: 1, MemoryGb: 1, Gpus: -1}},
		{name: "negative duration", job: sqlitedb.CreateJobParams{ID: "job-1", Cpus: 1, MemoryGb: 1, DurationSeconds: -1}},
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
	if _, err := service.Submit(ctx, sqlitedb.CreateJobParams{
		ID: "job-1", Cpus: 1, MemoryGb: 1, Command: "echo", Args: "[]",
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
	if running.Status != JobRunning {
		t.Fatalf("status after restart = %q, want %q", running.Status, JobRunning)
	}
	if running.Attempt != firstAssignment.Attempt {
		t.Fatalf("attempt after restart = %d, want %d", running.Attempt, firstAssignment.Attempt)
	}

	if err := restarted.RegisterNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RenewAssignment(ctx, firstAssignment); err != nil {
		t.Fatalf("renew assignment after restart: %v", err)
	}
	if err := restarted.Complete(ctx, firstAssignment); err != nil {
		t.Fatal(err)
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
