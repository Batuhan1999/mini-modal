# Assignment lease doubled to 30 seconds

Run on 2026-09-27 against commit `22e992f485cd866a019f113d50632729d456bdda` plus the uncommitted connection-pool experiment and a single application-code change: `assignmentLeaseDuration` from 15 to 30 seconds. The previous 15-second results are in `012-db-pool-size-2-and-4.md`. The default DB pool remains one connection. No other service or agent behavior changed for the three-run comparison.

The existing process-executor benchmark ran three times each at pool sizes 1, 2, and 4, interleaved by round. Every run used a fresh file-backed database and the same 4-CPU, 6-GiB, 1,024-PID container. The workload remained 128 jobs, 16 submitters, four real agent processes, one-second process jobs, and no intentionally failing jobs. There were zero submission errors and zero OOM kills. The mean p99 is the arithmetic mean of per-run p99 values, not a pooled percentile.

| Pool | Mean admission jobs/s | Mean submission p99 | Mean successful jobs/s | Mean end-to-end elapsed | Failed / 384 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 529.4 | 109.7 ms | 13.83 | 9.3 s | 0 |
| 2 | 349.7 | 130.7 ms | 3.27 | 38.4 s | 8 |
| 4 | 308.1 | 159.0 ms | 3.10 | 38.8 s | 23 |

All failures had `last_error = 'assignment lease expired'`. Doubling the lease did **not** clear them: the multi-connection runs now wait approximately 30 seconds before terminalizing those jobs, making end-to-end throughput worse. Mean whole-process Go pool wait was 5.53 s, 4.93 s, and 3.19 s for sizes 1, 2, and 4. Reducing pool wait did not translate into greater successful throughput.

Two focused pool-4 diagnostic runs followed. The first recorded failed-job event counts: every failed job was assigned, claimed, and had two log chunks. In the second run, the saved SQLite database confirms one failed job wrote both `started` and `finished` output. The agent logs show the actual failure: `Complete` encountered `database is locked (5) (SQLITE_BUSY)`, but the gRPC server returned `FailedPrecondition`. The agent treats any `FailedPrecondition` as a stale assignment and permanently abandons the completion report. The control plane later expires those jobs. In that diagnostic run, all five failed jobs followed this pattern. The supporting files are labeled `lease30-pool4-logs` in this directory.

This is not evidence that the process executor or agent claim rate is the bottleneck. It demonstrates a transient database error being misclassified as a permanent assignment error. SQLite still serializes writes across connections; concurrent `Submit` requests each write both a job and a history event in one transaction. This explains why four connections do not provide four-way write throughput. In the earlier concurrent-admission benchmark, mean Go pool wait was 66.88 s at pool 2 and 67.70 s at pool 4, while mean admission throughput was 449.0 vs 396.1 jobs/s. The data does not identify the exact contribution of SQLite write-lock waiting versus scheduler reads or host variance, but it shows that the fourth connection did not remove the limiting contention.

Next learning step: distinguish a genuinely stale assignment from transient SQLite errors at the gRPC boundary, then rerun the pool comparison with the original 15-second lease. Do not interpret a longer lease as a fix for the lost completion report.
