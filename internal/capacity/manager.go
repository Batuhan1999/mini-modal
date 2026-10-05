package capacity

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"mini-modal/internal/domain"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

type Manager struct {
	mu        sync.Mutex
	queries   *postgresdb.Queries
	healthy   func(string) bool
	idleSince map[string]time.Time
}

func New(queries *postgresdb.Queries, healthy func(string) bool) *Manager {
	return &Manager{queries: queries, healthy: healthy, idleSince: make(map[string]time.Time)}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.Printf("capacity reconciliation failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pools, err := m.queries.ListAutoscalingNodePools(ctx)
	if err != nil {
		return fmt.Errorf("list autoscaling node pools: %w", err)
	}
	if len(pools) == 0 {
		return nil
	}
	nodes, err := m.queries.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("list nodes for capacity: %w", err)
	}
	active, err := m.queries.ListActiveJobs(ctx)
	if err != nil {
		return fmt.Errorf("list active jobs for capacity: %w", err)
	}
	demand, err := m.queries.ListCapacityDemand(ctx)
	if err != nil {
		return fmt.Errorf("list jobs needing capacity: %w", err)
	}

	desired := m.plan(pools, nodes, active, demand, time.Now())
	for _, pool := range pools {
		if _, err := m.queries.SetDesiredNodesIfChanged(ctx, postgresdb.SetDesiredNodesIfChangedParams{
			ID: pool.ID, DesiredNodes: desired[pool.ID],
		}); err != nil {
			return fmt.Errorf("set desired nodes for pool %q: %w", pool.ID, err)
		}
	}
	return nil
}

type bin struct {
	node    domain.Node
	poolID  string
	busy    bool
	used    bool
	virtual bool
}

