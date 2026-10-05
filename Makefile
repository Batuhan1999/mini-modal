.PHONY: objectstore-up objectstore-stop benchmark-limits benchmark-admission-serial benchmark-admission-concurrent benchmark-fake benchmark-process benchmark-hello-process benchmark-postgres-stop

BENCHMARK_COMPOSE := docker compose -f compose.benchmark.yml
OBJECTSTORE_COMPOSE := docker compose -f compose.objectstore.yml

objectstore-up:
	$(OBJECTSTORE_COMPOSE) up -d --wait minio
	$(OBJECTSTORE_COMPOSE) run --rm minio-init

objectstore-stop:
	$(OBJECTSTORE_COMPOSE) stop minio

benchmark-limits: objectstore-up
	$(BENCHMARK_COMPOSE) up -d --wait postgres
	$(BENCHMARK_COMPOSE) exec -T postgres sh -c 'printf "Postgres CPU: "; cat /sys/fs/cgroup/cpu.max; printf "Postgres memory: "; cat /sys/fs/cgroup/memory.max'
	$(BENCHMARK_COMPOSE) run --build --rm benchmark sh -c 'printf "Application CPU: "; cat /sys/fs/cgroup/cpu.max; printf "Application memory: "; cat /sys/fs/cgroup/memory.max; printf "Application PIDs: "; cat /sys/fs/cgroup/pids.max'
	$(BENCHMARK_COMPOSE) run --build --rm loadgen sh -c 'printf "Load generator CPU: "; cat /sys/fs/cgroup/cpu.max; printf "Load generator memory: "; cat /sys/fs/cgroup/memory.max'

benchmark-postgres-stop:
	$(BENCHMARK_COMPOSE) stop postgres

benchmark-admission-serial:
	./scripts/run-isolated-benchmark.sh admission-serial

benchmark-admission-concurrent:
	./scripts/run-isolated-benchmark.sh admission-concurrent

benchmark-fake:
	./scripts/run-isolated-benchmark.sh fake-e2e

benchmark-process:
	./scripts/run-isolated-benchmark.sh process-e2e

benchmark-hello-process:
	./scripts/run-isolated-benchmark.sh hello-process-e2e
