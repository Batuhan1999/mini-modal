# Admission decomposition: SQLite journal mode and PostgreSQL pool size

Run on 2026-09-27. The PostgreSQL A/B used exact commit `8885b48f7053b260fb0dcbf8f508351ef855b8c5` with `git_dirty=false`. Each run submitted 10,000 jobs through 32 concurrent clients at unlimited target rate, seed 1, with no agents. The benchmark database was recreated before each run. Pool-1 and pool-20 runs were alternated across three rounds. The application container had a 4-CPU / 6-GiB limit; the dedicated PostgreSQL 15 container had its own 4-CPU / 4-GiB limit. All six runs stored 10,000 jobs, with zero submission errors and zero OOM kills.

| Configuration | Runs | Mean admissions/s | Mean submission p99 |
| --- | ---: | ---: | ---: |
| SQLite DELETE, one connection | 3 | 662 | 217.34 ms |
| SQLite WAL, one connection | 3 | 2,025 | 80.03 ms |
| PostgreSQL, one connection | 3 | 2,252 | 66.51 ms |
| PostgreSQL, 20 connections | 3 | 11,789 | 7.77 ms |

The SQLite rows come from the [matched journal-mode A/B](021-sqlite-wal-admission-ab-3x.md), run on the historical SQLite revision in a single 4-CPU / 6-GiB container. The PostgreSQL rows are a matched **pool-size** A/B on the current revision and split-container setup. All four rows use the same 10k/32-client request workload, but they are **not one controlled four-way engine benchmark**: the application versions, connection counts, container topology, and total available CPU differ across SQLite and PostgreSQL.

Within the controlled comparisons, switching SQLite from DELETE to WAL improved admission **3.06×**, while changing PostgreSQL's pool from 1 to 20 improved admission **5.24×**. SQLite WAL and one-connection PostgreSQL were close in this workload (2,025 versus 2,252/s), a descriptive difference of about 11%, not evidence of engine parity. The headline DELETE/one-connection SQLite to 20-connection PostgreSQL gap was about 17.8× in these runs, but attributing that whole factor to the database engine would be incorrect.

The PostgreSQL pool-1 rates were 2,277, 2,315, and 2,163 submissions/s; pool 20 reached 11,010, 11,768, and 12,590/s. Mean cumulative `go_sql_wait_duration_seconds_total` was about **140 seconds** at pool 1 versus **8 seconds** at pool 20. Those counters sum waits across concurrent goroutines over an entire run, not elapsed wall time or Submit-only time. At pool 1, PostgreSQL consumed about 2.14 CPU-seconds during a mean 4.45-second submission window (roughly 0.48 core); at pool 20, it used about 2.91 CPU-seconds during a mean 0.85-second window (roughly 3.4 cores). That is direct evidence that the one-connection application cap left PostgreSQL CPU capacity unused, whereas the larger pool let it process transactions concurrently. The scheduler also ran during admission and shared the same pool, so these numbers are whole-control-plane admission results, not isolated SQL INSERT measurements.

The WAL experiment isolates **journal mode** as a cause of the SQLite admission gain even though the pool stayed at one connection. It does not identify individual filesystem calls as the cause; SQLite's [WAL documentation](https://www.sqlite.org/wal.html) describes a sequential append-oriented write path and fewer sync operations than rollback journaling, which are plausible mechanisms. A syscall or block-I/O trace under the same workload would be needed to substantiate that lower-level explanation. Similarly, the remaining SQLite WAL versus PostgreSQL pool-1 difference needs matched CPU limits, pool settings, and application revision before it can be interpreted as an engine difference.

Raw PostgreSQL files are labeled `pgpool1-10k-r1`–`r3` and `pgpool20-10k-r1`–`r3`, each with JSON, Prometheus, and separate application/PostgreSQL cgroup traces. Per-run admission rates and p99 values are summarized as arithmetic means, not pooled request percentiles. The SQLite raw files are referenced in the previous report.
