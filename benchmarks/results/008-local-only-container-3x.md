# Local-only benchmark container: three-run checkpoint

Run on 2026-09-27. Source base commit: `c13c6551f04fe7ed075271f1a95d812ed5f445b0`. The submit/idempotency changes and the move from a long-lived devcontainer to a short-lived benchmark container were uncommitted; every result records `git_dirty: true`.

Each scenario ran three times in a fresh container with a temporary SQLite database and control plane. The container limits were verified as 4 CPUs (`cpu.max: 400000 100000`), 6 GiB memory, and 1,024 PIDs. Local development was outside Docker; no long-lived development container was running.

| Scenario | Jobs / submitters / agents | Mean admission jobs/s (range) | Mean submission p95 | Mean submission p99 | Mean terminal jobs/s |
| --- | --- | ---: | ---: | ---: | ---: |
| Admission, serial | 1,000 / 1 / 0 | 372.4 (330.7–408.8) | 5.3 ms | 7.8 ms | — |
| Admission, concurrent | 1,000 / 32 / 0 | 501.1 (491.2–511.6) | 179.3 ms | 270.7 ms | — |
| Fake executor, end to end | 256 / 32 / 8 | 491.2 (421.8–579.3) | 184.2 ms | 260.4 ms | 22.2 |
| Process executor, end to end | 128 / 16 / 4 | 536.3 (491.7–570.9) | 71.4 ms | 105.8 ms | 13.8 |

Figures are arithmetic means of three per-run values. The p99 column is **not** a pooled percentile across all requests. All 12 runs had zero submission errors and zero OOM kills. All 768 fake-executor and 384 process-executor jobs succeeded. Maximum observed container memory peak was about 188 MB, well below the 6 GiB cap.

Raw JSON and Prometheus snapshots in this directory use labels `local-only-dirty-r1`, `local-only-dirty-r2`, and `local-only-dirty-r3`. The simulator generated distinct job IDs, so the duplicate-ID submission path was not benchmarked. This checkpoint is not a causal comparison with the earlier live-devcontainer run in `007-idempotent-submit-live-dev-3x.md`: both source and host background workload differed, and the source is not yet identified by a commit alone.
