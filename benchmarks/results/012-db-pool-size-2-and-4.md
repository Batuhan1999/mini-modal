# SQLite connection-pool experiment: 1 vs 2 vs 4

Run on 2026-09-27 against commit `22e992f485cd866a019f113d50632729d456bdda` plus uncommitted changes. The previous three-run, one-connection baseline is in `011-db-pool-wait-3x.md`. For this experiment, SQLite connection-local `foreign_keys=ON` and `busy_timeout=5000` were moved into the driver DSN so every opened connection gets them. Pool size is now selectable via `-db-max-open-conns` / `DB_MAX_OPEN_CONNS`, but the default remains **1**. No journal-mode change was made. The application-code diff (`cmd/controlplane/main.go` and `internal/store/sqlite/database.go`) hashes to SHA-256 `270cfe1c5b9c59fd7c5ca2acbd1c48385df09ae130e35007dd00d077b592ed3c`.

Each size ran the same four existing scenarios three times in separate containers/databases, with the same 4-CPU, 6-GiB, 1,024-PID limits and sleep inhibition. Baseline was measured earlier the same day; host load may still differ, so differences in the tens of percent are suggestive, not a controlled proof of causality. No run had submission errors or OOM kills. Admission scenarios use 1,000 jobs; fake and process end-to-end use 256 and 128 jobs respectively. The p99 column is the arithmetic mean of each run's p99, not a pooled percentile.

| Scenario | Pool | Mean admission jobs/s | Mean submission p99 | Mean terminal jobs/s | Failed jobs / 3 runs |
| --- | ---: | ---: | ---: | ---: | ---: |
| Serial admission | 1 | 383.0 | 7.3 ms | — | — |
| Serial admission | 2 | 494.3 | 4.9 ms | — | — |
| Serial admission | 4 | 476.4 | 5.5 ms | — | — |
| Concurrent admission | 1 | 393.1 | 395.3 ms | — | — |
| Concurrent admission | 2 | 449.0 | 316.0 ms | — | — |
| Concurrent admission | 4 | 396.1 | 341.9 ms | — | — |
| Fake end-to-end | 1 | 543.9 | 218.7 ms | 22.3 | 0 / 768 |
| Fake end-to-end | 2 | 376.6 | 221.8 ms | 21.9 | 0 / 768 |
| Fake end-to-end | 4 | 388.1 | 272.6 ms | 21.9 | 0 / 768 |
| Process end-to-end | 1 | 477.9 | 116.9 ms | 13.8 | 0 / 384 |
| Process end-to-end | 2 | 327.7 | 167.5 ms | 8.7 | 6 / 384 |
| Process end-to-end | 4 | 286.7 | 272.5 ms | 6.0 | 20 / 384 |

In concurrent admission, mean Go pool-wait count stayed about 1,000 per run: 1,028 at pool 1, 1,030 at pool 2, 1,035 at pool 4. Mean cumulative wait fell from 82.61 s to 66.88 s and 67.70 s, respectively. Serial admission had no pool waits at sizes 2 and 4, versus 681 at size 1. These are whole-process pool counters, not isolated Submit measurements. The process end-to-end terminal rate includes failed jobs, so it overstates successful throughput at pool sizes 2 and 4.

After the suite, one additional process run at each size recorded failed-job `last_error` values. Pool 1 completed 128/128 jobs; pool 2 completed 125/128 and pool 4 completed 125/128. All six failures in the two multi-connection diagnostic runs were `assignment lease expired`. This establishes the observed failure mode, not whether the underlying delay came from SQLite write-lock contention, claim latency, renewal latency, or scheduling behavior. Additional targeted timing/logging would be needed to distinguish those causes.

Conclusion: a larger pool removes serial Go pool waiting and may modestly improve admission, but it does not solve concurrent admission and regresses process-executor reliability and throughput in this workload. Keep the default at 1 until lease timing and database contention are understood. The three-run result files are labeled `dbpool2-r1`–`r3` and `dbpool4-r1`–`r3`; diagnostic runs use `dbpool1-diagnostic`, `dbpool2-diagnostic`, and `dbpool4-diagnostic`.
