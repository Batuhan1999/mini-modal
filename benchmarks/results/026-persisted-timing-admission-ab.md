# Persisted timing vs in-memory timing: admission A/B

Run on 2026-09-27. The old variant was an isolated checkout of commit
`343f967854131468d907b42335e1f0de874967ed` with the scheduler's redundant
Go sort removed, matching the current working tree's scheduler behavior. The new
variant was the current uncommitted working tree, including the three persisted
job-timing columns and removal of the per-job Prometheus timestamp maps. Neither
variant was committed for this comparison.

Each run submitted 100,000 jobs through 32 concurrent clients at unlimited target
rate. There were no agents, so this measures admission under a growing pending
backlog, not execution throughput. PostgreSQL pool size was 20; the application
container was limited to 4 CPU / 6 GiB and the separate PostgreSQL 15 container
to 4 CPU / 4 GiB. The benchmark database was recreated before every run. Run
order was new ×3, old ×3, new ×3. All nine runs submitted and stored all 100,000
jobs with zero errors or OOM kills.

| Variant/block | Submission rates (jobs/s) | Mean jobs/s | Mean p99 | Mean app memory peak | Mean app CPU | Mean PostgreSQL CPU | Mean database size |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| New, first block | 11,540 / 11,129 / 11,424 | 11,364 | 9.78 ms | 295 MiB | 23.19 s | 31.70 s | 98.0 MiB |
| Old control | 12,804 / 11,736 / 12,916 | 12,486 | 10.37 ms | 310 MiB | 21.51 s | 29.58 s | 95.4 MiB |
| New, second block | 12,614 / 11,978 / 11,765 | 12,119 | 9.77 ms | 263 MiB | 21.89 s | 30.21 s | 97.6 MiB |
| New, all six | 11,540 / 11,129 / 11,424 / 12,614 / 11,978 / 11,765 | 11,742 | 9.77 ms | 279 MiB | 22.54 s | 30.96 s | 97.8 MiB |

The new variant did **not** improve admission throughput in this test. Its six-run
mean was about 6.0% below the three-run old control. The two new blocks differ
substantially: the first was 9.0% below old, while the second was only 2.9% below
old. Individual runs overlap, so the size of a regression is uncertain under
host-load/run-order variation. The new variant used about 31 MiB less mean peak
application-container memory and about 2.3 MiB more database storage per 100,000
jobs. Application-container memory includes the simulator, so it does not isolate
the control-plane heap. Mean PostgreSQL CPU was about 1.4 s higher; the extra
persisted fields are a plausible cost, not proven causation from this A/B alone.

This change trades some admission-path work and database space for bounded
in-process timing state and timing data that survives control-plane restarts. These
measurements do not cover claim, completion, retry, or end-to-end execution paths.
Raw JSON, Prometheus, application-resource, and PostgreSQL-resource files for each
run are named `timing-old-r{1,2,3}-100k` and `timing-db-r{1..6}-100k` in this
directory. Means are arithmetic means of run-level values; p99s are not pooled
request percentiles.
