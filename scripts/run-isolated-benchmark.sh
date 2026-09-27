#!/bin/sh

set -eu

scenario="${1:?scenario is required}"
case "${scenario}" in
    admission-serial|admission-concurrent|fake-e2e|process-e2e) ;;
    *) echo "unknown benchmark scenario: ${scenario}" >&2; exit 2 ;;
esac

benchmark_timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
BENCHMARK_TIMESTAMP="${benchmark_timestamp}"
export BENCHMARK_TIMESTAMP
benchmark_label="${BENCHMARK_LABEL:-benchmark}"
case "${benchmark_label}" in
    *[!a-zA-Z0-9_-]*|'') echo "benchmark label must contain only letters, digits, underscores, and hyphens" >&2; exit 2 ;;
esac
result_base="benchmarks/results/${benchmark_timestamp}-${benchmark_label}-${scenario}"
lock_dir="benchmarks/results/.benchmark-run-lock"
if ! mkdir "${lock_dir}" 2>/dev/null; then
    echo "another benchmark is already using the shared Postgres container" >&2
    exit 1
fi
monitor_dir="$(mktemp -d benchmarks/results/.pg-monitor.XXXXXX)"
chmod 755 "${monitor_dir}"
monitor_pid=""

compose() {
    docker compose -f compose.benchmark.yml "$@"
}

stop_monitor() {
    if [ -n "${monitor_pid}" ]; then
        : >"${monitor_dir}/stop"
        wait "${monitor_pid}" || true
        monitor_pid=""
    fi
}

cleanup() {
    stop_monitor
    rm -f "${monitor_dir}/stop" "${monitor_dir}/resources.csv" "${monitor_dir}/result.json"
    rmdir "${monitor_dir}"
    rmdir "${lock_dir}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compose up -d --wait postgres
compose build --quiet benchmark

# This compose project has its own benchmark-only database. Never target the
# local-development compose project or any database supplied by the caller.
compose exec -T postgres psql -X -U benchmark -d postgres -v ON_ERROR_STOP=1 \
    -c 'DROP DATABASE IF EXISTS mini_modal_benchmark WITH (FORCE)'
compose exec -T postgres psql -X -U benchmark -d postgres -v ON_ERROR_STOP=1 \
    -c 'CREATE DATABASE mini_modal_benchmark'
postgres_oom_start="$(compose exec -T postgres awk '$1 == "oom_kill" { print $2 }' /sys/fs/cgroup/memory.events | tr -d '\r')"

compose exec -T postgres sh /benchmark-scripts/monitor-benchmark-postgres.sh \
    "/benchmark-results/$(basename "${monitor_dir}")/stop" \
    >"${monitor_dir}/resources.csv" &
monitor_pid=$!

compose run --rm benchmark \
    timeout 300s ./scripts/run-benchmark.sh "${scenario}"

stop_monitor
cp "${monitor_dir}/resources.csv" "${result_base}-postgres-resources.csv"

start_ms="$(jq -r '.workload_usage.started_unix_ms' "${result_base}.json")"
end_ms="$(jq -r '.workload_usage.ended_unix_ms' "${result_base}.json")"
postgres_usage="$(awk -F, -v start="${start_ms}" -v end="${end_ms}" '
    NR == 1 { next }
    $1 <= start { for (i = 2; i <= 8; i++) base[i] = $i }
    $1 >= start && $1 <= end {
        count++
        if ($4 > peak_memory) peak_memory = $4
    }
    $1 >= end {
        for (i = 2; i <= 8; i++) finish[i] = $i
        found = 1
        exit
    }
    END {
        if (!found || !base[2]) exit 1
        printf "%.0f %.0f %.0f %.0f %.0f %.0f %.0f %.0f\n", \
            finish[2]-base[2], finish[3]-base[3], peak_memory, \
            finish[5]-base[5], finish[6]-base[6], \
            finish[7]-base[7], finish[8]-base[8], count
    }
' "${result_base}-postgres-resources.csv")"
set -- ${postgres_usage}

postgres_cpu_limit="$(compose exec -T postgres cat /sys/fs/cgroup/cpu.max | tr -d '\r')"
postgres_memory_limit="$(compose exec -T postgres cat /sys/fs/cgroup/memory.max | tr -d '\r')"
postgres_oom_end="$(compose exec -T postgres awk '$1 == "oom_kill" { print $2 }' /sys/fs/cgroup/memory.events | tr -d '\r')"
postgres_oom_kills="$((postgres_oom_end - postgres_oom_start))"

jq \
    --arg cpu_limit "${postgres_cpu_limit}" \
    --arg memory_limit "${postgres_memory_limit}" \
    --argjson cpu_usage_usec "$1" \
    --argjson cpu_throttled_usec "$2" \
    --argjson memory_peak_bytes "$3" \
    --argjson io_read_bytes "$4" \
    --argjson io_write_bytes "$5" \
    --argjson cpu_pressure_some_usec "$6" \
    --argjson io_pressure_some_usec "$7" \
    --argjson sample_count "$8" \
    --argjson oom_kills "${postgres_oom_kills}" \
    '. + {postgres_usage: {
        cpu_limit: $cpu_limit,
        memory_limit_bytes: $memory_limit,
        cpu_usage_usec: $cpu_usage_usec,
        cpu_throttled_usec: $cpu_throttled_usec,
        memory_peak_bytes: $memory_peak_bytes,
        io_read_bytes: $io_read_bytes,
        io_write_bytes: $io_write_bytes,
        cpu_pressure_some_usec: $cpu_pressure_some_usec,
        io_pressure_some_usec: $io_pressure_some_usec,
        sample_count: $sample_count,
        oom_kills: $oom_kills
    }}' "${result_base}.json" >"${monitor_dir}/result.json"
mv "${monitor_dir}/result.json" "${result_base}.json"

echo "Saved ${result_base}-postgres-resources.csv"
echo "Postgres: $1 CPU us, $3 peak bytes, $8 workload samples"
