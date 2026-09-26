# Experiment: remove scheduling from submission

Code commit: `22580da9872b8974b7d1f949d7c0a350e48092dc`

Environment: 4 CPU cores, 6 GiB memory, 1,024 PID limit, Go 1.27.1,
Linux/arm64. Every run used a fresh SQLite database and seed `1`. All four
results recorded `git_dirty: false`, zero submission errors, and zero OOM kills.

The change removes the synchronous scheduling pass from `Service.Submit`.
Submitted jobs are committed as `pending`, returned to the caller, and picked up
by the existing one-second background service loop.

| Scenario | Jobs | Submitters | Admission throughput | p50 | p95 | p99 | E2E throughput |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 | 1 | 405.4 jobs/s | 2.09 ms | 4.08 ms | 7.26 ms | n/a |
| Admission, concurrent | 1,000 | 32 | 408.4 jobs/s | 70.73 ms | 111.23 ms | 196.30 ms | n/a |
| Fake executor | 256 | 32 | 734.3 jobs/s | 40.90 ms | 55.48 ms | 55.81 ms | 20.7 jobs/s |
| Process executor | 128 | 16 | 429.1 jobs/s | 34.09 ms | 46.95 ms | 47.47 ms | 12.4 jobs/s |

## Canonical admission comparison

The clean baseline for the concurrent admission scenario is commit `16abc2a`,
which used the same 1,000 jobs, 32 submitters, and container limits.

| Metric | Synchronous scheduling | Background scheduling | Change |
| --- | ---: | ---: | ---: |
| Admission throughput | 262.5 jobs/s | 408.4 jobs/s | +55.6% |
| p50 submission latency | 119.04 ms | 70.73 ms | -40.6% |
| p95 submission latency | 166.01 ms | 111.23 ms | -33.0% |
| p99 submission latency | 186.24 ms | 196.30 ms | +5.4% |

## Interpretation

- Removing the full scheduling pass from every submission substantially improves
  admission throughput and typical latency.
- Concurrent admission still provides almost no throughput advantage over the
  serial case: 408.4 versus 405.4 jobs/s. The global service mutex and SQLite
  write serialization remain likely bottlenecks.
- The p99 regression shows that a small number of requests still wait much
  longer than the median under contention.
- Fake and process end-to-end throughput are slightly below the earlier 6 GiB
  smoke runs. Jobs now wait for the one-second scheduler tick, so this first
  optimization trades scheduling responsiveness for a faster submit path.
- The next experiment should add a capacity-one scheduler wake-up channel.
  Submissions can signal it without blocking, and many simultaneous submissions
  can collapse into one scheduling pass. That should preserve the admission gain
  while reducing the new scheduling delay.

Run every scenario multiple times and compare medians before treating the exact
percentages as publication-grade. This single run establishes that the direction
and magnitude of the admission improvement are large enough to continue.
