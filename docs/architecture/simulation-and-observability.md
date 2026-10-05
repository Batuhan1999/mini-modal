# Simulation and observability — LLD

**Code:** `cmd/simulator/`, `internal/simulator/`, `scripts/`,
`compose.benchmark.yml`, `internal/controlplane/metrics.go`, `deploy/`.

The simulator is an external HTTP client. Before the timed phase, it uploads
one artifact and creates one function version; generated jobs share that
version. It can send at a fixed rate or as a burst, vary concurrency, and
either stop after submission or poll until jobs reach terminal states.
Submission-only numbers stop timing when submissions return; they do not wait
for execution. Background scheduling still runs and can compete for resources.

The benchmark harness builds a short-lived application container limited to
4 CPUs and 6 GiB, plus a separate PostgreSQL container limited to 4 CPUs and
4 GiB. It recreates only `mini_modal_benchmark` before each run, disables
automatic provisioning, and starts the configured number of agents itself
for end-to-end scenarios. JSON summaries capture commit ID, dirty-tree flag,
latency percentiles, throughput, and resource samples. Historical results
are retained in `benchmarks/results/` and have different configurations;
compare only matching workloads and limits.

The control plane exports Prometheus counters, gauges, and histograms for
submissions, attempts, scheduler runs, queue wait, durations, logs, nodes,
and resources. Prometheus scrapes `/metrics`; Grafana uses the provisioned
dashboard. Some duration observations depend on in-memory timestamps and
do not reconstruct across a restart. The harness measures application and
PostgreSQL cgroups separately, but both still compete for physical host
resources.

See [benchmarking methodology](../benchmarking.md) before interpreting a
result. The current harness does **not** benchmark automatic scale-up or
Docker-isolated execution inside its fixed application container.
