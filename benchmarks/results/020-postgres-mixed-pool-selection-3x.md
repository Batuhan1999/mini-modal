# PostgreSQL pool selection under mixed control-plane load

Run on 2026-09-27 against base commit `22e992f485cd866a019f113d50632729d456bdda` plus the then-uncommitted PostgreSQL migration and benchmark-harness changes. The three-run raw JSON therefore records `git_dirty=true`. The application/database changes were subsequently committed as `7fd19ea`, and the benchmark harness as `39aef11`. The benchmark used a 4-CPU / 6-GiB application container and a separate, reusable PostgreSQL 15 container limited to 4 CPUs / 4 GiB. The benchmark database was dropped and recreated before each run. The simulator, control plane, and 64 real agent processes ran inside the application container; the agents used the fake executor. There were no submission errors, failed jobs, or OOM kills in these runs.

## 20k-job unpaced burst with 32 submitters and 64 agents

| DB pool | Runs | Mean admissions/s | Mean submission p99 | Mean submission time | Mean terminal jobs/s |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 1 (calibration) | 6,731 | 19.93 ms | 2.972 s | 214.9 |
| 16 | 3 | 13,047 | 7.29 ms | 1.534 s | 218.3 |
| 20 | 3 | **14,133** | **5.77 ms** | **1.417 s** | 218.5 |

Rates and percentiles are arithmetic means of per-run values, not pooled request percentiles. The pool-16 admission rates were 13,274, 12,509, and 13,358 jobs/s; pool 20 achieved 13,718, 13,779, and 14,901 jobs/s. Pool 20 was 8.3% faster than pool 16 on this mixed admission workload, with a 20.8% lower mean submission p99. The pool-4 mixed run is a single calibration, not a three-run estimate.

The final Prometheus snapshots show about 20.9 seconds of cumulative SQL connection wait per pool-16 run versus 13.1 seconds per pool-20 run; pool 4's single run had 83.6 seconds. These are summed waits across many goroutines over the *entire* roughly 92-second run, not wall-clock seconds or Submit-only timings. Scheduler runs averaged about 1,373 at pool 16 and 1,361 at pool 20, consuming about 51.5 and 51.3 seconds of cumulative scheduler-call time respectively. The recurring scheduler scans and DB operations remain a separate optimization target; adding connections does not remove that work.

The fake executor sleeps about two seconds per assignment and the agents advertise virtual CPU/memory capacity. Hence terminal throughput near 218 jobs/s is a capacity/executor artifact, not a control-plane throughput ceiling. Long submission-to-assignment waits in this saturated test mostly represent jobs waiting for agent slots. For one pool-20 run, persisted job-event timestamps gave a 180-ms median and 1.50-s p99 from **assignment to claim**; submission-to-assignment p50 was 43.5 seconds because the workload was deliberately backlogged. The principal pool-selection metrics here are submission throughput and latency while scheduling, claims, completions, and heartbeats are active.

## Cross-check against the larger admission sweep

The separate [three-run 100k admission sweep](019-postgres-dedicated-container-rerun-3x.md) had no agents: pool 4 averaged 5,820 submissions/s at 24.89-ms p99; pool 16 averaged 12,279/s at 8.41-ms p99; pool 20 averaged 12,902/s at 9.00-ms p99. Pool 20 used an average of 3.71 of Postgres's four cores in those runs, compared with 3.57 at pool 16. The 10k sweep found only a 1.4% throughput gain from pool 20 to 32, with worse p99 and Postgres nearly at its CPU cap. Pool 32 was not tested at 100k or under this mixed workload.

For this fixed benchmark machine, **20 is the selected default**: it has the best three-run mixed-load admission result and the best 100k admission rate among the tested 100k pools. It is a measured starting point, not a universal optimum. It can be changed with `-db-max-open-conns` or `DB_MAX_OPEN_CONNS` in the benchmark. Under a different DB CPU budget, query mix, agent count, or network latency, rerun the pool sweep.

Two confirmation runs used the exact committed source `39aef11af2cb7f6bd32b8177dfcb2c0051fccf4b` with `git_dirty=false`. At pool 20, 100k concurrent admission completed at 12,624 submissions/s, 10.08-ms p99, and zero errors. A 20k mixed fake-executor run with 64 agents admitted 13,384/s at 6.50-ms p99; all 20k succeeded. These are single-run provenance checks and are not included in the three-run averages above. Their labels are `committed-pool20-100k` and `committed-mixed-pool20`.

## Why the improvement is plausible, and what it does not prove

The earlier SQLite setup had a one-connection application pool. Its three-run concurrent-admission baseline averaged 393 jobs/s and accumulated about 82.6 seconds of connection-pool wait for 1,000 submissions. Raising SQLite to two or four connections reduced some Go-pool waiting but did not materially improve admission and exposed lease-expiry failures in process runs. That is evidence that the original configuration was heavily serialized, not a proof that SQLite's engine alone caused every slowdown. Each job submission still creates a transaction with both a job row and an event row; Postgres can service more of those transactions concurrently when the application pool permits it. The dedicated Postgres container also has its **own four-CPU budget**, whereas the old SQLite benchmark shared the application's CPU allocation. Consequently, the old SQLite rate and the new Postgres rate are not a controlled engine-only A/B test; the gain includes database choice, pool size, and resource isolation.

The raw mixed files are labeled `mixed-burst-pool16-r1`–`r3` and `mixed-burst-pool20-r1`–`r3`, with JSON, Prometheus, and separate application/Postgres cgroup traces for each run. The single pool-4 calibration is `mixed-burst-pool4-r1`.
