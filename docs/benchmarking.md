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
limit. The control plane and agents share the 4-CPU / 6-GiB application container.
The load generator runs in a separate 2-CPU / 2-GiB container; those resources
are not part of the application's quota. The benchmark
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

Keep generated JSON summaries and 100-ms application/load-generator/PostgreSQL resource traces
in `benchmarks/results/`. The JSON reports average CPU cores and utilization
against each container's CPU limit, peak memory and percent of its limit, CPU
throttling, process count, and OOM kills. The traces preserve bursts that an
average would hide.

The portfolio workload is `hello-process-e2e`: one uploaded Python function
version, many invocations, real agent processes and host-process execution. It
prints and returns a small result without sleeping. The older `process-e2e`
scenario deliberately sleeps for one second and is not comparable. Fake
execution and serial admission remain historical diagnostics, not headline
platform measurements. Admission-only throughput depends strongly on client
concurrency and does not measure job completion.

## Initial scenarios

Each target recreates the database in the shared benchmark Postgres container,
starts a fresh application container with its own control plane and agents,
submits from a separate load-generator container, then removes the application
container when the run finishes:

```bash
make benchmark-admission-serial
make benchmark-admission-concurrent
make benchmark-fake
make benchmark-process
make benchmark-hello-process
```

The admission scenarios stop timing after HTTP submission, but background
scheduling still runs and can affect throughput. The end-to-end scenarios
start agents and wait for every job to reach a terminal status. Override safe
defaults without editing scripts, for example:

```bash
JOBS=10000 CONCURRENCY=64 make benchmark-admission-concurrent
```

To separate scheduler-advertised capacity from physical limits, set
`NODE_CPUS` and `NODE_MEMORY_GB`. These values change placement only. They do
not allocate CPU or RAM to an agent, and the process executor does not enforce
them. `AGENTS` controls the number of independent claim loops. The benchmark
application container still has 4 real CPU cores and 6 GiB regardless of the
advertised node values.

```bash
JOBS=2048 AGENTS=16 NODE_CPUS=64 NODE_MEMORY_GB=128 \
  BENCHMARK_LABEL=resource-16 make benchmark-hello-process
```

At 100,000 jobs, the simulator's ordinary HTTP observer would fetch the entire
job list every second and contaminate the load. Use the benchmark-only
PostgreSQL status-count observer instead. It still waits for all jobs, but
reads only two aggregate counts every two seconds. `timing` in the result JSON
comes from per-job PostgreSQL timestamps; the observer does not determine the
reported completion rate. The longer outer timeout accommodates a sustained
run while the app and database remain in their resource-limited containers.
The observer waits up to 900 seconds by default (`BENCHMARK_OBSERVER_TIMEOUT_SECONDS`
overrides this). On a timeout, the command fails **after** saving a JSON marked
`outcome: observer_timeout_partial` and both resource traces. A partial run is
useful diagnostic evidence, but its completed/s figure is not comparable to a
fully drained run. Set the outer `BENCHMARK_TIMEOUT_SECONDS` higher than the
observer timeout to leave time for final metrics and cleanup.

```bash
BENCHMARK_DB_OBSERVER=true BENCHMARK_TIMEOUT_SECONDS=1200 \
  JOBS=100000 CONCURRENCY=32 AGENTS=16 NODE_CPUS=64 NODE_MEMORY_GB=128 \
  BENCHMARK_LABEL=resource-16-100k make benchmark-hello-process
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
100-ms cgroup traces for the application, load generator, and Postgres to
`benchmarks/results/`. The JSON `workload_usage` section measures the application
container from the generator's start signal through completion observation;
`loadgen_usage` measures the generator's own submission and observation work;
`postgres_usage` measures Postgres over the application window.
`container_usage` covers the application container's entire lifetime, including
startup and builds. Application CPU now combines only the control plane and agents.
Postgres and MinIO still have separate quotas, but all containers share the Mac's
physical CPU, memory, and storage; these are not dedicated-host measurements.
Block-I/O byte counts can miss writes that the
kernel flushes after the simulator ends, so interpret I/O pressure together
with the byte counts. No benchmark ports are published to the host, so a
local control plane can keep running, though its resource use may add noise.

Results from the old shared application/Postgres container are not directly
comparable with these split-container results.
