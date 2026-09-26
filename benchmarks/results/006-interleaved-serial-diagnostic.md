# Diagnostic: interleaved serial admission

Old code: `af50cae823a0f9b2302585758904f1ab566b964c`

New code: `18d65f89063d69e0d6a336011256b062e4ee9d15`. The new runs recorded
repository commit `2896614ad697fa76da42308ea01ffc4dd96b8255`, which adds
benchmark documentation and results but has the same Go source as `18d65f8`.

The earlier three-run comparison measured serial admission at 394.7 jobs/s
before and 365.5 jobs/s after. To test whether that difference followed the
code version or the time of the run, both versions were run in the order
old, new, new, old, old, new. Each run submitted 1,000 jobs with one submitter,
no agents, a fresh SQLite database, seed `1`, and the same 4-CPU/6-GiB/1,024-PID
container limits. All six runs were clean, had zero submission errors and OOM
kills, and recorded zero CPU quota throttling during the run.

| Order | Version | Admission throughput | Scheduler passes | Scheduler time |
| ---: | --- | ---: | ---: | ---: |
| 1 | Old | 399.4 jobs/s | 247 | 0.413 s |
| 2 | New | 452.3 jobs/s | 237 | 0.346 s |
| 3 | New | 343.8 jobs/s | 275 | 0.456 s |
| 4 | Old | 376.8 jobs/s | 261 | 0.460 s |
| 5 | Old | 381.6 jobs/s | 260 | 0.441 s |
| 6 | New | 362.1 jobs/s | 262 | 0.424 s |

The arithmetic means were **385.9 jobs/s old** and **386.1 jobs/s new**.
Average scheduler passes were 256 old and 258 new. The original serial gap
did not reproduce when versions were interleaved, so these measurements do
not support a consistent regression from the state-change wake-ups.

The wide spread within the new version (343.8–452.3 jobs/s) demonstrates how
noisy this small benchmark is. Slower submission pacing coalesces fewer wake-up
signals and can cause more scheduling passes. Each pass reads every pending job,
while the service mutex and single SQLite connection serialize it with
submissions. In the new-code runs, the fastest pass count was 237 and the
slowest was 275. The additional scheduler time was about 0.11 seconds, smaller
than the roughly 0.70-second difference in submission time, so it is only a
partial explanation.

The container CPU limit is a ceiling rather than a reservation. Other work on
the Mac or in Docker can still compete for CPU or disk access at different
times. This experiment ruled out CPU quota throttling but did not measure host
CPU pressure or disk wait, so it cannot identify which external source caused
the remaining variation.

Raw JSON and Prometheus snapshots use the `serial-ab-old` and `serial-ab-new`
labels in this directory. The CPU throttling counters were checked before and
after each run and remained zero; they are recorded in the diagnostic command
output, not the JSON schema.
