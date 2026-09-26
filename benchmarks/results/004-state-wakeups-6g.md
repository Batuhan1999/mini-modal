# Experiment: wake the scheduler after job state changes

Code commit: `18d65f89063d69e0d6a336011256b062e4ee9d15`

Environment: 4 CPU cores, 6 GiB memory, 1,024 PID limit, Go 1.27.1,
Linux/arm64. Each scenario used a fresh SQLite database and seed `1`.
All results recorded `git_dirty: false`, zero submission errors, and zero
OOM kills. Every end-to-end job succeeded.

The previous version woke the scheduler after submission but scheduled
synchronously after completion, failure, cancellation, and node registration.
This version uses the same capacity-one wake-up channel for those transitions.
The one-second loop still handles lease expiration, retry release, periodic
scheduling, and metrics refresh.

| Scenario | Jobs | Submitters | Admission throughput | p50 | p95 | p99 | E2E throughput | Scheduler runs |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 | 1 | 350.0 jobs/s | 2.26 ms | 5.67 ms | 8.83 ms | n/a | 265 |
| Admission, concurrent | 1,000 | 32 | 456.3 jobs/s | 66.96 ms | 91.92 ms | 95.51 ms | n/a | 30 |
| Fake executor | 256 | 32 | 400.2 jobs/s | 70.86 ms | 128.07 ms | 129.96 ms | 22.0 jobs/s | 99 |
| Process executor | 128 | 16 | 652.9 jobs/s | 18.96 ms | 47.86 ms | 48.87 ms | 13.9 jobs/s | 68 |

## Comparison with batched submission wake-ups

The previous clean benchmark used commit `af50cae` with identical scenario
settings and container limits.

| Scenario | Previous E2E throughput | Current E2E throughput | Previous scheduler runs | Current scheduler runs |
| --- | ---: | ---: | ---: | ---: |
| Fake executor | 21.9 jobs/s | 22.0 jobs/s | 286 | 99 |
| Process executor | 13.7 jobs/s | 13.9 jobs/s | 150 | 68 |

Scheduler passes fell by 65.4% in the fake executor run and 54.7% in the
process executor run. End-to-end throughput was nearly unchanged. That is
consistent with execution capacity, agent polling, and other fixed costs
dominating these workloads after the scheduling overhead was reduced.

The admission-only runs changed from 416.5 to 350.0 jobs/s (serial) and from
426.9 to 456.3 jobs/s (concurrent). Those scenarios do not exercise any of the
newly changed state-transition paths. Their mixed differences therefore cannot
be attributed to this code change from these measurements; they illustrate
run-to-run variation in this small benchmark.

The scheduler run counter includes the one-second maintenance passes. At these
job counts, the completion/failure change mainly reduces unnecessary passes.
Larger workloads and repeated runs are needed to measure whether it raises the
system's throughput ceiling.

Raw results and Prometheus snapshots share the `state-wakeup-18d65f8` label
in this directory, one JSON and one `.prom` file per scenario.
