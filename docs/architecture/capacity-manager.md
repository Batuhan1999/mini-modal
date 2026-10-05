# Capacity manager — LLD

**Code:** `internal/capacity/manager.go` and the capacity queries in
`internal/store/postgres/queries/`.

`Manager.Run` reconciles immediately and then once per second. It reads
autoscaling pools, registered nodes, active jobs, and due `pending` or
`unschedulable` jobs. Its only durable output is each pool's
`desired_nodes`; it never starts a process or assigns a job.

The planning pass builds in-memory bins from healthy, non-draining nodes.
Active jobs consume resources in their assigned bin. Waiting jobs are tried
against remaining space, in query order. If no bin fits, the planner adds a
hypothetical node from an eligible pool (preferring a CPU-only pool for
CPU-only work and then less spare capacity). It also counts active work on
lost nodes as demand for replacement capacity. `min_nodes`, `max_nodes`,
and `idle_timeout_seconds` limit the final desired count.

An idle node remains desired until its timeout expires. The timeout is held
in process memory; a restart restarts the idle observation period. SQL updates
use `IS DISTINCT FROM`, so an unchanged desired count is not rewritten.

This is a prediction over a snapshot, not a reservation or a transaction with
the scheduler. Health comes from the current control plane's memory. A pool
can request capacity that has not registered yet, and a busy or stuck agent
can delay convergence. The planner loads the full eligible job set; it is not
yet designed for very large queues or multiple concurrent planners.
