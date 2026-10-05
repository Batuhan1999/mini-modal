# mini-modal

A small function platform built to learn what sits between `submit()` and a
running container. The control plane is Go; function code is Python. Jobs and
function versions live in PostgreSQL, archives live in an S3-compatible store,
and node agents talk to the control plane over gRPC.

This is a working prototype, not a hosted service or a secure multi-tenant
sandbox. It has no authentication, a single control-plane process, and a local
provisioner that starts agent **processes**, not VMs.

## What happens to a function call?

1. The Python SDK packages the function and its reachable local imports. A
   content hash lets unchanged deployments reuse an existing version.
2. An invocation creates a job referencing that immutable version. The HTTP
   request returns after the job is stored; it does not wait for execution.
3. The scheduler assigns work to a healthy node with enough free resources.
   The capacity manager adjusts each pool's desired node count; the local
   provisioner starts or drains agents to follow it.
4. An agent claims its assignment over gRPC, downloads the archive, and runs
   the function in Docker. It streams logs and renews its lease while running.
5. The agent reports a result, failure, or cancellation. A lost lease can
   cause another attempt, so callers must not assume exactly-once execution.

The [high-level design](docs/architecture/README.md) has a diagram and links
to the low-level designs. The [Python SDK guide](sdk/python/README.md) shows
`@app.function`, `spawn()`, and `remote()`.

## Run locally

You need Go 1.27 and Docker with Compose. Copy the development configuration,
start PostgreSQL and the object store, then build the worker image and start
the control plane:

```sh
cp .env.example .env
docker compose -f compose.dev.yml up -d --wait postgres
make objectstore-up
docker build -t mini-modal-python:dev sdk/python
go tool air
```

Air builds both Go binaries and restarts the control plane on source changes.
The dashboard is at [localhost:8080](http://localhost:8080). It can upload a
Python file, deploy a function version, invoke it, and show its jobs, events,
logs, and result. Agents appear only when the capacity manager requests them.
The gRPC listener is on `localhost:9090`.

To run without Air, build the agent first and then start the control plane:

```sh
go build -o ./tmp/agent ./cmd/agent
go run ./cmd/controlplane
```

`.env` is ignored by Git. The example credentials are for local development
only. PostgreSQL and object-store data live in Docker volumes; stopping their
containers does not delete those volumes. The worker image must be available
to the Docker daemon used by an agent.

## Check the system

```sh
go test ./...
go vet ./...
PYTHONPATH=sdk/python python3 -m unittest discover -s sdk/python/tests
```

For PostgreSQL-backed Go tests, set `MINI_MODAL_TEST_DATABASE_URL` to a test
PostgreSQL URL. Those tests create isolated schemas; without the variable they
are skipped. The Python command also needs the SDK dependency installed
(`python3 -m pip install -e sdk/python`).

The fixed-container benchmark harness has separate resource limits for the
application and PostgreSQL. It resets **only** its benchmark database. See
[methodology](docs/benchmarking.md) and [results](benchmarks/results/README.md).
Do not compare an admission-only run with an end-to-end run: they measure
different paths.
The [hello-world process benchmark](docs/benchmark-hello-process-2026-10-03.md)
is the main end-to-end measurement: real agent processes, a short Python
function, and CPU/memory traces for the app and PostgreSQL. The accompanying
[engineering write-up](docs/blog/a-hello-world-function-found-my-scheduler.md)
explains the queue-size bottleneck. Older [baseline](docs/benchmark-2026-10-03.md)
and [claim-polling](docs/benchmark-claim-polling-2026-10-03.md) reports remain
as historical diagnostics.

## Where the code lives

| Path | Role |
| --- | --- |
| `cmd/controlplane`, `internal/controlplane` | HTTP/gRPC process and durable job lifecycle |
| `internal/scheduler` | Priority-ordered first-fit placement |
| `internal/capacity`, `internal/provisioner` | Desired node counts and local agent processes |
| `cmd/agent`, `internal/agent`, `internal/executor` | Claim, lease renewal, log upload, execution |
| `internal/store/postgres`, `internal/artifact` | PostgreSQL state and S3 archives |
| `sdk/python` | Function packaging, deployment cache, invocation client |
| `cmd/simulator`, `scripts`, `benchmarks/results` | Workload generation and measurements |

## Deliberate limits

- One control-plane process owns in-memory node heartbeat state. Multiple
  replicas would need shared health and coordination.
- Local provisioning launches processes on the control-plane host. It is a
  stand-in for a machine or VM provisioner, not one.
- Docker execution is Python-specific and does not provide a microVM security
  boundary. GPU requests participate in placement, but the Docker executor
  does not yet provision real GPUs.
- Scheduling and capacity planning scan current PostgreSQL rows. Large queues,
  fairness, quotas, and warm pools remain open design work.
- Historical SQLite benchmark results remain available, but PostgreSQL is the
  only maintained persistence implementation.

The [architecture review](docs/architecture/README.md#known-gaps) separates
these limits from implemented behaviour. The
[learning roadmap](docs/learning-roadmap.md) records the references that
informed the project.
