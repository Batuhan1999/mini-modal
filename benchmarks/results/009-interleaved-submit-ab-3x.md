# Submit changes: interleaved A/B benchmark

**Superseded for interpretation:** the laptop was closed during this run, introducing long gaps and making the lease-failure result unreliable. See `010-awake-submit-ab-3x.md` for the uninterrupted rerun. Raw measurements are retained for audit.

Run on 2026-09-27. Compared clean commit `c13c6551f04fe7ed075271f1a95d812ed5f445b0` (**old**) with the current uncommitted checkout (**new**). The new checkout removes the `Service.Submit` mutex, returns the row created in the transaction without another read, and adds idempotent handling for duplicate submissions. This comparison measures that bundle, **not the mutex removal in isolation**. Source files used for the new runs did not change during the benchmark.

Each of four scenarios ran three times per version, interleaved in the order old, new, new, old, old, new. Every run used a fresh container and SQLite database, the same benchmark image and script, and the same verified limits: 4 CPUs, 6 GiB memory, and 1,024 PIDs. The serial/concurrent admission scenarios submitted 1,000 jobs with 1/32 submitters; fake/process end-to-end scenarios submitted 256/128 jobs with 32/16 submitters and 8/4 agents. All 24 runs had zero submission errors and zero OOM kills. `go test ./...` passed for both versions.

| Scenario | Admission jobs/s, old → new | Change | Submission p99, old → new | Change |
| --- | ---: | ---: | ---: | ---: |
| Serial admission | 279.9 → 308.8 | +10.3% | 10.6 → 7.9 ms | −25.4% |
| Concurrent admission | 363.6 → 427.1 | +17.4% | 233.9 → 320.9 ms | +37.2% |
| Fake executor, end to end | 372.5 → 442.2 | +18.7% | 155.3 → 287.8 ms | +85.3% |
| Process executor, end to end | 495.3 → 597.5 | +20.6% | 63.1 → 86.8 ms | +37.6% |

End-to-end **terminal throughput** barely changed: fake executor 21.85 → 22.07 jobs/s (+1.0%); process executor 13.82 → 13.88 jobs/s (+0.4%). All 384 process jobs succeeded in each version. In the fake runs, old had 680 successes and 88 failures across 768 jobs, while new had 768 successes and no failures. Corresponding *successful* throughput was 19.33 → 22.07 jobs/s (+14.2%). Old fake runs 1 and 3 recorded 40 and 48 assignment lease expirations, matching their failure counts; old run 2 and all new runs recorded none. This is an observed reliability difference, not proof that the submit change alone caused it.

Figures are arithmetic means of three per-run values. The p99 values are means of per-run percentiles, **not pooled percentiles**. Higher admission throughput did not translate into materially higher terminal throughput, and concurrent submission tail latency worsened. The raw JSON and Prometheus snapshots in this directory use labels `ab-submit-old-r1` through `ab-submit-old-r3` and `ab-submit-new-r1` through `ab-submit-new-r3`. The new source was uncommitted when measured and was subsequently committed as `543445e`.
