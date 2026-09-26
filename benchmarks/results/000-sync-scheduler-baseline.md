# Baseline: synchronous scheduling during admission

Environment: 4 CPU cores, 4 GiB memory, 1,024 PID limit, Go 1.27.1,
Linux/arm64. All runs used a fresh SQLite database and seed `1`.

This is the original 4-GiB profile. The default benchmark box was subsequently
raised to 6 GiB; do not compare this table directly with the new profile.

These measurements are provisional because the working tree was dirty. Commit
the benchmark foundation before using future results as publication-grade
comparisons.

| Scenario | Jobs | Submitters | Admission throughput | p50 | p95 | p99 | E2E throughput |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 | 1 | 209.7 jobs/s | 4.08 ms | 8.81 ms | 13.85 ms | n/a |
| Admission, concurrent | 1,000 | 32 | 243.4 jobs/s | 126.82 ms | 187.39 ms | 193.71 ms | n/a |
| Fake executor | 256 | 32 | 414.9 jobs/s | 71.60 ms | 92.27 ms | 96.80 ms | 22.0 jobs/s |
| Process executor | 128 | 16 | 462.7 jobs/s | 29.38 ms | 49.55 ms | 55.52 ms | 13.8 jobs/s |

## What the baseline shows

- Thirty-two concurrent clients provide little admission improvement while p95
  latency grows by more than 21x. Requests queue behind the service mutex.
- The 1,000-job admission runs invoke the scheduler approximately 1,004 times,
  confirming that submission and scheduling are coupled almost one-to-one.
- Admission is faster when agents drain work concurrently because the pending
  queue remains smaller. This implicates repeatedly loading and sorting the
  complete pending queue.
- Fake execution isolates the control-plane and agent protocol path. Process
  execution adds real process startup and log-streaming overhead, reducing
  terminal throughput from 22.0 to 13.8 jobs/s in these differently-sized
  smoke workloads.

Repeat each scenario at least five times and compare medians before publishing
results. These first measurements validate the harness and establish the
expected shape of the current bottleneck.
