# Claim polling: 250 ms → 50 ms

This compares two three-run snapshots from 3 October 2026. The only Go
runtime change between them was the local agent's claim interval in
`cmd/agent/main.go`. Both used the same Docker harness: application 4 CPUs /
6 GiB, separate PostgreSQL 15 container 4 CPUs / 4 GiB, pool size 20,
benchmark database recreated per run. Each end-to-end run started real agent
processes but used the fake or host-process executor, not Docker execution.

| Scenario | Workload | 250 ms | 50 ms | Observed change |
| --- | --- | ---: | ---: | ---: |
| Fake executor | 256 jobs, 8 agents | 23.18 completed/s | 28.30 completed/s | +22.1% |
| Process executor | 128 jobs, 4 agents | 14.18 completed/s | 25.43 completed/s | +79.4% |
| Serial admission | 10,000 jobs, no agents | 1,869 accepted/s | 2,032 accepted/s | +8.7% |
| Concurrent admission | 10,000 jobs, 32 clients, no agents | 11,384 accepted/s | 12,956 accepted/s | +13.8% |

Rates are arithmetic means of the three per-run rates. All 1,152 jobs in
each snapshot's end-to-end scenarios succeeded, and both admission snapshots
had zero submission errors. The admission scenarios started **no agents**;
their apparent improvement cannot be caused by the claim interval. Runs were
sequential rather than alternated and shared a Mac with other Docker projects,
so host load and warmed caches remain confounders. Every JSON has
`git_dirty: true`; rerun from clean commits for a publication-grade comparison.

The queue-wait histogram gives a more direct signal:

| Scenario | Mean queue wait, 250 ms | Mean queue wait, 50 ms |
| --- | ---: | ---: |
| Fake executor | 4.46 s | 3.29 s |
| Process executor | 4.09 s | 1.80 s |

Attempt duration stayed about two seconds for fake execution and one second
for process execution. Process-run PostgreSQL connection waits remained zero
in both snapshots. Average application peak memory rose from about 215 to
270 MiB in process runs, consistent with more overlapping work; both were far
below the 6-GiB limit. No OOM kills were recorded.

These results support the shorter interval for this small fixed fleet, not
for arbitrary scale. An idle agent now tries up to 20 claims/s rather than
four; 1,000 idle agents would generate up to 20,000 claim calls/s before
network or processing delays. The next scaling experiment should measure
idle-claim request rate and PostgreSQL load at larger node counts, then
consider idle backoff, long polling, or server-driven wakeups. Lowering the
heartbeat, renewal, scheduler fallback, capacity, provisioner, dashboard, or
simulator timers blindly would add traffic or full-table scans without
addressing this measured claim bottleneck. The Python SDK's per-job result
poll was separately reduced from one second to 250 ms for interactive waits;
its fleet-wide read load has not been benchmarked.

The benchmark simulator still checks the whole job list once per second, so
reported terminal throughput includes up to roughly one second of observer
delay. It used the same interval in both snapshots. For exact completion-time
measurements, use server timestamps rather than increasing whole-list polling.

Raw JSON, Prometheus, and resource samples are in
[`benchmarks/results/`](../benchmarks/results/), with labels
`portfolio-final-v2` and `claim-50ms`. The [baseline report](benchmark-2026-10-03.md)
has the complete scenario definitions and earlier bottleneck hypotheses.
