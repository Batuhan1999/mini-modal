# Benchmark snapshot — 3 October 2026

This is a measured snapshot of the local prototype, not a production capacity
claim. Each scenario ran three times in the fixed Docker harness. The
application container had a 4-CPU / 6-GiB limit, PostgreSQL 15 had a separate
4-CPU / 4-GiB limit, and the connection pool was 20. The harness recreated
only `mini_modal_benchmark` before each run. Other Docker projects were active
on the host, so the container limits do not eliminate host-load noise.

The benchmark binary came from commit
`f0e8e12dce9768eb197ef3bebad9d04fb43e5634` **plus uncommitted changes**.
Every JSON records `git_dirty: true`. These numbers are useful for choosing
the next experiment, but should be rerun from a clean commit before being
presented as a reproducible version-to-version comparison.
The agent used a 250 ms claim interval in these runs; it was reduced to 50 ms
afterward, so the end-to-end rates below are not measurements of that change.

## Results

Rates below are arithmetic means of per-run rates, with the observed range in
parentheses. The p99 column is the mean of each run's submission p99, not a
pooled percentile. All 60,000 admission submissions returned successfully;
all 1,152 end-to-end jobs succeeded. There were no recorded OOM kills or
cgroup CPU throttled time in these runs.

| Scenario | Jobs / run | HTTP concurrency | Agents | Accepted jobs/s | Terminal jobs/s | Submission p99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Serial admission | 10,000 | 1 | 0 | 1,869 (1,791–1,952) | — | 2.02 ms (1.90–2.10) |
| Concurrent admission | 10,000 | 32 | 0 | 11,384 (10,210–12,199) | — | 9.47 ms (5.99–14.62) |
| Fake executor | 256 | 32 | 8 | — | 23.18 (23.16–23.20) | 26.48 ms (20.54–32.82) |
| Process executor | 128 | 16 | 4 | — | 14.18 (14.16–14.18) | 14.03 ms (10.61–16.22) |

The admission scenarios timed only the HTTP submission phase; they did **not**
wait for execution. The background scheduler was still active and consumed
resources during that phase. The end-to-end rates include submission, queue
wait, execution, and reporting. Fake execution takes about two seconds per
attempt; process execution takes about one second per attempt. Different agent
counts and workloads mean the two end-to-end rates are not an executor A/B
comparison.

## What the measurements suggest

1. **Agent claim cadence is the next throughput experiment.** The local agent
   polls one claim every 250 ms. Four agents can therefore begin at most about
   16 attempts/s through that loop, close to the observed 14.18 terminal
   jobs/s in the process run. Its average queue wait was 4.09 s while average
   attempt duration was 1.02 s. Eight fake agents have a nominal 32-claims/s
   ceiling and achieved 23.18 terminal jobs/s with 2.01 s attempts. This is a
   strong hypothesis, not proof of the sole bottleneck. Test a shorter poll
   interval or capacity-aware multi-claim loop with the *same* workload and
   agent count, recording claim rate and database load.
2. **Admission concurrency raises throughput but adds pool pressure.** The
   32-client runs opened all 20 database connections and recorded
   9,172–9,266 connection waits per 10,000 jobs, totalling 7.5–9.1 s of
   aggregate wait time. PostgreSQL averaged about 3.56 of its four available
   CPUs during the timed phase; the application averaged about 2.40. Pool
   waiting is real, but a larger pool could just move contention to PostgreSQL.
   Profile the submission transaction and database CPU before changing it.
3. **The scheduler is still doing work during admission.** The serial runs
   recorded 433–467 scheduler passes and 2.79–3.09 s of cumulative scheduler
   duration while accepting 10,000 jobs. The 32-client runs recorded 44–47
   passes and 0.67–0.74 s. The coalesced wakeup is not a fixed-rate batcher:
   lower arrival concurrency can allow a pass after many individual writes.
   Measure a bounded debounce/batch policy against both admission p99 and
   queue wait before adopting it.
4. **Capacity and Docker execution remain unmeasured.** The harness sets
   `-auto-provision=false` and launches a fixed number of real agent processes.
   It does not time node scale-up, drain, or Docker container startup. Those
   need separate bounded scenarios, not extrapolation from the rates above.

Concurrent admission used roughly 127 MiB application and 203 MiB PostgreSQL
peak memory on average. The end-to-end runs averaged below one CPU core in
each container over their timed phases. Memory exhaustion and CPU throttling
were not observed in this workload. This does not establish a system limit;
the scenarios are intentionally finite and use a single control plane.

## Reproduce and inspect

Run each command three times from the same source state:

```sh
JOBS=10000 BENCHMARK_LABEL=portfolio-final-v2 make benchmark-admission-serial
JOBS=10000 BENCHMARK_LABEL=portfolio-final-v2 make benchmark-admission-concurrent
BENCHMARK_LABEL=portfolio-final-v2 make benchmark-fake
BENCHMARK_LABEL=portfolio-final-v2 make benchmark-process
```

The 12 JSON summaries, Prometheus snapshots, and application/PostgreSQL
resource samples are in [`benchmarks/results/`](../benchmarks/results/), named
`20261003T*-portfolio-final-v2-*`. The JSON records the exact job count,
concurrency, agent count, limits, commit, dirty flag, and raw latencies. The
[benchmarking methodology](benchmarking.md) explains the harness and why a
Docker executor is outside its fixed resource boundary.
