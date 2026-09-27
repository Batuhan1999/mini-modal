# mini-modal

A learning project for building a small Modal-like compute platform in Go.

The curated [learning roadmap](docs/learning-roadmap.md) connects each project
milestone to official architecture documentation, engineering articles, videos,
open-source implementations and distributed-systems material.

The current version has a PostgreSQL-backed control plane, a public HTTP API, gRPC
communication with polling node agents, durable assignment leases, retries,
cancellation, streamed logs, a Docker executor, Prometheus metrics, and a
browser dashboard with a local development-agent launcher. There is no real
GPU execution yet.

## Run it

```bash
cp -n .env.example .env
docker compose -f compose.dev.yml up -d --wait postgres
make objectstore-up
go tool air
```

The local Compose file starts only Postgres. The Go control plane and agents run
on your Mac. Air and `go run ./cmd/controlplane` read `DATABASE_URL` from
`.env`; the file is ignored by Git. Existing SQLite jobs are not copied to
Postgres. The named Docker volume persists data across `docker compose down`;
stop the database with
`docker compose -f compose.dev.yml down` when finished.

MinIO is a separate, persistent local S3-compatible service. Its API is at
`http://127.0.0.1:9000` and its console is at `http://127.0.0.1:9001`.
The development credentials and bucket are in `.env.example`; change them in
your ignored `.env` if needed. `make objectstore-up` creates the
`mini-modal-artifacts` bucket if it does not exist. `make objectstore-stop`
stops MinIO without deleting its volume. The benchmark containers use the
same MinIO instance over Docker's `mini-modal-objectstore` network at
`http://minio:9000`. The control plane uses the S3-compatible API to store
function archives.
This Mac already has the pinned MinIO image cached. The Compose file uses that
local image because pulls from MinIO's published registries failed during
setup; do not prune that image unless you arrange another image source first.

Open [http://localhost:8080](http://localhost:8080) to use the dashboard. From
there you can launch local agent processes, submit jobs, and watch scheduling,
resource reservations, leases, completions, and failures update live. Launched
agents still register, heartbeat, claim work, and report results over gRPC.
The Functions panel lets you upload `hello_function.py` from the repository
root, deploy a version, inspect versions, and create an invocation job. The
Docker executor downloads each invocation's artifact, extracts it into a
temporary directory, and mounts it read-only at `/workspace`. The directory is
removed after the container exits, so `python hello_function.py` can run from
the deployed version.

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

### Resource-limited benchmarks

Develop locally using `go tool air`. Benchmarks use a short-lived application
container (4 CPU, 6 GiB, 1,024 PIDs) and a reusable PostgreSQL container with
its own 4-CPU / 4-GiB limit:

```bash
make benchmark-limits
make benchmark-admission-serial
make benchmark-admission-concurrent
make benchmark-fake
make benchmark-process
```

Each run starts its own control plane and agents when needed. The benchmark
database is recreated before every run; the dedicated PostgreSQL container
stays up between runs and can be stopped with `make benchmark-postgres-stop`.
The benchmark launcher starts the shared MinIO service if needed, but never
clears its bucket.
No persistent development container is required. See
[docs/benchmarking.md](docs/benchmarking.md) for the methodology.

The API listens on `http://localhost:8080`:

```text
POST /v1/jobs
POST /v1/functions/{id}/artifacts
POST /v1/functions/{id}/versions
POST /v1/functions/{id}/jobs
GET  /v1/functions
GET  /v1/functions/{id}/versions
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

`POST /v1/functions/{id}/artifacts` accepts a raw `.tar.gz` archive with
`Content-Type: application/gzip`, or a single `.py` file with
`Content-Type: text/x-python` and `?filename=hello_function.py`. Python source
is packaged into a tar.gz archive before storage. The response includes an
`artifact_key`, SHA-256 digest, and size. Archives are limited to 32 MiB compressed and 256 MiB uncompressed;
only regular files and directories with safe relative paths are accepted.
For example:

```bash
tar -czf function.tar.gz main.py
curl -sS -X POST -H 'Content-Type: application/gzip' \
  --data-binary @function.tar.gz \
  http://localhost:8080/v1/functions/greet/artifacts
```

For a single Python file, upload `hello_function.py` directly in the
dashboard; no manual `tar` step is needed.

`POST /v1/functions/{id}/versions` creates an immutable, reusable runtime
version and returns its `id`. Supply `artifact_key`, `image`, `command`, `cpus`,
and `memory_gb`; `args`, GPU resources, duration, and retry settings are
optional. The artifact key must belong to that function and exist in the
configured bucket.
`POST /v1/functions/{id}/jobs` accepts `id`, `version_id`, and optional
`priority` to submit an invocation. Each job keeps the exact version ID and
snapshots its runtime settings for scheduling. The Docker executor downloads
and runs the version's artifact for each invocation.
Direct `POST /v1/jobs` submissions also require `function_version_id` from a
deployed version. There is no legacy job format or database upgrade path for
jobs without a version; recreate an older development database before running
this schema.

## Run a workload simulation

The simulator is an HTTP client, not an in-process fake. Start the control
plane and enough agents first, then run:

```bash
go run ./cmd/simulator -jobs 100 -rate 10 -failure-fraction 0.1 -timeout-fraction 0.1
```

Each simulator run uploads one small artifact and deploys one function version
before the timed submission phase. All jobs in that run reference the same
version; this setup is excluded from submission throughput and latency.

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
MINI_MODAL_TEST_DATABASE_URL='postgres://mini_modal:mini_modal@localhost:5432/mini_modal?sslmode=disable' go test ./...
MINI_MODAL_TEST_DATABASE_URL='postgres://mini_modal:mini_modal@localhost:5432/mini_modal?sslmode=disable' go test -race ./...
```

Integration tests create and remove their own Postgres schemas; they do not
delete your development jobs. Without `MINI_MODAL_TEST_DATABASE_URL`, the
Postgres-dependent tests are skipped.

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
- stores jobs, nodes, attempts, and assignment leases in PostgreSQL;
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
dashboard / HTTP API -> control-plane service -> PostgreSQL -> scheduler
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

- Evaluate contention and correctness with multiple control-plane replicas.
- Add idempotency keys for submission and durable node-heartbeat ownership.

### 3. Isolation and images

- Add image caching and warm container pools.
- Add stronger sandboxing with a microVM runtime.
- Add network, filesystem, and execution-policy controls.

### 4. Distributed scheduling

- Add scheduler leader election.
- Add multiple queues, quotas, fairness, preemption, and gang scheduling.
- Add an autoscaler and warm worker pools.
