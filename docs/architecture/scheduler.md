# Scheduler — LLD

**Code:** `internal/scheduler/scheduler.go`, `internal/domain/domain.go`,
and `Service.scheduleLocked` in `internal/controlplane/service.go`.

The SQL pending-job query orders by priority descending and job ID, so the
policy receives deterministic order. `Policy.Schedule` walks jobs in that
order and chooses the first node whose currently available GPU type/count,
CPU, and memory fit. It reserves resources in its in-memory node slice so a
later job sees reduced capacity.

The policy returns assignments, temporarily pending jobs, and jobs that no
supplied node can ever fit. The control plane persists assignments one by
one, guarded by `status = 'pending'`. For the last category it also checks
all registered nodes before marking a job `unschedulable`: a compatible but
temporarily unavailable node should leave the job pending. Registration
requeues unschedulable jobs for another pass.

Node availability is reconstructed from PostgreSQL nodes plus active
`assigned`, `running`, and `cancelling` jobs. Drain and process-local
heartbeat health filter nodes out of new placement. The policy does not
itself lock database rows or provision nodes.

This is deliberately first fit, not a fairness or bin-packing optimizer.
Each pass loads every eligible pending job, then scans nodes per job; large
queues can make the scheduler pass expensive. Capacity planning is a
separate pass and can be briefly out of sync with placement.
