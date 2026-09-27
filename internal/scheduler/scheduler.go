package scheduler

import (
	"sort"

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
func (p *Policy) Schedule(jobs []postgresdb.Job, nodes []domain.Node) ([]domain.Assignment, []postgresdb.Job, []postgresdb.Job) {
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].Priority > jobs[j].Priority
	})

	assignments := make([]domain.Assignment, 0, len(jobs))
	pending := make([]postgresdb.Job, 0)
	unschedulable := make([]postgresdb.Job, 0)

	for _, job := range jobs {
		assigned := false

		canPossiblyRun := false
		for i := range nodes {
			if nodes[i].CanPossiblyRun(job) {
				canPossiblyRun = true
			}

			if !nodes[i].CanRun(job) {
				continue
			}

			nodes[i].Reserve(job)
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
