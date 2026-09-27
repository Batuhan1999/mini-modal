# Idempotent submission: three-run benchmark

Run on 2026-09-27. Source base commit: `c13c6551f04fe7ed075271f1a95d812ed5f445b0`; the source changes were uncommitted, and every result records `git_dirty: true`. The change set includes removing the `Submit` mutex, returning the existing job for a matching duplicate ID, and returning HTTP 409 for a conflicting duplicate ID.

Each scenario ran three times in a fresh, resource-limited sibling container with its own temporary SQLite database and control plane. The long-lived Cursor development container remained running. Limits per benchmark container were 4 CPUs, 6 GiB memory, and 1,024 PIDs. The simulator used seed 1 and the default `run-benchmark.sh` workload for each scenario.

| Scenario | Jobs / submitters / agents | Mean admission jobs/s (range) | Mean submission p95 | Mean submission p99 | Mean terminal jobs/s |
| --- | --- | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 / 1 / 0 | 389.0 (356.3–431.0) | 5.1 ms | 7.6 ms | — |
| Admission, concurrent | 1,000 / 32 / 0 | 452.7 (391.4–509.2) | 209.5 ms | 342.8 ms | — |
| Fake executor, end to end | 256 / 32 / 8 | 538.1 (521.7–556.0) | 151.9 ms | 236.0 ms | 22.3 |
| Process executor, end to end | 128 / 16 / 4 | 559.0 (506.9–593.9) | 71.4 ms | 105.5 ms | 13.9 |

These are arithmetic means of each run's throughput or latency figure; the p99 column is the mean of three per-run p99 values, not a pooled percentile. All 12 runs had zero submission errors and zero OOM kills. All 768 fake-executor jobs and 384 process-executor jobs succeeded. The simulator generated a distinct ID for every submission, so this benchmark did **not** measure the duplicate-ID path. Tests cover matching and conflicting retries.

The serial admission rate varied by about 75 jobs/s across just three runs. The active development container and other host workloads were not held constant, and previous benchmarks stopped that container. Do not treat differences from those earlier results as a causal speedup or regression. A publication-grade comparison needs committed source and the same background workload for both versions.

Raw JSON and Prometheus snapshots are in this directory with labels `idempotent-submit-uncommitted-r1`, `idempotent-submit-uncommitted-r2`, and `idempotent-submit-uncommitted-r3`.
