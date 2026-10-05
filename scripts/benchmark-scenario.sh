#!/bin/sh

# Source this file after setting scenario. Both containers must use the same workload.
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
    hello-process-e2e)
        jobs="${JOBS:-512}"
        concurrency="${CONCURRENCY:-32}"
        submit_only=false
        agent_count="${AGENTS:-8}"
        executor=process
        ;;
    *)
        echo "unknown benchmark scenario: ${scenario}" >&2
        exit 2
        ;;
esac

workload=sleep
if [ "${scenario}" = hello-process-e2e ]; then
    workload=hello
fi
if [ "${BENCHMARK_DB_OBSERVER:-false}" = true ]; then
    if [ "${scenario}" != hello-process-e2e ]; then
        echo "database observer is only supported for hello-process-e2e" >&2
        exit 2
    fi
    submit_only=true
fi
