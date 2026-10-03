package provisioner

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"
)

type testDrainer struct {
	mu    sync.Mutex
	nodes []string
}

func (d *testDrainer) SetNodeDrain(_ context.Context, nodeID string, shouldDrain bool) error {
	if shouldDrain {
		d.mu.Lock()
		d.nodes = append(d.nodes, nodeID)
		d.mu.Unlock()
	}
	return sql.ErrNoRows
}

func (d *testDrainer) UnregisterNode(context.Context, string) error { return nil }

func TestReconcileStartsAndDrainsOnlyDesiredProcesses(t *testing.T) {
	db := testdb.Open(t)
	queries := postgresdb.New(db)
	ctx := context.Background()
	_, err := queries.CreateNodePool(ctx, postgresdb.CreateNodePoolParams{
		ID: "cpu", CpusPerNode: 4, MemoryGbPerNode: 8,
		MaxNodes: 4, DesiredNodes: 2, AutoscaleEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "agent-stub")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	drainer := &testDrainer{}
	m := New(ctx, binary, "localhost:9090", queries, drainer)
	m.stdout = io.Discard
	m.stderr = io.Discard
	t.Cleanup(m.Stop)

	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	started := len(m.processes)
	m.mu.Unlock()
	if started != 2 {
		t.Fatalf("started processes = %d, want 2", started)
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	stillStarted := len(m.processes)
	m.mu.Unlock()
	if stillStarted != 2 {
		t.Fatalf("repeated reconciliation started %d processes, want 2", stillStarted)
	}
	if _, err := queries.UpdateDesiredNodes(ctx, postgresdb.UpdateDesiredNodesParams{
		ID: "cpu", DesiredNodes: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	drainer.mu.Lock()
	drained := append([]string(nil), drainer.nodes...)
	drainer.mu.Unlock()
	if len(drained) != 2 {
		t.Fatalf("drained nodes = %v, want both processes", drained)
	}
}