func (m *Manager) plan(
	pools []postgresdb.NodePool,
	nodes []postgresdb.Node,
	active []postgresdb.ListActiveJobsRow,
	demand []postgresdb.ListCapacityDemandRow,
	now time.Time,
) map[string]int64 {
	desired := make(map[string]int64, len(pools))
	poolByID := make(map[string]postgresdb.NodePool, len(pools))
	observed := make(map[string]int64, len(pools))
	for _, pool := range pools {
		poolByID[pool.ID] = pool
	}

	bins := make([]*bin, 0, len(nodes))
	byNodeID := make(map[string]*bin, len(nodes))
	seen := make(map[string]bool, len(nodes))
	for _, stored := range nodes {
		seen[stored.ID] = true
		if stored.ShouldDrain || !m.healthy(stored.ID) {
			delete(m.idleSince, stored.ID)
			continue
		}
		candidate := &bin{poolID: stored.PoolID, node: domain.Node{
			ID: stored.ID, PoolID: stored.PoolID, GPUType: stored.GpuType,
			TotalGPUs: stored.TotalGpus, AvailableGPUs: stored.TotalGpus,
			TotalCPUs: stored.TotalCpus, AvailableCPUs: stored.TotalCpus,
			TotalMemoryGB: stored.TotalMemoryGb, AvailableMemoryGB: stored.TotalMemoryGb,
		}}
		bins = append(bins, candidate)
		byNodeID[stored.ID] = candidate
		observed[stored.PoolID]++
	}
	for nodeID := range m.idleSince {
		if !seen[nodeID] {
			delete(m.idleSince, nodeID)
		}
	}

	lost := make([]postgresdb.ListCapacityDemandRow, 0)
	for _, job := range active {
		candidate := byNodeID[job.NodeID]
		if candidate == nil {
			if job.Status != "cancelling" && !m.healthy(job.NodeID) {
				lost = append(lost, postgresdb.ListCapacityDemandRow{
					ID: job.ID, GpuType: job.GpuType, Gpus: job.Gpus,
					Cpus: job.Cpus, MemoryGb: job.MemoryGb,
				})
			}
			continue
		}
		candidate.busy = true
		candidate.node.Reserve(domain.Resources{
			GPUType: job.GpuType, GPUs: job.Gpus, CPUs: job.Cpus, MemoryGB: job.MemoryGb,
		})
	}
	for _, candidate := range bins {
		if _, autoscaled := poolByID[candidate.poolID]; !autoscaled {
			continue
		}
		if candidate.busy {
			delete(m.idleSince, candidate.node.ID)
		} else if m.idleSince[candidate.node.ID].IsZero() {
			m.idleSince[candidate.node.ID] = now
		}
	}

	virtualCount := make(map[string]int64, len(pools))
	for _, job := range append(lost, demand...) {
		resources := domain.Resources{
			GPUType: job.GpuType, GPUs: job.Gpus, CPUs: job.Cpus, MemoryGB: job.MemoryGb,
		}
		placed := false
		for _, candidate := range bins {
			if candidate.node.CanRun(resources) {
				candidate.node.Reserve(resources)
				candidate.used = true
				if !candidate.virtual {
					delete(m.idleSince, candidate.node.ID)
				}
				placed = true
				break
			}
		}
		if placed {
			continue
		}

		chosen := -1
		for i, pool := range pools {
			if observed[pool.ID]+virtualCount[pool.ID] >= pool.MaxNodes || !poolCanRun(pool, resources) {
				continue
			}
			if chosen < 0 || preferPool(pool, pools[chosen], resources) {
				chosen = i
			}
		}
		if chosen < 0 {
			continue
		}
		pool := pools[chosen]
		virtualCount[pool.ID]++
		candidate := &bin{poolID: pool.ID, virtual: true, used: true, node: domain.Node{
			GPUType:   pool.GpuType,
			TotalGPUs: pool.GpusPerNode, AvailableGPUs: pool.GpusPerNode,
			TotalCPUs: pool.CpusPerNode, AvailableCPUs: pool.CpusPerNode,
			TotalMemoryGB: pool.MemoryGbPerNode, AvailableMemoryGB: pool.MemoryGbPerNode,
		}}
		candidate.node.Reserve(resources)
		bins = append(bins, candidate)
	}

	for _, candidate := range bins {
		pool, autoscaled := poolByID[candidate.poolID]
		if !autoscaled {
			continue
		}
		idleFor := now.Sub(m.idleSince[candidate.node.ID])
		if candidate.virtual || candidate.busy || candidate.used || idleFor < time.Duration(pool.IdleTimeoutSeconds)*time.Second {
			desired[pool.ID]++
		}
	}
	for _, pool := range pools {
		if desired[pool.ID] < pool.MinNodes {
			desired[pool.ID] = pool.MinNodes
		}
		if desired[pool.ID] > pool.MaxNodes {
			desired[pool.ID] = pool.MaxNodes
		}
	}
	return desired
}

func poolCanRun(pool postgresdb.NodePool, job domain.Resources) bool {
	return (job.GPUType == "" || job.GPUType == pool.GpuType) &&
		job.GPUs <= pool.GpusPerNode &&
		job.CPUs <= pool.CpusPerNode &&
		job.MemoryGB <= pool.MemoryGbPerNode
}

func preferPool(a, b postgresdb.NodePool, job domain.Resources) bool {
	if job.GPUs == 0 && (a.GpusPerNode == 0) != (b.GpusPerNode == 0) {
		return a.GpusPerNode == 0
	}
	if a.GpusPerNode-job.GPUs != b.GpusPerNode-job.GPUs {
		return a.GpusPerNode-job.GPUs < b.GpusPerNode-job.GPUs
	}
	if a.CpusPerNode-job.CPUs != b.CpusPerNode-job.CPUs {
		return a.CpusPerNode-job.CPUs < b.CpusPerNode-job.CPUs
	}
	if a.MemoryGbPerNode-job.MemoryGB != b.MemoryGbPerNode-job.MemoryGB {
		return a.MemoryGbPerNode-job.MemoryGB < b.MemoryGbPerNode-job.MemoryGB
	}
	return a.ID < b.ID
}
