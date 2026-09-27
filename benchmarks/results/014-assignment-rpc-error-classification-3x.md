# Assignment RPC error classification: SQLite baseline after fix

Run on 2026-09-27 against commit `22e992f485cd866a019f113d50632729d456bdda` plus the uncommitted pool-size experiment and assignment RPC error-classification change. The lease remained 30 seconds, as in `013-lease-30s-process-e2e-3x.md`. The gRPC server now returns `FailedPrecondition` only for an explicit stale-assignment error; other completion/failure/cancellation errors return `Unavailable`, which causes the agent to retry instead of discarding a completion.

The same process-executor workload ran three times at each pool size, interleaved by round: 128 jobs, 16 submitters, four real agent processes, one-second process jobs, no intentional failures, and fresh file-backed SQLite databases in the same 4-CPU, 6-GiB, 1,024-PID container. All runs had zero submission errors and zero OOM kills.

| Pool | Mean admission jobs/s | Mean submission p99 | Mean successful jobs/s | Mean end-to-end elapsed | Succeeded / 384 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 582.6 | 83.8 ms | 13.87 | 9.2 s | 384 |
| 2 | 376.8 | 103.1 ms | 12.82 | 10.0 s | 384 |
| 4 | 315.8 | 304.3 ms | 13.60 | 9.4 s | 384 |

Before the classification fix, the same 30-second-lease suite had 0, 8, and 23 failed jobs for pool sizes 1, 2, and 4. The prior diagnostic logs showed `SQLITE_BUSY` during `Complete` mislabeled as `FailedPrecondition`; the agent therefore abandoned the report and the lease later expired. After this fix all three pool sizes completed every job. This demonstrates a correctness repair, not a throughput improvement from a larger pool. The admission-only workload was not rerun because it never calls assignment completion; its earlier numbers remain in `012-db-pool-size-2-and-4.md`.

The nine JSON summaries and Prometheus snapshots are labeled `rpcfix-pool1-r1`–`r3`, `rpcfix-pool2-r1`–`r3`, and `rpcfix-pool4-r1`–`r3`.
