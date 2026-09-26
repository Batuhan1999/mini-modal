# Baseline: synchronous scheduling with the 6 GiB box

Environment: 4 CPU cores, 6 GiB memory, 1,024 PID limit, Go 1.27.1,
Linux/arm64. All runs used a fresh SQLite database and seed `1`.

These measurements are provisional because the working tree was dirty. Commit
the benchmark foundation before using future results as publication-grade
comparisons.

| Scenario | Jobs | Submitters | Admission throughput | p50 | p95 | p99 | E2E throughput |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 | 1 | 229.4 jobs/s | 4.13 ms | 7.37 ms | 9.02 ms | n/a |
| Admission, concurrent | 1,000 | 32 | 242.2 jobs/s | 140.99 ms | 161.13 ms | 168.05 ms | n/a |
| Fake executor | 256 | 32 | 324.4 jobs/s | 89.75 ms | 136.44 ms | 149.18 ms | 23.7 jobs/s |
| Process executor | 128 | 16 | 289.9 jobs/s | 44.90 ms | 92.16 ms | 98.13 ms | 13.5 jobs/s |

## Resource usage

| Scenario | Peak memory | Peak PIDs | OOM kills | SQLite size |
| --- | ---: | ---: | ---: | ---: |
| Admission, serial | 161.9 MiB | 21 | 0 | 616 KiB |
| Admission, concurrent | 162.7 MiB | 22 | 0 | 612 KiB |
| Fake executor | 161.4 MiB | 87 | 0 | 400 KiB |
| Process executor | 176.9 MiB | 115 | 0 | 288 KiB |

## What this baseline shows

- Raising the memory ceiling from 4 GiB to 6 GiB does not materially improve
  admission throughput. The workload peaks below 180 MiB, so memory capacity is
  not the current bottleneck.
- Thirty-two concurrent clients improve admission from 229.4 to only 242.2
  jobs/s, while p95 latency rises from 7.37 ms to 161.13 ms. Requests are still
  queuing behind serialized submission and scheduling work.
- The fake executor isolates the control-plane and agent protocol path. The
  process executor adds real process startup and log-streaming overhead, which
  lowers terminal throughput from 23.7 to 13.5 jobs/s in these differently-sized
  smoke workloads.
- No run reached the memory or PID limits, and no OOM kill occurred. The fixed
  box gives us a stable ceiling without distorting this baseline.

Repeat each scenario at least five times and compare medians before publishing
results. The next useful experiment is to decouple scheduling from the submit
request, then rerun these exact scenarios without changing their parameters.
