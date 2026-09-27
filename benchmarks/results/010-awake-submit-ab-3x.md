# Submit changes: awake interleaved A/B rerun

Run on 2026-09-27 after the laptop-sleep interruption in `009-interleaved-submit-ab-3x.md`. This run used `caffeinate` and completed continuously from 11:59 to 12:03 UTC. The earlier raw results are retained, but the figures below supersede them for comparison.

**Old** was clean commit `c13c6551f04fe7ed075271f1a95d812ed5f445b0`; **new** was the uncommitted current checkout. The latter removes the `Service.Submit` mutex, returns the row from the creation transaction without an extra read, and adds idempotent duplicate-submission handling. This compares the **bundle**, not the mutex change alone. The key new-source SHA-256 values remained unchanged across both A/B runs: `service.go` `111c7212cad8b8b155650d783e18b4687a45c48ae424b6cbf933490a94a06229`, `submission.go` `4805a29502c8b658537534a4e95adff3721964ba6e8f660b739607957c15c0c2`, and `jobs.go` `259e1de3d7cf2949c61a481e28e2e3beec16d86f8dce9ce3dbeedf9420588118`.

Each of four scenarios ran three times per version, in the interleaved order old, new, new, old, old, new. All 24 runs used the same benchmark image and script, a fresh container and SQLite database, and verified limits of 4 CPUs, 6 GiB memory, and 1,024 PIDs. The serial/concurrent admission scenarios used 1,000 jobs and 1/32 submitters; the fake/process end-to-end scenarios used 256/128 jobs, 32/16 submitters, and 8/4 agents.

| Scenario | Mean admission jobs/s, old → new | Change | Mean submission p99, old → new | Change |
| --- | ---: | ---: | ---: | ---: |
| Serial admission | 358.0 → 351.5 | −1.8% | 7.3 → 7.4 ms | +2.4% |
| Concurrent admission | 454.0 → 468.8 | +3.2% | 119.9 → 314.7 ms | +162.6% |
| Fake executor, end to end | 436.7 → 514.8 | +17.9% | 112.8 → 216.9 ms | +92.3% |
| Process executor, end to end | 414.4 → 540.9 | +30.5% | 68.2 → 91.4 ms | +34.0% |

End-to-end **completed-job throughput** barely changed: fake executor 22.08 → 22.25 jobs/s (+0.8%); process executor 13.74 → 13.85 jobs/s (+0.8%). All 768 fake-executor and 384 process-executor jobs succeeded **on each version**, with zero submission errors, lease-expiration failures, or OOM kills. Thus the 88 fake-executor failures in the interrupted prior run did **not** reproduce.

Figures are arithmetic means of three per-run measurements. The p99 values are means of per-run percentiles, not pooled percentiles. Serial admission ranges overlap (old 327.8–379.7, new 329.2–383.7 jobs/s), as do concurrent admission ranges (old 418.7–490.2, new 451.2–488.2 jobs/s); those differences are too small to claim a clear improvement from three runs. The active-agent scenarios show larger admission gains, but not corresponding gains in completed-job capacity, and submission tail latency is worse. Other host containers were running during the test, so host contention remains a possible source of noise despite interleaving and fixed benchmark-container limits.

The 24 raw JSON and Prometheus snapshots in this directory use labels `ab-awake-old-r1` through `ab-awake-old-r3` and `ab-awake-new-r1` through `ab-awake-new-r3`. The new source was uncommitted when measured and was subsequently committed as `543445e`; the source hashes above tie the run to that snapshot.
