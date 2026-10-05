#!/bin/sh

set -eu

scenario="${1:-}"
if [ -z "${scenario}" ]; then
    echo "usage: $0 admission-serial|admission-concurrent|fake-e2e|process-e2e|hello-process-e2e" >&2
    exit 2
fi

. ./scripts/benchmark-scenario.sh

case "${agent_count}" in
    ''|*[!0-9]*) echo "agent count must be a non-negative integer" >&2; exit 2 ;;
esac
node_cpus="${NODE_CPUS:-8}"
node_memory_gb="${NODE_MEMORY_GB:-16}"
for value in "${node_cpus}" "${node_memory_gb}"; do
    case "${value}" in
        ''|*[!0-9]*|0) echo "node CPU and memory values must be positive integers" >&2; exit 2 ;;
    esac
done

commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
git_dirty=false
untracked_source_files="$(
    git ls-files --others --exclude-standard 2>/dev/null |
        grep -Ev '^benchmarks/results/' || true
)"
if ! git diff --quiet 2>/dev/null ||
    ! git diff --cached --quiet 2>/dev/null ||
    [ -n "${untracked_source_files}" ]; then
    git_dirty=true
fi

run_dir="$(mktemp -d)"
bin_dir="${run_dir}/bin"
mkdir -p "${bin_dir}" benchmarks/results
control_plane_pid=""
agent_pids=""
memory_monitor_pid=""
memory_monitor_stop="${run_dir}/memory-monitor-stop"
memory_monitor_result="${run_dir}/memory-monitor-peak"
resource_samples="${run_dir}/resource-samples.csv"

cleanup() {
    if [ -n "${memory_monitor_pid}" ]; then
        : >"${memory_monitor_stop}"
        wait "${memory_monitor_pid}" 2>/dev/null || true
    fi
    for pid in ${agent_pids}; do
        kill "${pid}" 2>/dev/null || true
    done
    if [ -n "${control_plane_pid}" ]; then
        kill "${control_plane_pid}" 2>/dev/null || true
    fi
    wait 2>/dev/null || true
    rm -rf "${run_dir}"
}
trap cleanup EXIT INT TERM

database_url="${BENCHMARK_DATABASE_URL:?benchmark database URL is required}"
if ! pg_isready -q; then
    echo "benchmark Postgres is not ready" >&2
    exit 1
fi

go build -o "${bin_dir}/controlplane" ./cmd/controlplane
go build -o "${bin_dir}/agent" ./cmd/agent

"${bin_dir}/controlplane" \
    -database-url "${database_url}" \
    -db-max-open-conns "${DB_MAX_OPEN_CONNS:-20}" \
    -auto-provision=false \
    -agent-binary "${bin_dir}/agent" \
    >"${run_dir}/controlplane.log" 2>&1 &
control_plane_pid=$!

ready=false
for _ in $(seq 1 100); do
    if curl -fsS http://localhost:8080/metrics >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 0.1
done
if [ "${ready}" != true ]; then
    tail -100 "${run_dir}/controlplane.log" >&2
    echo "control plane did not become ready" >&2
    exit 1
fi

psql -X -v ON_ERROR_STOP=1 -c "INSERT INTO node_pools (id, gpu_type, gpus_per_node, cpus_per_node, memory_gb_per_node, min_nodes, max_nodes, desired_nodes, idle_timeout_seconds) VALUES ('benchmark', '', 0, ${node_cpus}, ${node_memory_gb}, 0, 100000, ${agent_count}, 0)"

index=1
while [ "${index}" -le "${agent_count}" ]; do
    node_id="benchmark-node-$(printf '%02d' "${index}")"
    "${bin_dir}/agent" \
        -node-id "${node_id}" \
        -pool-id benchmark \
        -executor "${executor}" \
        -cpus "${node_cpus}" \
        -memory-gb "${node_memory_gb}" \
        >"${run_dir}/${node_id}.log" 2>&1 &
    agent_pids="${agent_pids} $!"
    index=$((index + 1))
done

if [ "${agent_count}" -gt 0 ]; then
    sleep 2
fi

timestamp="${BENCHMARK_TIMESTAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
benchmark_label="${BENCHMARK_LABEL:-sync-scheduler}"
benchmark_rate="${BENCHMARK_RATE:-0}"
result_base="benchmarks/results/${timestamp}-${benchmark_label}-${scenario}"
summary_path="${result_base}-simulator.json"
start_file="${result_base}.started"
submitted_file="${result_base}.submitted"

