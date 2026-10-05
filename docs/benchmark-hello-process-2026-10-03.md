# Hello-world process benchmark, 3 October 2026

This is the first end-to-end benchmark of a real, short Python function in
mini-modal. A single version is uploaded before timing; each invocation stores
an input, is scheduled, is claimed by an agent process, downloads the artifact,
executes in a host process, and reports a result. There is no artificial sleep
and no fake executor. This is **not** a Docker-container or microVM startup
benchmark.

## Setup

The application, agents, and simulator share a container limited to 4 CPUs and
6 GiB. PostgreSQL 15 has a separate 4-CPU, 4-GiB container. MinIO is separate.
The benchmark database is recreated before each run. Agent claims are polled
every 50 ms. `NODE_CPUS=64` and `NODE_MEMORY_GB=128` advertise logical
placement capacity per agent; they do **not** grant physical resources. The
function prints and returns `Hello, <job_id>!`. The 2,048- and 10,000-job
results below are three separate runs per configuration, not one pooled run.

All measurements used commit `f0e8e12dce9768eb197ef3bebad9d04fb43e5634`
with uncommitted changes (`git_dirty: true` in the raw JSON). These are
diagnostic snapshots, not clean-commit version comparisons. Docker shares the
Mac host, so host activity is another uncontrolled variable.

## Short bursts: more agents, then diminishing returns

| Agents | Jobs per run | Completed/s, mean | Job p99, mean | App CPU, mean / 4 cores | App peak memory, max / 6 GiB | PG CPU, mean / 4 cores |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 8 | 2,048 | 156.9 | 12.77 s | 2.50 | 272 MiB | 0.42 |
| 16 | 2,048 | 265.7 | 7.51 s | 3.58 | 288 MiB | 0.54 |
| 20 | 2,048 | 232.8 | 8.55 s | 3.59 | 303 MiB | 0.57 |
| 24 | 2,048 | 256.7 | 7.79 s | 3.65 | 329 MiB | 0.54 |
| 32 | 2,048 | 248.3 | 8.12 s | 3.48 | 403 MiB | 0.50 |

All 30,720 jobs in this sweep succeeded. The eight-agent result is close to
the nominal claim ceiling of `8 × (1 / 0.05 s) = 160` starts/s. Sixteen agents
were fastest *in this setup*. Above that, additional claim loops did not
consistently increase completions and consumed more process/memory capacity.
The non-monotonic 20/24/32-agent results are not enough to establish a general
optimum; CPU throttling, contention, and shared-host variation all matter.

At 16 agents and **10,000 jobs**, the three runs averaged **234.6 completed/s**
(range 227.9–240.5). All 30,000 jobs succeeded. Mean app CPU was **3.69 of 4
cores**; PostgreSQL used **0.75 of 4 cores**. Peak observed application memory
was **315 MiB / 6 GiB** and PostgreSQL memory **314 MiB / 4 GiB**. The mean p99
job duration was **41.55 s**. Almost all of that tail is queueing behind the
burst, not a long-running hello-world function. The resource traces record
100-ms CPU, memory, I/O, and throttling samples rather than relying on a single
`docker stats` snapshot.

## The 100,000-job stress run

One bounded run submitted **100,000 jobs in 9.77 s** with zero submission
errors. The benchmark then waited up to 15 minutes using a database status
count every two seconds, avoiding the simulator's expensive full HTTP job-list
observer. It timed out with **46,548 succeeded**, **52,444 pending**, **989
assigned**, **19 running**, and **no terminal failures**. The partial completion
rate over the 910.5-s span from first submission to last success was **51.1/s**.
This is *not* a completed 100,000-job throughput measurement: the remaining
jobs are censored by the benchmark deadline, and the 19 running processes were
stopped during container cleanup.

| Resource during 100k stress window | Application container | PostgreSQL container |
| --- | ---: | ---: |
| Average CPU | 1.16 / 4 cores (29%) | 1.01 / 4 cores (25%) |
| Peak sampled memory | 415 MiB / 6 GiB (6.8%) | 794 MiB / 4 GiB (19.4%) |
| Resource samples | 8,734 | 8,578 |

The cgroup traces span about 901 seconds. Neither container was near its CPU
or memory *quota on average*. A brief per-core or I/O bottleneck is still
possible; average CPU alone does not disprove it. The contrast with the 10k
run is the important signal: a larger queue coincided with **lower** aggregate
CPU use and **much lower** completion rate, not a memory ceiling.

A Prometheus snapshot during the run recorded 2,335 scheduler passes taking
631.7 s in total, about 0.27 s per pass. The same snapshot counted 640
assignment-lease expirations. After shutdown, `EXPLAIN (ANALYZE, BUFFERS)` of
the pending-job query over the remaining 52,444 rows took **236 ms**; PostgreSQL
used an external merge sort with about **4.1 MiB** of temporary disk. This
post-run query plan is diagnostic, not a measurement of every live pass. The
code holds `Service.mu` across that read, placement, and per-assignment writes;
claims and completions need the same mutex. This makes lock serialization and
unbounded candidate selection strong hypotheses, not yet isolated causes.

