# Database-pool wait measurement: three-run suite

Run on 2026-09-27 against commit `22e992f485cd866a019f113d50632729d456bdda` plus the uncommitted registration of `collectors.NewDBStatsCollector(db, "mini-modal")` in `cmd/controlplane/main.go` (SHA-256 `48f011a0d582ea5476cd77d92736d6b9994825ad1691ac60a165a90472ea0166`). The source did not change during the run. The database still uses `SetMaxOpenConns(1)`; no other configuration or benchmark workload was changed.

The four existing scenarios ran three times each, in scenario order within rounds `r1`, `r2`, and `r3`. Every run used a fresh container/database, the same benchmark image and script, and verified limits of 4 CPUs, 6 GiB memory, and 1,024 PIDs. macOS sleep inhibition was active, and the result timestamps show no unexplained gaps. All 12 runs had zero submission errors and zero OOM kills. All 768 fake-executor and 384 process-executor jobs succeeded.

| Scenario | Mean admission jobs/s (range) | Mean submission p99 | Mean DB waits | Mean cumulative DB wait | Mean time per wait |
| --- | ---: | ---: | ---: | ---: | ---: |
| Serial admission | 383.0 (303.6–485.9) | 7.3 ms | 681 | 1.15 s | 1.7 ms |
| Concurrent admission | 393.1 (332.8–497.3) | 395.3 ms | 1,028 | 82.61 s | 80.4 ms |
| Fake executor, end to end | 543.9 (522.7–556.5) | 218.7 ms | 266 | 14.05 s | 52.8 ms |
| Process executor, end to end | 477.9 (432.8–541.4) | 116.9 ms | 672 | 5.76 s | 8.6 ms |

The wait columns come from the final Prometheus snapshot's `go_sql_wait_count_total` and `go_sql_wait_duration_seconds_total` counters. They measure **all pool waits since control-plane startup**, not only `Submit`. Since each run starts a fresh process, startup is a small but unseparated part of the total. `Mean time per wait` is total wait duration divided by total wait count across the three runs. Cumulative wait time can exceed wall time because many goroutines wait simultaneously. The end-of-run `in_use_connections` gauge is not a measure of utilization throughout the run; that needs time-series scraping.

The concurrent admission runs are strong evidence of **Go connection-pool contention**: each submitted 1,000 jobs through 32 submitters in about 2–3 seconds, while accumulating 63–95 seconds of pool wait and roughly 1,000 wait events. Scheduler duration also accumulated 1.63–1.77 seconds across those runs; that wall-clock timer includes any connection wait. These measurements do **not** establish SQLite's own one-writer lock as the limiting factor, because the application has only one open connection. End-to-end terminal throughput remained about 22.30 jobs/s for fake execution and 13.80 jobs/s for process execution.

These are arithmetic means of per-run measurements; the p99 values are **not pooled percentiles**. Admission throughput varied substantially across rounds and differs from `010-awake-submit-ab-3x.md`, so do not attribute that difference to the collector without a controlled A/B. The next controlled change would be to keep this workload fixed while testing additional connections, after ensuring connection-local SQLite PRAGMAs are applied on every new connection and monitoring `SQLITE_BUSY`, throughput, p99, and pool wait.

The 12 JSON summaries and 12 Prometheus snapshots use labels `dbpool-r1`, `dbpool-r2`, and `dbpool-r3` in this directory.