observer="http_job_list"
if [ "${BENCHMARK_DB_OBSERVER:-false}" = true ]; then
    observer="postgres_status_counts"
fi
observer_timed_out=false

io_totals() {
    awk '
        {
            for (i = 2; i <= NF; i++) {
                split($i, counter, "=")
                totals[counter[1]] += counter[2]
            }
        }
        END {
            printf "%.0f %.0f %.0f %.0f\n", totals["rbytes"], totals["wbytes"], totals["rios"], totals["wios"]
        }
    ' /sys/fs/cgroup/io.stat
}

pressure_total() {
    awk -v level="$2" '
        $1 == level {
            for (i = 2; i <= NF; i++) {
                split($i, field, "=")
                if (field[1] == "total") {
                    print field[2]
                    exit
                }
            }
        }
    ' "$1"
}

echo "Running ${scenario}: jobs=${jobs} concurrency=${concurrency} agents=${agent_count} node_cpus=${node_cpus} node_memory_gb=${node_memory_gb} executor=${executor}"
printf '%s\n' ready >"${result_base}.ready"
while [ ! -f "${start_file}" ]; do
    sleep 0.1
done
workload_cpu_start="$(awk '$1 == "usage_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
workload_throttled_start="$(awk '$1 == "throttled_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
workload_cpu_pressure_start="$(pressure_total /sys/fs/cgroup/cpu.pressure some)"
workload_memory_start="$(cat /sys/fs/cgroup/memory.current)"
set -- $(io_totals)
workload_io_read_start="$1"
workload_io_write_start="$2"
workload_io_read_ops_start="$3"
workload_io_write_ops_start="$4"
workload_io_pressure_some_start="$(pressure_total /sys/fs/cgroup/io.pressure some)"
workload_io_pressure_full_start="$(pressure_total /sys/fs/cgroup/io.pressure full)"
(
    peak="${workload_memory_start}"
    printf 'unix_ms,cpu_usage_usec,memory_bytes,io_read_bytes,io_write_bytes\n' >"${resource_samples}"
    sample_resources() {
        sampled_at="$(date +%s%3N)"
        sampled_cpu="$(awk '$1 == "usage_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
        sampled_memory="$(cat /sys/fs/cgroup/memory.current)"
        set -- $(io_totals)
        if [ "${sampled_memory}" -gt "${peak}" ]; then
            peak="${sampled_memory}"
        fi
        printf '%s,%s,%s,%s,%s\n' "${sampled_at}" "${sampled_cpu}" "${sampled_memory}" "$1" "$2" >>"${resource_samples}"
    }
    sample_resources
    while [ ! -e "${memory_monitor_stop}" ]; do
        sleep 0.1
        sample_resources
    done
    printf '%s\n' "${peak}" >"${memory_monitor_result}"
) &
memory_monitor_pid=$!
workload_started_unix_ms="$(date +%s%3N)"
while [ ! -f "${submitted_file}" ]; do
    sleep 0.1
done
if [ ! -s "${summary_path}" ]; then
    echo "load generator did not produce a summary" >&2
    exit 1
fi
if [ "${observer}" = postgres_status_counts ]; then
    deadline="$(( $(date +%s) + ${BENCHMARK_OBSERVER_TIMEOUT_SECONDS:-900} ))"
    checks=0
    while :; do
        set -- $(psql -XAtq -F ' ' -c "
SELECT COUNT(*) FILTER (WHERE status = 'succeeded'),
       COUNT(*) FILTER (WHERE status IN ('failed', 'cancelled', 'unschedulable'))
FROM jobs;")
        succeeded="$1"
        other_terminal="$2"
        if [ "$((succeeded + other_terminal))" -eq "${jobs}" ]; then
            break
        fi
        if [ "$(date +%s)" -ge "${deadline}" ]; then
            echo "timed out waiting for jobs: ${succeeded} succeeded, ${other_terminal} other terminal, ${jobs} submitted" >&2
            observer_timed_out=true
            break
        fi
        checks="$((checks + 1))"
        if [ "$((checks % 5))" -eq 0 ]; then
            echo "Completed ${succeeded}/${jobs} hello-world jobs"
        fi
        sleep 2
    done
    status_counts="$(psql -XAtq -c 'SELECT json_object_agg(status, total) FROM (SELECT status, COUNT(*) AS total FROM jobs GROUP BY status) AS counts;')"
    if [ "${observer_timed_out}" = true ]; then
        observed_span_ms="$(psql -XAtq -c 'SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT) - MIN(created_at) FROM jobs;')"
    else
        observed_span_ms="$(psql -XAtq -c 'SELECT MAX(updated_at) - MIN(created_at) FROM jobs;')"
    fi
    jq --argjson statuses "${status_counts}" --argjson observed_span_ms "${observed_span_ms}" \
        '.statuses = $statuses | .elapsed_ns = ($observed_span_ms * 1000000)' \
        "${summary_path}" >"${run_dir}/observed-summary.json"
    mv "${run_dir}/observed-summary.json" "${summary_path}"
    if [ "${observer_timed_out}" = false ]; then
        echo "All ${succeeded} jobs succeeded; database terminal span: ${observed_span_ms} ms"
    fi
fi
if [ "${workload}" = hello ] && [ "${observer_timed_out}" = false ]; then
    jq -e --argjson jobs "${jobs}" '
        .submitted == $jobs and .submission_errors == 0 and .statuses.succeeded == $jobs
    ' "${summary_path}" >/dev/null || {
        echo "hello-world benchmark did not complete every submitted job successfully" >&2
        exit 1
    }