The [partial JSON](../benchmarks/results/20261003T171638Z-resource-16-100k-hello-process-e2e.partial.json),
[application cgroup trace](../benchmarks/results/20261003T171638Z-resource-16-100k-hello-process-e2e-resources.partial.csv),
[PostgreSQL cgroup trace](../benchmarks/results/20261003T171638Z-resource-16-100k-hello-process-e2e-postgres-resources.partial.csv),
and [Prometheus snapshot](../benchmarks/results/20261003T171638Z-resource-16-100k-hello-process-e2e.partial.prom)
preserve the aborted run separately from successful full-run JSON records.

## Bounded scheduling follow-up

I changed candidate selection to a 10,000-row, priority-ordered keyset batch,
added a pending-job priority index, and advanced the cursor across passes. The
cursor wraps after the last batch. This avoids repeatedly selecting only an
unplaceable head of queue. It still holds `Service.mu` while selecting and
assigning each batch; the change does not remove that critical section.

Two single runs used the same 4-CPU/6-GiB application and 4-CPU/4-GiB
PostgreSQL limits, sixteen agents, 64 advertised CPUs and 128 GiB per node,
32 submitter workers, and the hello-world host-process executor:

| Jobs | Outcome | Full-run completions/s | Job p99 | App CPU avg / 4 | PG CPU avg / 4 | Scheduler mean/pass | Assignment lease expirations |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 20,000 | 20,000 succeeded | 258.7 | 75.26 s | 3.77 | 0.75 | 22.0 ms | 981 |
| 100,000 | 100,000 succeeded | 198.6 | 492.25 s | 3.04 | 0.92 | 32.9 ms | 11,307 |

The 100k run accepted all jobs in **8.61 s** with zero submission errors and
finished them in **503.63 s**. Peak sampled application memory was **471 MiB**
and PostgreSQL memory **619 MiB**. The scheduler took **170.5 s across 5,182
passes**. Compare that with the earlier partial run's snapshot of **631.7 s
across 2,335 passes** (about 270 ms/pass). This is a large improvement in
observed queue progress under the same configured limits, not a controlled
single-variable A/B: the index and batching changed together, both runs used
dirty working trees, and other host work may have differed. The first run
never completed, so its 51.1/s partial rate is not a full-run throughput
baseline.

The longer run still produced substantial assignment churn. Its 11,307
expirations are a count over a *completed* 100k run and should not be directly
compared with 640 in the earlier *partial* run. Job p99 of about 8.2 minutes
also shows that the queue is not yet fast, even though it drains. The next
measurements should isolate service-mutex wait, claim-query cost, and why
assignments expire before being claimed.

The new [20k JSON](../benchmarks/results/20261003T175208Z-batched-20k-hello-process-e2e.json),
[100k JSON](../benchmarks/results/20261003T175359Z-batched-100k-hello-process-e2e.json),
[100k Prometheus snapshot](../benchmarks/results/20261003T175359Z-batched-100k-hello-process-e2e.prom),
[application resource trace](../benchmarks/results/20261003T175359Z-batched-100k-hello-process-e2e-resources.csv),
and [PostgreSQL resource trace](../benchmarks/results/20261003T175359Z-batched-100k-hello-process-e2e-postgres-resources.csv)
preserve the follow-up.

## Throughput ceiling on the benchmark box

I kept the batched scheduler, four-CPU application container, separate
four-CPU PostgreSQL container, and real hello-world process executor fixed.
Each agent still polls for one claim every 50 ms. For 20,000-job burst runs,
the three-run averages were:

| Agents | Completed/s mean (range) | Job p99 mean | App CPU mean / 4 | Expired assignment leases mean |
| ---: | ---: | ---: | ---: | ---: |
| 16 | 247.4 (241.7–253.3) | 78.7 s | 3.63 | 994 |
| 20 | 242.2 (241.0–243.4) | 80.4 s | 3.63 | 1,231 |
| 24 | 244.5 (237.1–248.3) | 79.6 s | 3.67 | 1,556 |

Single screening runs at 12, 32, and 48 agents reached 229, 229, and 216
completions/s, respectively. Those are not three-run averages. On this
workload, sixteen agents gave the best measured mean of the repeated settings
and fewer lease expirations; the small difference between sixteen and
twenty-four does not establish an exact optimum.

A burst-drain rate is not necessarily a sustainable incoming rate. I then
paced submissions with sixteen agents. The simulator ran inside the same
four-CPU application container, so its own CPU cost is included:

| Jobs | Requested input/s | Delivered input/s | Full-run completions/s | Drain after submissions | Job p99 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 20,000 | 225 | 223.1 | 215.4 | 3.2 s | 49.6 s |
| 20,000 | 250 | 240.4 | 230.3 | 3.6 s | 53.3 s |
| 20,000 | 275 | 258.1 | 244.1 | 4.4 s | 58.0 s |
| 20,000 | 300 | 278.9 | 243.1 | 10.6 s | 71.3 s |
| 100,000 | 225 | 218.2 | 214.9 | 7.0 s | 61.7 s |
| 100,000 | 250 | 239.7 | 225.1 | 27.0 s | 141.9 s |

