# mini-modal

A learning project for building a small Modal-like compute platform in Go.

The curated [learning roadmap](docs/learning-roadmap.md) connects each project
milestone to official architecture documentation, engineering articles, videos,
open-source implementations and distributed-systems material.

The current version has a SQLite-backed control plane, a public HTTP API, gRPC
communication with polling node agents, durable assignment leases, a fake
executor, and a browser dashboard with a local development-agent launcher.
There is no container runtime or real GPU execution yet.

## Run it

```bash
go build -o ./tmp/agent ./cmd/agent
go run ./cmd/controlplane
```

Open [http://localhost:8080](http://localhost:8080) to use the dashboard. From
there you can launch local agent processes, submit jobs, and watch scheduling,
resource reservations, leases, completions, and failures update live. Launched
agents still register, heartbeat, claim work, and report results over gRPC.

To run a real agent process against the control plane:

```bash
go run ./cmd/agent -node-id node-a -cpus 8 -memory-gb 16
```

For automatic rebuilds and restarts while developing:

```bash
go tool air
```

Air watches Go and embedded web source files, rebuilds both executables, and
restarts `cmd/controlplane` after a change. It is a development tool and is not
used in production.

The API listens on `http://localhost:8080`:

```text
POST /v1/jobs
GET  /v1/jobs
GET  /v1/jobs/{id}
GET  /v1/nodes
GET  /v1/dev/nodes
PUT  /v1/dev/nodes
DELETE /v1/dev/nodes
```

## Test it

```bash
go test ./...
go test -race ./...
```

## Useful Go commands

| TypeScript habit | Go equivalent |
| --- | --- |
| `npm init` | `go mod init mini-modal` |
| `npm run dev` | `go tool air` |
| `npm test` | `go test ./...` |
| `npm run build` | `go build ./...` |
| generated code script | `go generate ./...` |
| formatter/linter | `gofmt -w .` and `go vet ./...` |
| install dependencies | `go get example.com/package` |

## Current milestone

The project currently:

- sorts jobs by priority;
- finds a node with enough CPU, memory, and GPUs;
- checks GPU type compatibility;
- reserves resources without exceeding capacity;
- distinguishes temporarily pending jobs from unschedulable jobs;
- exposes job submission and inspection through JSON over HTTP;
- lets node agents claim assigned work;
- stores jobs, nodes, attempts, and assignment leases in SQLite;
- connects agent processes to the control plane over gRPC;
- runs assignments with a fake executor before reporting completion or failure;
- requeues work after an assignment lease expires; and
- provides a dashboard and a local development-agent process manager.

The current request path is:

```text
dashboard / HTTP API -> control-plane service -> SQLite -> scheduler
                              ^                         |
                              |                         v
node agent -> gRPC -> claim -> fake executor -> completion or failure
                              ^
locally launched agent -------+
```

## Suggested milestones

### 1. Scheduler core

- Add FCFS, priority, bin-packing, and spreading policies.
- Add job completion and resource release.
- Add a deterministic simulator that generates workloads.
- Benchmark scheduling latency and decisions per second.

### 2. Control plane

- Extend the HTTP API with cancellation and internal agent operations.
- Add PostgreSQL persistence and migrations.
- Add job state transitions and idempotency keys.

### 3. Data plane

- Move the node agent into its own process and connect it over an internal API.
- Add heartbeats and leases.
- Replace fake execution with local Docker containers.
- Stream logs and collect exit codes.

### 4. Distributed systems

- Add scheduler leader election.
- Recover assignments after scheduler or node failure.
- Add multiple queues, quotas, fairness, preemption, and gang scheduling.
- Add an autoscaler and warm worker pools.

Do not add these pieces until the scheduler core is understandable and tested.
