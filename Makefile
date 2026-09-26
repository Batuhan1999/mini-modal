.PHONY: box-build box-up box-shell box-dev box-test box-generate box-limits box-down benchmark-admission-serial benchmark-admission-concurrent benchmark-fake benchmark-process

box-build:
	docker compose -f compose.dev.yml build

box-up:
	docker compose -f compose.dev.yml up -d

box-shell:
	docker compose -f compose.dev.yml exec dev bash

box-dev:
	docker compose -f compose.dev.yml exec dev go tool air

box-test:
	docker compose -f compose.dev.yml exec dev go test ./...

box-generate:
	docker compose -f compose.dev.yml exec dev go generate ./...

box-limits:
	docker compose -f compose.dev.yml exec dev sh -c 'printf "CPU: "; cat /sys/fs/cgroup/cpu.max; printf "Memory: "; cat /sys/fs/cgroup/memory.max; printf "PIDs: "; cat /sys/fs/cgroup/pids.max'

box-down:
	docker compose -f compose.dev.yml down

benchmark-admission-serial:
	docker compose -f compose.dev.yml stop dev
	docker compose -f compose.dev.yml run --rm dev ./scripts/run-benchmark.sh admission-serial

benchmark-admission-concurrent:
	docker compose -f compose.dev.yml stop dev
	docker compose -f compose.dev.yml run --rm dev ./scripts/run-benchmark.sh admission-concurrent

benchmark-fake:
	docker compose -f compose.dev.yml stop dev
	docker compose -f compose.dev.yml run --rm dev ./scripts/run-benchmark.sh fake-e2e

benchmark-process:
	docker compose -f compose.dev.yml stop dev
	docker compose -f compose.dev.yml run --rm dev ./scripts/run-benchmark.sh process-e2e
