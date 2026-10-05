#!/bin/sh

set -eu

scenario="${1:?scenario is required}"
. ./scripts/benchmark-scenario.sh

timestamp="${BENCHMARK_TIMESTAMP:?benchmark timestamp is required}"
label="${BENCHMARK_LABEL:-benchmark}"
result_base="benchmarks/results/${timestamp}-${label}-${scenario}"
summary_path="${result_base}-simulator.json"
stop_file="${result_base}.loadgen-stop"
samples_path="${result_base}-loadgen-resources.csv"
usage_path="${result_base}-loadgen-usage.json"

run_dir="$(mktemp -d)"
monitor_pid=""
cleanup() {
    if [ -n "${monitor_pid}" ]; then
        : >"${stop_file}"
        wait "${monitor_pid}" || true
    fi
    rm -f "${stop_file}"
    rm -rf "${run_dir}"
}
trap cleanup EXIT INT TERM

go build -o "${run_dir}/simulator" ./cmd/simulator

ready=false
for _ in $(seq 1 100); do
    if curl -fsS http://benchmark:8080/metrics >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 0.1
done
if [ "${ready}" != true ]; then
    echo "control plane is not reachable from the load generator" >&2
    exit 1
fi

submit_flag=""
if [ "${submit_only}" = true ]; then
    submit_flag="-submit-only"
fi

sh ./scripts/monitor-benchmark-postgres.sh "${stop_file}" >"${samples_path}" &
monitor_pid=$!
cpu_start="$(awk '$1 == "usage_usec" { print $2 }' /sys/fs/cgroup/cpu.stat)"
throttled_start="$(awk '$1 == "throttled_usec" { print $2 }' /sys/fs/cgroup/cpu.stat)"
started_ms="$(date +%s%3N)"
printf '%s\n' started >"${result_base}.started"

"${run_dir}/simulator" \
    -base-url http://benchmark:8080 \
    -workload "${workload}" \
    -jobs "${jobs}" \
    -rate "${BENCHMARK_RATE:-0}" \
    -concurrency "${concurrency}" \
    ${submit_flag} \
    -failure-fraction 0 \
    -timeout-fraction 0 \
    -min-runtime 1s \
    -max-runtime 1s \
    -timeout-headroom 60s \
    -max-attempts 1 \
    -wait-timeout 10m \
    -output "${summary_path}"

ended_ms="$(date +%s%3N)"
cpu_end="$(awk '$1 == "usage_usec" { print $2 }' /sys/fs/cgroup/cpu.stat)"
throttled_end="$(awk '$1 == "throttled_usec" { print $2 }' /sys/fs/cgroup/cpu.stat)"
memory_peak="$(cat /sys/fs/cgroup/memory.peak)"
cpu_limit="$(cat /sys/fs/cgroup/cpu.max)"
memory_limit="$(cat /sys/fs/cgroup/memory.max)"
oom_kills="$(awk '$1 == "oom_kill" { print $2 }' /sys/fs/cgroup/memory.events)"
: >"${stop_file}"
wait "${monitor_pid}"
monitor_pid=""
rm -f "${stop_file}"

jq -n \
    --arg cpu_limit "${cpu_limit}" \
    --arg memory_limit "${memory_limit}" \
    --argjson started_unix_ms "${started_ms}" \
    --argjson ended_unix_ms "${ended_ms}" \
    --argjson cpu_usage_usec "$((cpu_end - cpu_start))" \
    --argjson cpu_throttled_usec "$((throttled_end - throttled_start))" \
    --argjson memory_peak_bytes "${memory_peak}" \
    --argjson oom_kills "${oom_kills}" \
    '{cpu_limit: $cpu_limit, memory_limit_bytes: $memory_limit,
      started_unix_ms: $started_unix_ms, ended_unix_ms: $ended_unix_ms,
      cpu_usage_usec: $cpu_usage_usec, cpu_throttled_usec: $cpu_throttled_usec,
      memory_peak_bytes: $memory_peak_bytes, oom_kills: $oom_kills}' \
    >"${usage_path}"
printf '%s\n' submitted >"${result_base}.submitted"
