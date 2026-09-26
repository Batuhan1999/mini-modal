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
