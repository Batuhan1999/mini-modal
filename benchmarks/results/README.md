# Benchmark results

Store simulator JSON summaries here using names that identify the architecture
and workload, for example:

```text
001-sync-scheduler-admission-c1-1000.json
002-background-scheduler-admission-c1-1000.json
```

Every comparison should use the same container limits, workload size,
concurrency, seed, and database starting state.

Run each scenario three times for each code version. Report the arithmetic
mean of the three per-run throughput values alongside their range, and keep
each run's JSON and Prometheus snapshot. Compare versions using the same
number of runs; a single run should not be presented as equivalent to a
three-run average. Run versions in alternating order when possible to reduce
the effect of changing host load.

Each JSON result records the full Git commit SHA and whether the working tree
was dirty before the run started. Publication-grade comparisons should use
results where `git_dirty` is `false`.

The [3 October 2026 snapshot](../../docs/benchmark-2026-10-03.md) summarizes
the 12 `portfolio-final-v2` runs and distinguishes measured facts from next
experiments. Those runs used a dirty working tree; keep that caveat with any
quoted number.

The [claim-polling follow-up](../../docs/benchmark-claim-polling-2026-10-03.md)
compares those runs with the 12 `claim-50ms` runs. Admission-only changes in
that comparison are host variation, since those runs have no agents.

The [hello-world process report](../../docs/benchmark-hello-process-2026-10-03.md)
is the primary end-to-end result. Labels `resource-8` through `resource-32`
cover three 2,048-job repetitions per agent count; `resource-16-10k` covers
three longer 10,000-job repetitions. The application and PostgreSQL resource
CSV files sample their cgroups about every 100 ms. The app container combines
the control plane, agents, and simulator; it cannot attribute CPU to one of
those processes. `NODE_CPUS`/`NODE_MEMORY_GB` are scheduler advertisements,
not the container's physical limits.

`resource-16-100k` was a single bounded stress run. Its files carry `.partial`
in the name because only 46,548 of 100,000 submitted jobs completed before the
15-minute observer deadline. The [report](../../docs/benchmark-hello-process-2026-10-03.md)
explains the CPU/memory measurements and why the 51.1/s partial rate must not
be placed in the full-run throughput table.

`batched-20k` and `batched-100k` are single follow-up runs after adding a
10,000-job scheduler batch and pending-priority index. Both drained fully.
Their JSON and resource traces are linked from the same report. These are
diagnostic runs from a dirty working tree, not three-run averages or an
isolated A/B of batching versus indexing.

`compute-sweep-a16`, `compute-sweep-a20`, and `compute-sweep-a24` each have
three 20,000-job hello-world process runs. `compute-sweep-a12`, `a32`, and
`a48` are single-run screens only. `compute-paced-r225` through `r300` are
single paced 20,000-job runs; `compute-paced-100k-r225` and `r250` are the
longer capacity checks. The [hello-world report](../../docs/benchmark-hello-process-2026-10-03.md)
explains why the delivered rate differs from the requested rate and why the
shared-container load generator prevents a platform-only capacity claim.

`isolated-loadgen-smoke` validates the revised harness with 128 jobs.
`isolated-loadgen-100k-r250` and `isolated-loadgen-100k-r275` are single
100,000-job paced runs with the simulator in its own 2-CPU/2-GiB container.
The control plane and agents remain together in the 4-CPU/6-GiB application
container. Each result JSON has `loadgen_usage`; matching
`-loadgen-resources.csv` files hold its cgroup trace. The application and
PostgreSQL traces remain separate. The [report](../../docs/benchmark-hello-process-2026-10-03.md)
compares the two rates and keeps them distinct from the older shared-container
measurements. These runs used a dirty working tree and are not three-run
averages.
