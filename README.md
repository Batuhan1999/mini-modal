# mini-modal

A learning project for building a small Modal-like compute platform in Go.

The curated [learning roadmap](docs/learning-roadmap.md) connects each project
milestone to official architecture documentation, engineering articles, videos,
open-source implementations and distributed-systems material.

The current version is intentionally simple: it has an in-memory control plane,
a public HTTP API, polling node agents and a fake executor. There is no database,
Docker runtime or real GPU yet.

## Run it

```bash
go run ./cmd/minimodal
```

For automatic rebuilds and restarts while developing:

```bash
go tool air
```

Air watches the Go source files, rebuilds `cmd/minimodal`, and restarts the
process after a change. It is a development tool and is not used in production.

The API listens on `http://localhost:8080`:

```text
POST /v1/jobs
GET  /v1/jobs
GET  /v1/jobs/{id}
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
- lets node agents claim assigned work; and
- runs assignments with a fake executor before reporting completion or failure.

The current request path is:

```text
HTTP API -> control-plane service -> state -> scheduler
                                      ^
node agent -> claim -> fake executor -+-> completion or failure
```

The control-plane state is still in memory, so jobs disappear when the process
restarts.

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
