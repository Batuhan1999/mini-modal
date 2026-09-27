#!/bin/sh

set -eu

scenario="${1:-}"
if [ -z "${scenario}" ]; then
    echo "usage: $0 admission-serial|admission-concurrent|fake-e2e|process-e2e" >&2
    exit 2
fi

case "${scenario}" in
    admission-serial)
        jobs="${JOBS:-1000}"
        concurrency="${CONCURRENCY:-1}"
        submit_only=true
        agent_count=0
        executor=fake
        ;;
    admission-concurrent)
        jobs="${JOBS:-1000}"
        concurrency="${CONCURRENCY:-32}"
        submit_only=true
        agent_count=0
        executor=fake
        ;;
    fake-e2e)
        jobs="${JOBS:-256}"
        concurrency="${CONCURRENCY:-32}"
        submit_only=false
        agent_count="${AGENTS:-8}"
        executor=fake
        ;;
    process-e2e)
        jobs="${JOBS:-128}"
        concurrency="${CONCURRENCY:-16}"
        submit_only=false
        agent_count="${AGENTS:-4}"
        executor=process
        ;;
    *)
        echo "unknown benchmark scenario: ${scenario}" >&2
        exit 2
        ;;
esac

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
go build -o "${bin_dir}/simulator" ./cmd/simulator

"${bin_dir}/controlplane" \
    -database-url "${database_url}" \
    -db-max-open-conns "${DB_MAX_OPEN_CONNS:-20}" \
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

index=1
while [ "${index}" -le "${agent_count}" ]; do
    node_id="benchmark-node-$(printf '%02d' "${index}")"
    "${bin_dir}/agent" \
        -node-id "${node_id}" \
        -executor "${executor}" \
        -cpus 8 \
        -memory-gb 16 \
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
summary_path="${run_dir}/summary.json"

submit_flag=""
if [ "${submit_only}" = true ]; then
    submit_flag="-submit-only"
fi

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

echo "Running ${scenario}: jobs=${jobs} concurrency=${concurrency} agents=${agent_count} executor=${executor}"
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
"${bin_dir}/simulator" \
    -jobs "${jobs}" \
    -rate "${benchmark_rate}" \
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
    --argjson jobs "${jobs}" \
    --argjson concurrency "${concurrency}" \
    --argjson rate "${benchmark_rate}" \
    --argjson agents "${agent_count}" \
    --argjson db_max_open_conns "${DB_MAX_OPEN_CONNS:-20}" \
    --argjson database_bytes "${database_bytes}" \
    --argjson stored_jobs "${stored_jobs}" \
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
    --argjson oom_kills "${oom_kills}" \
    --slurpfile summary "${summary_path}" \
    '{
        schema_version: 3,
        timestamp: $timestamp,
        benchmark_label: $benchmark_label,
        scenario: $scenario,
        git_commit: $commit,
        git_dirty: $git_dirty,
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
            cpu_throttled_usec: $workload_cpu_throttled_usec,
            cpu_pressure_some_usec: $workload_cpu_pressure_usec,
            memory_start_bytes: $workload_memory_start,
            memory_end_bytes: $workload_memory_end,
            memory_peak_bytes: $workload_memory_peak,
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
        workload: {jobs: $jobs, concurrency: $concurrency, rate_per_second: $rate, agents: $agents, executor: $executor, db_max_open_conns: $db_max_open_conns},
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
