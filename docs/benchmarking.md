# Benchmarking mini-modal

The benchmark environment is intentionally fixed so architectural changes can
be compared rather than accidentally measuring a different machine setup.

## Fixed environment

The application benchmark container defaults to:

- 4 CPU cores;
- 6 GB RAM with no additional swap allowance;
- 1,024 processes;
- Go 1.27 on Debian Bookworm.

One PostgreSQL 15 container is reused across runs with its own 4-CPU / 4-GiB
limit. The application container retains its 4-CPU / 6-GiB limit. The benchmark
database `mini_modal_benchmark` is dropped and recreated before every run;
the Postgres container and volume remain. The separate `compose.dev.yml`
development database is not touched.
The local MinIO service and its artifact bucket are also shared across runs;
the benchmark application container reaches it at `http://minio:9000` on the
`mini-modal-objectstore` network. The benchmark launcher starts MinIO if
needed and does not reset its volume. MinIO is capped at 1 CPU and 1 GiB of
memory. Once artifact uploads are benchmarked,
record whether code was already present in that bucket so warm and cold runs
are compared separately.

Check the effective cgroup limits with:

```bash
make benchmark-limits
```

Docker still runs on the Mac. These cgroup limits are per-container ceilings,
not reserved physical resources; other host workloads can affect timings. Fake
and process executor benchmarks stay inside the application container boundary.
Docker-executor benchmarks are separate because access to the host
Docker socket would allow child containers to consume resources outside the
benchmark container.

The optional Prometheus and Grafana containers are separately capped at 1 CPU /
1 GiB and 0.5 CPU / 512 MiB, respectively. A benchmark container's CPU and
memory limits are ceilings, not reservations; other host or Docker workloads
can still affect timings.

## Develop locally

```bash
cp -n .env.example .env
docker compose -f compose.dev.yml up -d --wait postgres
make objectstore-up
go tool air
```

Run tests and generators with your local Go toolchain. When comparing versions,
stop local development processes or keep their load consistent across both
versions. The benchmark commands below build and remove their application
containers automatically. The benchmark Postgres container stays up for reuse;
stop it with `make benchmark-postgres-stop` when finished.

For a local simulation against your development control plane:

```bash
go run ./cmd/simulator -jobs 1000 -rate 0 -concurrency 1 -submit-only
```

## Measurement ladder

Do not begin with one million jobs. Run the same scenario at increasing sizes:

1. 1,000 jobs;
2. 10,000 jobs;
3. 100,000 jobs;
4. 1,000,000 jobs only after the lower levels remain stable.

For admission tests, use `-submit-only`. For end-to-end tests, omit it and run
agents with the desired executor.

Record these values for every architectural change:

- Git commit and a short description of the change;
- exact simulator command and random seed;
- accepted jobs per second;
- submission latency p50, p95, and p99;
- scheduler p95 latency;
- agent claimed and finished attempts per second;
- queue-wait p95;
- terminal jobs per second;
- peak memory, CPU, database size, and errors.

Keep generated JSON summaries in `benchmarks/results/` so they can become the
source material for graphs and the eventual blog post.

## Initial scenarios

Each target starts a fresh application container with its own control plane,
recreates the database in the shared benchmark Postgres container, then removes
the application container when the run finishes:

```bash
make benchmark-admission-serial
make benchmark-admission-concurrent
make benchmark-fake
make benchmark-process
```

The admission scenarios stop after HTTP submission. The end-to-end scenarios
start agents and wait for every job to reach a terminal status. Override safe
defaults without editing scripts, for example:

```bash
JOBS=10000 CONCURRENCY=64 make benchmark-admission-concurrent
```

Use `BENCHMARK_LABEL` to identify an architecture revision in filenames and
metadata:

```bash
BENCHMARK_LABEL=background-scheduler make benchmark-admission-serial
```

Use `DB_MAX_OPEN_CONNS` to compare PostgreSQL connection-pool sizes without
changing the source. The default is 20, chosen from the dedicated 4-core
Postgres benchmark; the metadata records the selected size. Treat it as a
workload-specific starting point, not a universal optimum.

```bash
DB_MAX_OPEN_CONNS=2 BENCHMARK_LABEL=dbpool2 make benchmark-admission-concurrent
```

Every run writes a metadata/result JSON file, a Prometheus snapshot, and separate
100-ms cgroup traces for the application and Postgres containers to
`benchmarks/results/`. The JSON `workload_usage` section measures the application
container during the simulator invocation; `postgres_usage` measures the
dedicated Postgres container over the same window. `container_usage` covers the
application container's entire lifetime, including startup and builds. The
application measurements still combine the control plane, agents, and simulator.
Block-I/O byte counts can miss writes that the
kernel flushes after the simulator ends, so interpret I/O pressure together
with the byte counts. No benchmark ports are published to the host, so a
local control plane can keep running, though its resource use may add noise.

Results from the old shared application/Postgres container are not directly
comparable with these split-container results.
