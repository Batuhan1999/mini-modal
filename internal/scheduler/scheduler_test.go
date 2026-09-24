package scheduler

import (
	"testing"

	"mini-modal/internal/domain"
	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

func TestSchedulePrioritizesJobsAndDoesNotOverAllocate(t *testing.T) {
	nodes := []domain.Node{
		{
			ID:                "node-a",
			GPUType:           "A100",
			TotalGPUs:         4,
			AvailableGPUs:     4,
			TotalCPUs:         32,
			AvailableCPUs:     32,
			TotalMemoryGB:     128,
			AvailableMemoryGB: 128,
		},
	}

	jobs := []sqlitedb.Job{
		{ID: "low", Priority: 1, GpuType: "A100", Gpus: 3, Cpus: 4, MemoryGb: 8},
		{ID: "high", Priority: 10, GpuType: "A100", Gpus: 2, Cpus: 4, MemoryGb: 8},
	}

	assignments, pending, _ := NewFirstFitPolicy().Schedule(jobs, nodes)

	if len(assignments) != 1 {
		t.Fatalf("got %d assignments, want 1", len(assignments))
	}
	if assignments[0].JobID != "high" {
		t.Fatalf("assigned %q, want high-priority job", assignments[0].JobID)
	}
	if len(pending) != 1 || pending[0].ID != "low" {
		t.Fatalf("unexpected pending jobs: %#v", pending)
	}
}
