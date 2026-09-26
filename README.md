# mini-modal

A learning project for building a small Modal-like compute platform in Go.

The curated [learning roadmap](docs/learning-roadmap.md) connects each project
milestone to official architecture documentation, engineering articles, videos,
open-source implementations and distributed-systems material.

The current version has a SQLite-backed control plane, a public HTTP API, gRPC
communication with polling node agents, durable assignment leases, retries,
cancellation, streamed logs, a Docker executor, Prometheus metrics, and a
browser dashboard with a local development-agent launcher. There is no real
GPU execution yet.

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

### Resource-limited development box

To develop and benchmark inside a fixed 4-CPU, 6-GiB, 1,024-PID Linux
environment:

```bash
make box-build
make box-up
make box-limits
make box-dev
```

Run `make box-dev` in one terminal and use `make box-shell` from another. Cursor
can also reopen the repository through `.devcontainer/devcontainer.json`.
Configuration and benchmark methodology are documented in
[docs/benchmarking.md](docs/benchmarking.md).

The API listens on `http://localhost:8080`:

```text
POST /v1/jobs
GET  /v1/jobs
GET  /v1/jobs/{id}
POST /v1/jobs/{id}/cancel
GET  /v1/jobs/{id}/logs
GET  /v1/jobs/{id}/events
GET  /v1/nodes
GET  /v1/dev/nodes
PUT  /v1/dev/nodes
DELETE /v1/dev/nodes
GET  /metrics
```

## Run a workload simulation

The simulator is an HTTP client, not an in-process fake. Start the control
plane and enough agents first, then run:

```bash
go run ./cmd/simulator -jobs 100 -rate 10 -failure-fraction 0.1 -timeout-fraction 0.1
```

`-rate 0` submits the workload as a burst. The seed is deterministic, and the
simulator waits for its jobs to reach a terminal state before printing a
summary. Under heavier local Docker load, increase `-timeout-headroom` so
container startup does not turn intended successes into timeouts. See every
option with `go run ./cmd/simulator -help`.

For admission-only measurements, use concurrent submitters without retaining
or polling every job:

```bash
go run ./cmd/simulator -jobs 10000 -rate 0 -concurrency 32 -submit-only
```

## Observe it with Prometheus and Grafana

The control plane exports process, Go runtime, scheduler, queue, job, retry,
lease, log, node-health, and resource metrics at
[http://localhost:8080/metrics](http://localhost:8080/metrics).

With the control plane running on the host, start the local observability
stack:

```bash
docker compose -f compose.observability.yml up -d
```

- Prometheus: [http://localhost:9091](http://localhost:9091)
- Grafana: [http://localhost:3000](http://localhost:3000)

Grafana starts with an anonymous read-only login, the Prometheus data source,
and the **Mini Modal Overview** dashboard already provisioned. Stop the stack
with `docker compose -f compose.observability.yml down`.

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
- runs assignments in Docker containers with CPU and memory limits;
- streams stdout, stderr, and system logs to the control plane;
- retries failed or lease-expired attempts with configurable backoff;
- supports execution timeouts and cancellation;
- requeues work after an assignment lease expires; and
- provides a dashboard, a local development-agent process manager, a public
  workload simulator, and Prometheus/Grafana observability.

The current request path is:

```text
dashboard / HTTP API -> control-plane service -> SQLite -> scheduler
                              ^                         |
                              |                         v
node agent -> gRPC -> claim -> Docker executor -> completion or failure
                              ^
locally launched agent -------+
```

## Suggested milestones

### 1. Load and reliability

- Add named simulator scenarios and machine-readable result reports.
- Measure scheduler throughput and HTTP/gRPC latency under increasing load.
- Exercise agent loss, control-plane restart, and slow log-upload scenarios.

### 2. Production persistence

- Move from SQLite to PostgreSQL when multiple control-plane replicas are
  needed.
- Add idempotency keys for submission and durable node-heartbeat ownership.

### 3. Isolation and images

- Add image caching and warm container pools.
- Add stronger sandboxing with a microVM runtime.
- Add network, filesystem, and execution-policy controls.

### 4. Distributed scheduling

- Add scheduler leader election.
- Add multiple queues, quotas, fairness, preemption, and gang scheduling.
- Add an autoscaler and warm worker pools.
