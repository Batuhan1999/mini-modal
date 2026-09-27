# PostgreSQL benchmark rerun with separate application and database limits

Run on 2026-09-27 against commit `22e992f485cd866a019f113d50632729d456bdda` plus uncommitted PostgreSQL migration and benchmark-harness changes (`git_dirty=true`). The Docker VM exposed 12 CPUs and 12.53 GB of memory. Every run used a fresh application container capped at 4 CPUs / 6 GiB and the **same dedicated PostgreSQL 15 container** capped at 4 CPUs / 4 GiB. Before every run, the harness dropped and recreated only `mini_modal_benchmark`; it did not restart Postgres or touch the local-development database. Thus application and database have separate cgroup CPU/memory/I/O readings, but still share the Docker VM and host. A recreated database is a clean logical dataset, not a cold Postgres page cache or WAL state.

The 57 formal runs submitted 2,617,152 jobs with zero submission errors, zero OOM kills, and expected stored-job counts in every run. All end-to-end jobs reached the expected successful terminal state. Each variant ran three times. The 10k and 100k pool sweeps rotated pool order across rounds. Percentiles and rates below are arithmetic means of per-run values, not pooled request percentiles. Admission-only tests still ran the control-plane scheduler in the background; they are not isolated SQL `INSERT` microbenchmarks.

## 100k concurrent submissions, 32 clients, no agents

| DB pool | Jobs/s | p99 | App CPU cores / 4 | PG CPU cores / 4 | PG CPU throttling per run |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 4 | 5,820 | 24.89 ms | 1.76 | 1.81 | 0.02 s |
| 5 | 6,803 | 20.62 ms | 1.95 | 2.02 | 0 s |
| 6 | 7,650 | 17.83 ms | 2.11 | 2.21 | 0 s |
| 8 | 8,736 | 14.91 ms | 2.31 | 2.62 | 0.03 s |
| 9 | 9,358 | 13.47 ms | 2.41 | 2.79 | 0.02 s |
| 10 | 9,309 | 13.84 ms | 2.43 | 2.95 | 0.18 s |
| 16 | 12,279 | **8.41 ms** | 2.65 | 3.57 | 0.34 s |
| 20 | **12,902** | 9.00 ms | 2.68 | 3.71 | 0.74 s |

Pool 20's three rates were 13,322, 12,596, and 12,789 jobs/s; pool 16's were 12,662, 11,738, and 12,437. Pool 4 was stable at 5,755–5,853 jobs/s. At pool 20, PostgreSQL's cgroup averaged about 3.7 of its four available cores, while the application container—including the control plane and simulator—used about 2.7 of four. Neither ran out of memory: mean sampled peaks were about 523 MB for Postgres and 330 MB for the application at pool 20. The Postgres CPU quota began throttling modestly at larger pools. Its mean I/O pressure (`some`) was about 0.34 s during the roughly 7.8-s pool-20 runs, versus about 0.88 s during the 17.2-s pool-4 runs; these are cumulative stall durations, not percentages. Read/write byte deltas may miss post-window flushes.

Mean cumulative SQL-pool connection wait fell from 482.7 s at pool 4 to 264.9 s at 8, 233.9 s at 9, 108.2 s at 16, and 68.7 s at 20. These sums include time waited by many concurrent goroutines; they are **not** wall-clock seconds. The fall in pool wait and rising Postgres CPU strongly suggest that a small connection pool throttled admission first, while database-side CPU capacity becomes a more plausible limit at larger pools. They do not, by themselves, isolate PostgreSQL query execution from WAL, locking, network, or the scheduler's own database reads. The application container did not hit its four-core cap in these runs. A separate simulator container or process-level trace would be needed to separate control-plane CPU from client CPU.

## Shorter runs and end-to-end checks

At 10k concurrent submissions, the pool sweep averaged:

| DB pool | Jobs/s | p99 | PG CPU cores / 4 |
| ---: | ---: | ---: | ---: |
| 4 | 5,896 | 23.46 ms | 1.41 |
| 10 | 9,813 | 11.21 ms | 2.58 |
| 16 | 12,390 | 7.47 ms | 3.35 |
| 20 | 13,679 | **6.82 ms** | 3.66 |
| 32 | **13,872** | 8.76 ms | 3.94 |

Pool 32 added only about 1.4% throughput over 20 in the 10k test, with worse p99 and Postgres very near its CPU cap. Pool 32 was **not** rerun at 100k, so this does not establish a 100k optimum. The separate 10k baseline runs at pool 4 averaged 2,025 serial submissions/s (2.11-ms p99) and 6,016 concurrent submissions/s (22.95-ms p99). The three standard 1k/256/128-job scenario rounds averaged 2,247 serial submissions/s, 6,463 concurrent submissions/s, 23.15 completed fake-executor jobs/s, and 14.16 completed process-executor jobs/s.

The old shared-container results are not directly comparable: PostgreSQL, the application, and the simulator previously competed inside **one** four-CPU cgroup. Those 168 old result/report files were moved from the project to macOS Trash at `/Users/batuhancakir/.Trash/mini-modal-shared-postgres-20260927.X7H0kj`; earlier SQLite benchmark files were left alone. The new results use labels `split-base-r{1,2,3}`, `split-10k-r{1,2,3}`, `split-pool{4,10,16,20,32}-10k-r{1,2,3}`, and `split-pool{4,5,6,8,9,10,16,20}-100k-r{1,2,3}`. Each has JSON, Prometheus, and separate application/Postgres resource CSVs in this directory. The configured application pool default was still 4 during this sweep; it was subsequently changed to 20 after the [mixed-load comparison](020-postgres-mixed-pool-selection-3x.md).
