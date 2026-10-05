# Local provisioner — LLD

**Code:** `internal/provisioner/manager.go`, wired by
`cmd/controlplane/main.go` when `-auto-provision=true`.

The provisioner is the actuator for `node_pools.desired_nodes`. Every second
it reads autoscaling pools and active jobs, compares desired counts with the
agent processes it owns, and reconciles the difference. It starts at most
two new agents per pass and caps locally owned processes at 32. Each process
receives a generated node ID, pool resources, a gRPC address, and the Docker
executor flag. Agents register and heartbeat through the normal gRPC path.

Scale-down chooses excess non-busy processes, marks their nodes draining
through the control plane, and lets the heartbeat response tell the agent to
stop claiming work. If the process has not registered, it is interrupted
directly. On exit, the provisioner marks the node draining and removes its
registration. `Stop` interrupts owned processes; after two seconds it kills
remaining ones.

The process map is local and ephemeral. A separate control plane cannot see
or manage these handles; this module does not create a machine or VM. It
counts owned live processes, not externally started agents. A process that
remains alive but stops heartbeating is not automatically replaced just
because capacity planning regards its node as unhealthy.
