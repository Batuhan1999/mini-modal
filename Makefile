.PHONY: benchmark-limits benchmark-admission-serial benchmark-admission-concurrent benchmark-fake benchmark-process

BENCHMARK_COMPOSE := docker compose -f compose.benchmark.yml

benchmark-limits:
	$(BENCHMARK_COMPOSE) run --build --rm benchmark sh -c 'printf "CPU: "; cat /sys/fs/cgroup/cpu.max; printf "Memory: "; cat /sys/fs/cgroup/memory.max; printf "PIDs: "; cat /sys/fs/cgroup/pids.max'

benchmark-admission-serial:
	$(BENCHMARK_COMPOSE) run --build --rm benchmark ./scripts/run-benchmark.sh admission-serial

benchmark-admission-concurrent:
	$(BENCHMARK_COMPOSE) run --build --rm benchmark ./scripts/run-benchmark.sh admission-concurrent

benchmark-fake:
	$(BENCHMARK_COMPOSE) run --build --rm benchmark ./scripts/run-benchmark.sh fake-e2e

benchmark-process:
	$(BENCHMARK_COMPOSE) run --build --rm benchmark ./scripts/run-benchmark.sh process-e2e