fi
workload_ended_unix_ms="$(date +%s%3N)"
workload_cpu_end="$(awk '$1 == "usage_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
workload_throttled_end="$(awk '$1 == "throttled_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
workload_cpu_pressure_end="$(pressure_total /sys/fs/cgroup/cpu.pressure some)"
workload_memory_end="$(cat /sys/fs/cgroup/memory.current)"
set -- $(io_totals)
workload_io_read_end="$1"
workload_io_write_end="$2"
workload_io_read_ops_end="$3"
workload_io_write_ops_end="$4"
workload_io_pressure_some_end="$(pressure_total /sys/fs/cgroup/io.pressure some)"
workload_io_pressure_full_end="$(pressure_total /sys/fs/cgroup/io.pressure full)"
: >"${memory_monitor_stop}"
wait "${memory_monitor_pid}"
memory_monitor_pid=""
workload_memory_peak="$(cat "${memory_monitor_result}")"
if [ "${workload_memory_end}" -gt "${workload_memory_peak}" ]; then
    workload_memory_peak="${workload_memory_end}"
fi
workload_cpu_usage_usec="$((workload_cpu_end - workload_cpu_start))"
workload_cpu_throttled_usec="$((workload_throttled_end - workload_throttled_start))"
workload_cpu_pressure_usec="$((workload_cpu_pressure_end - workload_cpu_pressure_start))"
workload_io_read_bytes="$((workload_io_read_end - workload_io_read_start))"
workload_io_write_bytes="$((workload_io_write_end - workload_io_write_start))"
workload_io_read_ops="$((workload_io_read_ops_end - workload_io_read_ops_start))"
workload_io_write_ops="$((workload_io_write_ops_end - workload_io_write_ops_start))"
workload_io_pressure_some_usec="$((workload_io_pressure_some_end - workload_io_pressure_some_start))"
workload_io_pressure_full_usec="$((workload_io_pressure_full_end - workload_io_pressure_full_start))"
set -- $(awk -F, '
    NR == 2 { previous_time = $1; previous_cpu = $2; previous_read = $4; previous_write = $5; next }
    NR > 2 {
        elapsed_ms = $1 - previous_time
        if (elapsed_ms > 0) {
            cpu_millicores = ($2 - previous_cpu) / elapsed_ms
            read_bytes_per_sec = ($4 - previous_read) * 1000 / elapsed_ms
            write_bytes_per_sec = ($5 - previous_write) * 1000 / elapsed_ms
            if (cpu_millicores > peak_cpu) peak_cpu = cpu_millicores
            if (read_bytes_per_sec > peak_read) peak_read = read_bytes_per_sec
            if (write_bytes_per_sec > peak_write) peak_write = write_bytes_per_sec
        }
        previous_time = $1; previous_cpu = $2; previous_read = $4; previous_write = $5
    }
    END { printf "%.0f %.0f %.0f %d\n", peak_cpu, peak_read, peak_write, NR - 1 }
' "${resource_samples}")
workload_sampled_peak_cpu_millicores="$1"
workload_sampled_peak_io_read_bytes_per_sec="$2"
workload_sampled_peak_io_write_bytes_per_sec="$3"
workload_sample_count="$4"

curl -fsS http://localhost:8080/metrics >"${result_base}.prom"

cpu_limit="$(cat /sys/fs/cgroup/cpu.max)"
memory_limit="$(cat /sys/fs/cgroup/memory.max)"
pids_limit="$(cat /sys/fs/cgroup/pids.max)"
memory_peak="$(cat /sys/fs/cgroup/memory.peak)"
pids_peak="$(cat /sys/fs/cgroup/pids.peak)"
cpu_usage_usec="$(awk '$1 == "usage_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
oom_kills="$(awk '$1 == "oom_kill" {print $2}' /sys/fs/cgroup/memory.events)"
database_bytes="$(psql -XAtq -c 'SELECT pg_database_size(current_database());')"
stored_jobs="$(psql -XAtq -c 'SELECT COUNT(*) FROM jobs;')"
job_timing="$(psql -XAtq -c "
SELECT json_build_object(
    'first_submitted_unix_ms', MIN(created_at),
    'last_terminal_unix_ms', MAX(updated_at),
    'terminal_span_ms', MAX(updated_at) - MIN(created_at),
    'job_duration_p50_ms', percentile_cont(0.50) WITHIN GROUP (ORDER BY updated_at - created_at),
    'job_duration_p95_ms', percentile_cont(0.95) WITHIN GROUP (ORDER BY updated_at - created_at),
    'job_duration_p99_ms', percentile_cont(0.99) WITHIN GROUP (ORDER BY updated_at - created_at)
)
FROM jobs
WHERE status IN ('succeeded', 'failed', 'cancelled', 'unschedulable');")"
failure_reasons="$(psql -XAtq -c "
SELECT COALESCE(json_agg(t), '[]'::json)
FROM (SELECT last_error AS reason, COUNT(*) AS jobs FROM jobs WHERE status = 'failed' GROUP BY last_error) t;")"
failed_job_events="$(psql -XAtq -c "
SELECT COALESCE(json_agg(t), '[]'::json)
FROM (
    SELECT j.id AS job_id,
           j.node_id,
           j.last_error,
           (SELECT COUNT(*) FROM job_events e WHERE e.job_id = j.id AND e.event = 'assigned') AS assigned_events,
           (SELECT COUNT(*) FROM job_events e WHERE e.job_id = j.id AND e.event = 'claimed') AS claimed_events,
           (SELECT COUNT(*) FROM job_logs l WHERE l.job_id = j.id) AS log_chunks
    FROM jobs j
    WHERE j.status = 'failed'
    ORDER BY j.id
    LIMIT 20
) t;")"

jq -n \
    --arg timestamp "${timestamp}" \
    --arg scenario "${scenario}" \
    --arg benchmark_label "${benchmark_label}" \
    --arg commit "${commit}" \
    --argjson git_dirty "${git_dirty}" \
    --arg cpu_limit "${cpu_limit}" \
    --arg memory_limit "${memory_limit}" \
    --arg pids_limit "${pids_limit}" \
    --arg executor "${executor}" \
    --arg workload "${workload}" \
    --arg observer "${observer}" \
    --argjson jobs "${jobs}" \
    --argjson concurrency "${concurrency}" \
    --argjson rate "${benchmark_rate}" \
    --argjson agents "${agent_count}" \
    --argjson node_cpus "${node_cpus}" \
    --argjson node_memory_gb "${node_memory_gb}" \
    --argjson db_max_open_conns "${DB_MAX_OPEN_CONNS:-20}" \
    --argjson database_bytes "${database_bytes}" \
    --argjson stored_jobs "${stored_jobs}" \
    --argjson job_timing "${job_timing}" \
    --argjson failure_reasons "${failure_reasons:-[]}" \
    --argjson failed_job_events "${failed_job_events:-[]}" \
    --argjson memory_peak "${memory_peak}" \
    --argjson pids_peak "${pids_peak}" \
    --argjson cpu_usage_usec "${cpu_usage_usec}" \
    --argjson workload_cpu_usage_usec "${workload_cpu_usage_usec}" \
    --argjson workload_cpu_throttled_usec "${workload_cpu_throttled_usec}" \
    --argjson workload_cpu_pressure_usec "${workload_cpu_pressure_usec}" \
    --argjson workload_memory_start "${workload_memory_start}" \
    --argjson workload_memory_end "${workload_memory_end}" \
    --argjson workload_memory_peak "${workload_memory_peak}" \
    --argjson workload_io_read_bytes "${workload_io_read_bytes}" \
    --argjson workload_io_write_bytes "${workload_io_write_bytes}" \
    --argjson workload_io_read_ops "${workload_io_read_ops}" \
    --argjson workload_io_write_ops "${workload_io_write_ops}" \
    --argjson workload_io_pressure_some_usec "${workload_io_pressure_some_usec}" \
    --argjson workload_io_pressure_full_usec "${workload_io_pressure_full_usec}" \
    --argjson workload_sampled_peak_cpu_millicores "${workload_sampled_peak_cpu_millicores}" \
    --argjson workload_sampled_peak_io_read_bytes_per_sec "${workload_sampled_peak_io_read_bytes_per_sec}" \
    --argjson workload_sampled_peak_io_write_bytes_per_sec "${workload_sampled_peak_io_write_bytes_per_sec}" \
    --argjson workload_sample_count "${workload_sample_count}" \
    --argjson workload_started_unix_ms "${workload_started_unix_ms}" \
    --argjson workload_ended_unix_ms "${workload_ended_unix_ms}" \
    --argjson observer_timed_out "${observer_timed_out}" \
    --argjson oom_kills "${oom_kills}" \
    --slurpfile summary "${summary_path}" \
    '{
        schema_version: 3,
        timestamp: $timestamp,
        benchmark_label: $benchmark_label,
        scenario: $scenario,
        git_commit: $commit,
        git_dirty: $git_dirty,
        outcome: (if $observer_timed_out then "observer_timeout_partial" else "complete" end),
        limits: {cpu: $cpu_limit, memory_bytes: $memory_limit, pids: $pids_limit},
        container_usage: {
            cpu_usage_usec: $cpu_usage_usec,
            memory_peak_bytes: $memory_peak,
            pids_peak: $pids_peak,
            oom_kills: $oom_kills
        },
        workload_usage: {
            started_unix_ms: $workload_started_unix_ms,
            ended_unix_ms: $workload_ended_unix_ms,
            cpu_usage_usec: $workload_cpu_usage_usec,
            cpu_average_cores: ($workload_cpu_usage_usec / (($workload_ended_unix_ms - $workload_started_unix_ms) * 1000)),
            cpu_limit_utilization_percent: (100 * $workload_cpu_usage_usec / (($workload_ended_unix_ms - $workload_started_unix_ms) * 1000) / (($cpu_limit | split(" ") | map(tonumber) | .[0]) / ($cpu_limit | split(" ") | map(tonumber) | .[1]))),
            cpu_throttled_usec: $workload_cpu_throttled_usec,
            cpu_pressure_some_usec: $workload_cpu_pressure_usec,
            memory_start_bytes: $workload_memory_start,
            memory_end_bytes: $workload_memory_end,
            memory_peak_bytes: $workload_memory_peak,
            memory_peak_percent_of_limit: (100 * $workload_memory_peak / ($memory_limit | tonumber)),
            io_read_bytes: $workload_io_read_bytes,
            io_write_bytes: $workload_io_write_bytes,
            io_read_ops: $workload_io_read_ops,
            io_write_ops: $workload_io_write_ops,
            io_pressure_some_usec: $workload_io_pressure_some_usec,
            io_pressure_full_usec: $workload_io_pressure_full_usec,
            sampled_peak_cpu_millicores: $workload_sampled_peak_cpu_millicores,
            sampled_peak_io_read_bytes_per_sec: $workload_sampled_peak_io_read_bytes_per_sec,
            sampled_peak_io_write_bytes_per_sec: $workload_sampled_peak_io_write_bytes_per_sec,
            sample_count: $workload_sample_count
        },
        workload: {jobs: $jobs, concurrency: $concurrency, rate_per_second: $rate, agents: $agents, node_cpus: $node_cpus, node_memory_gb: $node_memory_gb, executor: $executor, function: $workload, observer: $observer, db_max_open_conns: $db_max_open_conns},
        timing: $job_timing,
        storage: {database_bytes: $database_bytes, jobs: $stored_jobs, failure_reasons: $failure_reasons, failed_job_events: $failed_job_events},
        result: $summary[0]
    }' >"${result_base}.json"

echo "Saved ${result_base}.json"
echo "Saved ${result_base}.prom"
cp "${resource_samples}" "${result_base}-resources.csv"
echo "Saved ${result_base}-resources.csv"

if [ "${BENCHMARK_CAPTURE_LOGS:-false}" = true ]; then
    cp "${run_dir}/controlplane.log" "${result_base}-controlplane.log"
    for log_file in "${run_dir}"/benchmark-node-*.log; do
        if [ -f "${log_file}" ]; then
            cp "${log_file}" "${result_base}-$(basename "${log_file}")"
        fi
    done
fi
