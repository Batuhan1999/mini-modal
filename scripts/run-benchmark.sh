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
        grep -Ev '^benchmarks/results/[^/]+\.(json|prom)$' || true
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

cleanup() {
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

go build -o "${bin_dir}/controlplane" ./cmd/controlplane
go build -o "${bin_dir}/agent" ./cmd/agent
go build -o "${bin_dir}/simulator" ./cmd/simulator

"${bin_dir}/controlplane" \
    -db-path "${run_dir}/benchmark.db" \
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

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
benchmark_label="${BENCHMARK_LABEL:-sync-scheduler}"
result_base="benchmarks/results/${timestamp}-${benchmark_label}-${scenario}"
summary_path="${run_dir}/summary.json"

submit_flag=""
if [ "${submit_only}" = true ]; then
    submit_flag="-submit-only"
fi

echo "Running ${scenario}: jobs=${jobs} concurrency=${concurrency} agents=${agent_count} executor=${executor}"
"${bin_dir}/simulator" \
    -jobs "${jobs}" \
    -rate 0 \
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

curl -fsS http://localhost:8080/metrics >"${result_base}.prom"

cpu_limit="$(cat /sys/fs/cgroup/cpu.max)"
memory_limit="$(cat /sys/fs/cgroup/memory.max)"
pids_limit="$(cat /sys/fs/cgroup/pids.max)"
memory_peak="$(cat /sys/fs/cgroup/memory.peak)"
pids_peak="$(cat /sys/fs/cgroup/pids.peak)"
cpu_usage_usec="$(awk '$1 == "usage_usec" {print $2}' /sys/fs/cgroup/cpu.stat)"
oom_kills="$(awk '$1 == "oom_kill" {print $2}' /sys/fs/cgroup/memory.events)"
database_bytes="$(stat -c %s "${run_dir}/benchmark.db")"
stored_jobs="$(sqlite3 "${run_dir}/benchmark.db" 'SELECT COUNT(*) FROM jobs;')"

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
    --argjson agents "${agent_count}" \
    --argjson database_bytes "${database_bytes}" \
    --argjson stored_jobs "${stored_jobs}" \
    --argjson memory_peak "${memory_peak}" \
    --argjson pids_peak "${pids_peak}" \
    --argjson cpu_usage_usec "${cpu_usage_usec}" \
    --argjson oom_kills "${oom_kills}" \
    --slurpfile summary "${summary_path}" \
    '{
        schema_version: 1,
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
        workload: {jobs: $jobs, concurrency: $concurrency, agents: $agents, executor: $executor},
        storage: {database_bytes: $database_bytes, jobs: $stored_jobs},
        result: $summary[0]
    }' >"${result_base}.json"

echo "Saved ${result_base}.json"
echo "Saved ${result_base}.prom"
