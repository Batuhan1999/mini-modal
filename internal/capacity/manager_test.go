package capacity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"
)

func TestPlanPacksJobsAndChoosesPool(t *testing.T) {
	m := New(nil, func(string) bool { return false })
	pools := []postgresdb.NodePool{
		{ID: "cpu", CpusPerNode: 8, MemoryGbPerNode: 16, MaxNodes: 4},
		{ID: "gpu", GpuType: "A100", GpusPerNode: 1, CpusPerNode: 8, MemoryGbPerNode: 16, MaxNodes: 4},
	}
	jobs := []postgresdb.ListCapacityDemandRow{
		{ID: "one", Cpus: 4, MemoryGb: 8},
		{ID: "two", Cpus: 4, MemoryGb: 8},
		{ID: "three", Cpus: 4, MemoryGb: 8},
		{ID: "gpu", GpuType: "A100", Gpus: 1, Cpus: 2, MemoryGb: 4},
	}
	desired := m.plan(pools, nil, nil, jobs, time.Now())
	if desired["cpu"] != 2 || desired["gpu"] != 1 {
		t.Fatalf("desired = %v, want cpu=2 and gpu=1", desired)
	}
}

func TestPlanUsesExistingCapacityAndProtectsBusyNode(t *testing.T) {
	m := New(nil, func(string) bool { return true })
	pool := postgresdb.NodePool{ID: "cpu", CpusPerNode: 8, MemoryGbPerNode: 16, MaxNodes: 4}
	nodes := []postgresdb.Node{{ID: "node-a", PoolID: "cpu", TotalCpus: 8, TotalMemoryGb: 16}}
	active := []postgresdb.ListActiveJobsRow{{ID: "running", NodeID: "node-a", Status: "running", Cpus: 4, MemoryGb: 8}}
	waiting := []postgresdb.ListCapacityDemandRow{{ID: "waiting", Cpus: 4, MemoryGb: 8}}
	desired := m.plan([]postgresdb.NodePool{pool}, nodes, active, waiting, time.Now())
	if desired["cpu"] != 1 {
		t.Fatalf("desired = %v, want one existing node", desired)
	}
	desired = m.plan([]postgresdb.NodePool{pool}, nodes, active, nil, time.Now())
	if desired["cpu"] != 1 {
		t.Fatalf("desired = %v, running job must keep its node", desired)
	}
}

func TestPlanWaitsForIdleTimeoutBeforeScalingDown(t *testing.T) {
	m := New(nil, func(string) bool { return true })
	pool := postgresdb.NodePool{ID: "cpu", CpusPerNode: 8, MemoryGbPerNode: 16, MaxNodes: 4, IdleTimeoutSeconds: 5}
	nodes := []postgresdb.Node{{ID: "node-a", PoolID: "cpu", TotalCpus: 8, TotalMemoryGb: 16}}
	start := time.Unix(100, 0)
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, nil, start)["cpu"]; got != 1 {
		t.Fatalf("desired at idle start = %d, want 1", got)
	}
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, nil, start.Add(6*time.Second))["cpu"]; got != 0 {
		t.Fatalf("desired after idle timeout = %d, want 0", got)
	}
}

func TestPlanStartsIdleTimeoutAfterLastDemand(t *testing.T) {
	m := New(nil, func(string) bool { return true })
	pool := postgresdb.NodePool{ID: "cpu", CpusPerNode: 8, MemoryGbPerNode: 16, MaxNodes: 4, IdleTimeoutSeconds: 5}
	nodes := []postgresdb.Node{{ID: "node-a", PoolID: "cpu", TotalCpus: 8, TotalMemoryGb: 16}}
	start := time.Unix(100, 0)
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, nil, start)["cpu"]; got != 1 {
		t.Fatalf("desired at idle start = %d, want 1", got)
	}
	waiting := []postgresdb.ListCapacityDemandRow{{ID: "job-a", Cpus: 4, MemoryGb: 8}}
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, waiting, start.Add(6*time.Second))["cpu"]; got != 1 {
		t.Fatalf("desired with demand = %d, want 1", got)
	}
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, nil, start.Add(7*time.Second))["cpu"]; got != 1 {
		t.Fatalf("desired just after demand clears = %d, want 1", got)
	}
	if got := m.plan([]postgresdb.NodePool{pool}, nodes, nil, nil, start.Add(13*time.Second))["cpu"]; got != 0 {
		t.Fatalf("desired after fresh idle timeout = %d, want 0", got)
	}
}

func TestPlanReplacesLostCapacityAndRespectsLimits(t *testing.T) {
	m := New(nil, func(string) bool { return false })
	pool := postgresdb.NodePool{ID: "cpu", CpusPerNode: 4, MemoryGbPerNode: 8, MinNodes: 1, MaxNodes: 2}
	nodes := []postgresdb.Node{{ID: "lost", PoolID: "cpu", TotalCpus: 4, TotalMemoryGb: 8}}
	active := []postgresdb.ListActiveJobsRow{{ID: "running", NodeID: "lost", Status: "running", Cpus: 4, MemoryGb: 8}}
	desired := m.plan([]postgresdb.NodePool{pool}, nodes, active, nil, time.Now())
	if desired["cpu"] != 1 {
		t.Fatalf("desired for lost running node = %d, want replacement", desired["cpu"])
	}
	waiting := []postgresdb.ListCapacityDemandRow{
		{ID: "one", Cpus: 4, MemoryGb: 8},
		{ID: "two", Cpus: 4, MemoryGb: 8},
		{ID: "three", Cpus: 4, MemoryGb: 8},
	}
	if got := m.plan([]postgresdb.NodePool{pool}, nil, nil, waiting, time.Now())["cpu"]; got != 2 {
		t.Fatalf("desired beyond pool maximum = %d, want 2", got)
	}
	if got := m.plan([]postgresdb.NodePool{pool}, nil, nil, nil, time.Now())["cpu"]; got != 1 {
		t.Fatalf("desired below pool minimum = %d, want 1", got)
	}
}

func TestReconcileIncludesUnschedulableJobs(t *testing.T) {
	db := testdb.Open(t)
	testdb.SeedFunctionVersion(t, db)
	queries := postgresdb.New(db)
	ctx := context.Background()
	_, err := queries.CreateNodePool(ctx, postgresdb.CreateNodePoolParams{
		ID: "cpu", CpusPerNode: 8, MemoryGbPerNode: 16,
		MaxNodes: 4, AutoscaleEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateJob(ctx, postgresdb.CreateJobParams{
		ID: "job-a", FunctionVersionID: testdb.FunctionVersionID,
		InputJson: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE jobs SET status = 'unschedulable' WHERE id = 'job-a'"); err != nil {
		t.Fatal(err)
	}
	m := New(queries, func(string) bool { return false })
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := queries.GetNodePool(ctx, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if pool.DesiredNodes != 1 {
		t.Fatalf("desired nodes = %d, want 1", pool.DesiredNodes)
	}
}
