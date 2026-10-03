package postgres_test

import (
	"context"
	"testing"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
	"mini-modal/internal/store/postgres/testdb"
)

func TestNodePoolMembershipAndDesiredCount(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	queries := postgresdb.New(db)

	pool, err := queries.CreateNodePool(ctx, postgresdb.CreateNodePoolParams{
		ID: "cpu-workers", CpusPerNode: 8, MemoryGbPerNode: 16,
		MinNodes: 0, MaxNodes: 4, DesiredNodes: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pool.DesiredNodes != 2 {
		t.Fatalf("desired nodes = %d, want 2", pool.DesiredNodes)
	}

	if _, err := queries.UpsertNode(ctx, postgresdb.UpsertNodeParams{
		ID: "node-a", PoolID: "missing-pool", TotalCpus: 8, TotalMemoryGb: 16,
	}); err == nil {
		t.Fatal("registered a node in a nonexistent pool")
	}
	node, err := queries.UpsertNode(ctx, postgresdb.UpsertNodeParams{
		ID: "node-a", PoolID: pool.ID, TotalCpus: 8, TotalMemoryGb: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.PoolID != pool.ID {
		t.Fatalf("node pool = %q, want %q", node.PoolID, pool.ID)
	}

	pool, err = queries.UpdateDesiredNodes(ctx, postgresdb.UpdateDesiredNodesParams{
		ID: pool.ID, DesiredNodes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pool.DesiredNodes != 3 {
		t.Fatalf("desired nodes = %d, want 3", pool.DesiredNodes)
	}
	if _, err := queries.UpdateDesiredNodes(ctx, postgresdb.UpdateDesiredNodesParams{
		ID: pool.ID, DesiredNodes: 5,
	}); err == nil {
		t.Fatal("accepted desired node count above the pool maximum")
	}
}