The 100k run delivering 218/s kept its *pending* count near zero at sampled
checkpoints: 4 at about 35k submitted, 8 at 53k, 35 at 69k, and 6 at 87k.
The higher-rate run rose from 29 pending near 43k submitted to 309 near 65k
and 2,206 near 84k. That is direct evidence that 240 delivered submissions/s
outpaced this configuration over the longer window. The exact sustainable
threshold between 218 and 240/s is **not measured**.

This is still not a clean platform-only CPU limit. At the two long paced
rates, the application container averaged 3.60 and 3.66 of four cores;
PostgreSQL averaged 0.78 of four. The application cgroup reported 143 and
172 seconds of CPU throttling. The load generator shared that quota with the
control plane and agents, and other host activity was uncontrolled. Lease
expirations remained high at 5,814 and 6,667. Moving the generator outside
the app quota, measuring per-process CPU and mutex wait, and reducing
assignment churn are needed before claiming a production capacity figure.

The [three-run burst samples](../benchmarks/results/README.md),
[long lower-rate result](../benchmarks/results/20261003T214722Z-compute-paced-100k-r225-hello-process-e2e.json),
and [long higher-rate result](../benchmarks/results/20261003T213747Z-compute-paced-100k-r250-hello-process-e2e.json)
retain the detailed timings and resource traces.

## Load generator outside the four-core application box

I moved only the simulator into a separate 2-CPU, 2-GiB container. The control
plane and sixteen real agent processes still share one 4-CPU, 6-GiB container;
PostgreSQL still has its separate 4-CPU, 4-GiB limit. Both new runs submitted
100,000 hello-world process jobs against a freshly recreated benchmark database.
Each rate has **one run**, so these are capacity probes, not an averaged ceiling.

| Requested/s | Delivered/s | Completed/s over full run | Drain after submission | Job p99 | App CPU mean / 4 | Generator CPU mean / 2 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 250 | 249.9 | 246.0 | 6.4 s | 61.4 s | 3.63 | 0.11 |
| 275 | 274.9 | 242.2 | 49.1 s | 268.0 s | 3.64 | 0.11 |

All 200,000 submissions were accepted and all jobs eventually succeeded.
Near 92,000 submissions in the 275/s run, about 10,300 jobs were `pending` or
`assigned`, versus about 3,600 near 50,000. The queue was growing. At 250/s,
the full run finished only 6.4 seconds after submission stopped. Thus 250/s
was handled over this particular 100k window, while 275/s exceeded the
observed processing rate. This does not locate an exact sustainable maximum.

The generator had no CPU throttling; the application used about 91% of its
four-core quota on average and recorded 145 and 129 seconds of cgroup CPU
throttling. PostgreSQL averaged 0.76 and 0.80 cores. App peak memory was 404
and 426 MiB, well below 6 GiB. Assignment leases expired 5,306 and 7,742
times, respectively, so lease churn remains a possible contributor to CPU
load and tail latency. These observations do not isolate the cost of claims,
scheduling, or completion writes. Docker's quotas are not dedicated physical
resources: all containers still share the Mac. The code was also dirty in
both result records, and host load may have varied between runs.

The [250/s result](../benchmarks/results/20261003T224559Z-isolated-loadgen-100k-r250-hello-process-e2e.json)
and [275/s result](../benchmarks/results/20261003T225327Z-isolated-loadgen-100k-r275-hello-process-e2e.json)
contain the raw timings, separate application/load-generator/PostgreSQL resource
traces, and Prometheus snapshots. They should not be averaged with the earlier
shared-generator runs above.

## Interpretation and next experiment

The small burst reveals the 50-ms claim cadence. At 10,000 jobs the application
approaches its four-core CPU quota, while memory remains far below its limit.
In the first, pre-batching large-queue run, a different path dominated the
observed window: the scheduler read all eligible pending rows, computed
placement, and held the service mutex while doing so. That same mutex guards
claims and completions.
The first 100,000-job run did not drain within its bounded timeout and must
stay separate from the successful follow-up above.

The bounded candidate set and priority index improved the observed result.
The next controlled comparison should isolate lock wait, claims/s, and lease
expirations before attributing all remaining latency to one cause. Changing
the advertised node resources alone does not increase physical execution
capacity. The isolated-generator follow-up above removes that measurement
confounder, but repeated runs and component-level CPU/lock measurements are
still needed before claiming a precise platform limit.

Raw JSON, Prometheus snapshots, and separate resource traces are
indexed in [`benchmarks/results/`](../benchmarks/results/README.md). The
[benchmark methodology](benchmarking.md) includes the reproduction command
and explains which containers are and are not measured.
