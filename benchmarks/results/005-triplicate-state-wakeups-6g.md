# Three-run comparison: scheduler wake-ups after state changes

Previous code: `af50cae823a0f9b2302585758904f1ab566b964c`
Current code: `18d65f89063d69e0d6a336011256b062e4ee9d15`

Both versions ran each scenario three times with a fresh SQLite database,
seed `1`, 4 CPU cores, 6 GiB memory, and a 1,024 PID limit. All 24 runs
recorded a clean Git tree, zero submission errors, and zero OOM kills. Every
end-to-end job succeeded.

Throughput below is the arithmetic mean of the three per-run jobs/second
values. The range shows the lowest and highest individual runs. Admission
scenarios use submission throughput; end-to-end scenarios use terminal-job
throughput.

| Scenario | Previous mean (range) | Current mean (range) | Previous scheduler passes | Current scheduler passes |
| --- | ---: | ---: | ---: | ---: |
| Admission, serial | 394.7 (378.0–411.9) | 365.5 (350.0–378.1) jobs/s | 253.7 | 259.3 |
| Admission, concurrent | 502.9 (471.8–535.2) | 505.1 (456.3–532.2) jobs/s | 30.0 | 30.0 |
| Fake executor E2E | 21.9 (21.6–22.2) | 21.9 (21.9–22.0) jobs/s | 285.3 | 97.0 |
| Process executor E2E | 13.7 (13.5–13.9) | 13.8 (13.7–13.9) jobs/s | 150.3 | 69.0 |

The previous version had the same batched submission wake-up but scheduled
synchronously after completion, failure, cancellation, and registration. The
current version sends those transitions through the wake-up channel too.
Average scheduler passes fell by 66.0% with the fake executor and 54.1% with
the process executor. End-to-end throughput barely moved at these workload
sizes, suggesting that execution capacity and other fixed costs dominate.

## Serial admission difference

Serial admission averaged 394.7 jobs/s before and 365.5 jobs/s after, a 7.4%
drop. Individual runs were 411.9, 378.0, and 394.2 before and 350.0, 378.1,
and 368.4 after. The changed completion, cancellation, and registration paths
are not exercised in this no-agent scenario; `Submit` and the scheduler loop
did not change between commits.

The scheduler held the service lock for an average of about 0.412 seconds
before and 0.422 seconds after. That approximately 0.010-second difference is
too small to explain the approximately 0.20-second difference in average
submission wall time. The exact cause is not established by these runs.
Host load, SQLite write timing, and other run-to-run effects remain possible.

The versions were run in separate blocks rather than alternating order, so
time-varying host load is still a confounder. An alternating A/B run with host
CPU and disk measurements would be the next diagnostic if the serial gap
matters to a future optimization decision.

Follow-up: the [alternating serial diagnostic](006-interleaved-serial-diagnostic.md)
averaged 385.9 jobs/s before and 386.1 jobs/s after. The earlier 7.4% gap did
not reproduce when the versions were interleaved.

## Raw data

The previous version's three runs per scenario use the
`baseline-retest-af50cae` filename label. The current version uses one run
from `state-wakeup-18d65f8` plus two runs from `replicate-18d65f8`; serial
admission's two extra runs use `serial-variance-18d65f8`. Every run has a JSON
summary and a matching `.prom` metrics snapshot in this directory.
