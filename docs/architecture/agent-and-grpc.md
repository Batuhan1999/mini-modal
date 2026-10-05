# Agent and gRPC contract — LLD

**Code:** `proto/agent/v1/agent.proto`, generated code in `gen/agent/v1/`,
`internal/transport/agentgrpc/`, `internal/agent/`, `cmd/agent/main.go`.

The agent registers its node ID, pool ID, and resources, then heartbeats
every second. It polls `Claim` on a configurable interval (50 ms in the
local binary). Claims return an assignment with job ID, node ID, attempt,
artifact key, image, Python entrypoint, JSON input, resource limits, and
timeout. The gRPC client implements the agent's control-plane interface;
the server translates RPCs to control-plane calls.

For each claimed assignment, the agent starts execution in a goroutine. A
five-second renewal loop extends the lease and learns whether cancellation
was requested. The execution context also enforces the function timeout.
On completion it reports JSON output; on failure it reports a reason and
error string. Transient reporting errors are retried while the parent
context remains active. A stale-attempt response ends retries because a new
attempt may already own the job.

Stdout, stderr, and system messages share a monotonically increasing sequence
number. A buffered channel feeds a client-streaming `StreamLogs` RPC. The
server validates job ID, attempt, source, sequence, and payload before
persisting chunks. If log transport ends early, execution continues, but
those logs can be lost.

Drain is separate from execution cancellation: a drain response stops new
claims and waits for already started assignments. A cancellation response
from lease renewal cancels one assignment's execution context. There is no
agent authentication or encrypted gRPC transport in the local setup.
