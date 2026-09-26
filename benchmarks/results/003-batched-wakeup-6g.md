# Experiment: batch scheduler wake-ups

Code commit: `af50cae823a0f9b2302585758904f1ab566b964c`

Environment: 4 CPU cores, 6 GiB memory, 1,024 PID limit, Go 1.27.1,
Linux/arm64. Every run used a fresh SQLite database and seed `1`. All four
results recorded `git_dirty: false`, zero submission errors, and zero OOM kills.

The change adds a capacity-one wake-up channel to the control-plane service.
Successful submissions signal it without blocking. The scheduler waits for a
5 ms batching window, drains a coalesced signal, and performs one scheduling
pass. The one-second ticker remains as a fallback and maintenance trigger.

| Scenario | Jobs | Submitters | Admission throughput | p50 | p95 | p99 | E2E throughput | Scheduler runs |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 | 1 | 416.5 jobs/s | 1.99 ms | 4.83 ms | 6.56 ms | n/a | 245 |
| Admission, concurrent | 1,000 | 32 | 426.9 jobs/s | 72.90 ms | 93.67 ms | 122.91 ms | n/a | 32 |
| Fake executor | 256 | 32 | 369.3 jobs/s | 73.48 ms | 132.39 ms | 136.16 ms | 21.9 jobs/s | 286 |
| Process executor | 128 | 16 | 370.9 jobs/s | 36.91 ms | 82.52 ms | 82.92 ms | 13.7 jobs/s | 150 |

## Comparison with the background scheduler

The previous experiment at commit `22580da` used only the one-second scheduler
tick. Both experiments used the same workloads and container limits.

| Scenario | Background scheduler | Batched wake-up | Change |
| --- | ---: | ---: | ---: |
| Admission, serial | 405.4 jobs/s | 416.5 jobs/s | +2.7% |
| Admission, concurrent | 408.4 jobs/s | 426.9 jobs/s | +4.5% |
| Fake executor E2E | 20.7 jobs/s | 21.9 jobs/s | +5.6% |
| Process executor E2E | 12.4 jobs/s | 13.7 jobs/s | +10.2% |

Concurrent admission p95 fell from 111.23 ms to 93.67 ms, and p99 fell from
196.30 ms to 122.91 ms. The wake-up path therefore improved both throughput and
tail latency in this run.

## Scheduler work

| Scenario | Background scheduler | Batched wake-up |
| --- | ---: | ---: |
| Admission, serial | 2 runs | 245 runs |
| Admission, concurrent | 2 runs | 32 runs |
| Fake executor | 278 runs | 286 runs |
| Process executor | 144 runs | 150 runs |

The 5 ms window coalesces concurrent submissions effectively: 1,000 requests
caused only 32 scheduler passes. Serial submissions cannot coalesce as strongly,
so they caused 245 passes. End-to-end scheduler counts remain high because job
completion and failure paths still invoke scheduling synchronously.

## Interpretation

- The wake-up channel removes most of the one-second scheduling delay without
  giving up admission throughput.
- The strongest result is lower concurrent tail latency together with higher
  throughput.
- The fake and process E2E improvements indicate that agents receive work more
  promptly.
- Admission throughput inside the E2E scenarios is lower than in the admission-
  only scenarios because scheduling, agents, heartbeats, and execution contend
  for the same fixed resources. E2E throughput is the meaningful measure there.
- The next focused experiment can remove synchronous scheduling from completion
  and failure paths and route those state changes through the same coalesced
  wake-up mechanism.

Run each scenario multiple times and compare medians before treating the exact
percentages as publication-grade. This run preserves the complete raw JSON and
Prometheus snapshots for later analysis.
