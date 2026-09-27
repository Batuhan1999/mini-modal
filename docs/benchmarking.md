# Benchmarking mini-modal

The benchmark environment is intentionally fixed so architectural changes can
be compared rather than accidentally measuring a different machine setup.

## Fixed environment

Each benchmark container defaults to:

- 4 CPU cores;
- 6 GB RAM with no additional swap allowance;
- 1,024 processes;
- Go 1.27 on Debian Bookworm.

Check the effective cgroup limits with:

```bash
make benchmark-limits
```

Docker still runs on the Mac, but these cgroup limits bound the processes inside
each benchmark container. Fake and process executor benchmarks stay inside this
boundary. Docker-executor benchmarks are separate because access to the host
Docker socket would allow child containers to consume resources outside the
benchmark container.

The optional Prometheus and Grafana containers are separately capped at 1 CPU /
1 GiB and 0.5 CPU / 512 MiB, respectively. A benchmark container's CPU and
memory limits are ceilings, not reservations; other host or Docker workloads
can still affect timings.

## Develop locally

```bash
go tool air
```

Run tests and generators with your local Go toolchain. When comparing versions,
stop local development processes or keep their load consistent across both
versions. The benchmark commands below build and remove their containers
automatically; they do not need a long-lived development container.

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

Each target starts a fresh resource-limited container with its own control plane
and temporary database, then removes that container when the run finishes:

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

Every run writes one metadata/result JSON file and one raw Prometheus snapshot
to `benchmarks/results/`. No benchmark ports are published to the host, so a
local control plane can keep running, though its resource use may add noise.
