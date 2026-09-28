package scheduler

import (
	"mini-modal/internal/domain"
	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

// Policy contains the placement algorithm used by the control plane.
type Policy struct{}

func NewFirstFitPolicy() *Policy {
	return &Policy{}
}

// Schedule assigns higher-priority jobs first using a deterministic first-fit
// policy. Jobs that do not currently fit remain pending.
func (p *Policy) Schedule(jobs []postgresdb.ListPendingJobsRow, nodes []domain.Node) ([]domain.Assignment, []postgresdb.ListPendingJobsRow, []postgresdb.ListPendingJobsRow) {

	assignments := make([]domain.Assignment, 0, len(jobs))
	pending := make([]postgresdb.ListPendingJobsRow, 0)
	unschedulable := make([]postgresdb.ListPendingJobsRow, 0)

	for _, job := range jobs {
		assigned := false
		resources := domain.Resources{GPUType: job.GpuType, GPUs: job.Gpus, CPUs: job.Cpus, MemoryGB: job.MemoryGb}

		canPossiblyRun := false
		for i := range nodes {
			if nodes[i].CanPossiblyRun(resources) {
				canPossiblyRun = true
			}

			if !nodes[i].CanRun(resources) {
				continue
			}

			nodes[i].Reserve(resources)
			assignments = append(assignments, domain.Assignment{
				JobID:  job.ID,
				NodeID: nodes[i].ID,
			})
			assigned = true
			break
		}

		if !canPossiblyRun {
			unschedulable = append(unschedulable, job)
		}

		if canPossiblyRun && !assigned {
			pending = append(pending, job)
		}
	}

	return assignments, pending, unschedulable
}
