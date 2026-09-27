#!/bin/sh

set -eu

stop_file="${1:?stop file is required}"

stat_value() {
    awk -v key="$2" '$1 == key { print $2; exit }' "$1"
}

pressure_total() {
    awk '$1 == "some" { for (i = 2; i <= NF; i++) { split($i, field, "="); if (field[1] == "total") { print field[2]; exit } } }' "$1"
}

io_totals() {
    awk '{ for (i = 2; i <= NF; i++) { split($i, field, "="); totals[field[1]] += field[2] } } END { printf "%.0f,%.0f\n", totals["rbytes"], totals["wbytes"] }' /sys/fs/cgroup/io.stat
}

printf 'unix_ms,cpu_usage_usec,cpu_throttled_usec,memory_bytes,io_read_bytes,io_write_bytes,cpu_pressure_some_usec,io_pressure_some_usec\n'
while [ ! -e "${stop_file}" ]; do
    printf '%s,%s,%s,%s,%s,%s,%s\n' \
        "$(date +%s%3N)" \
        "$(stat_value /sys/fs/cgroup/cpu.stat usage_usec)" \
        "$(stat_value /sys/fs/cgroup/cpu.stat throttled_usec)" \
        "$(cat /sys/fs/cgroup/memory.current)" \
        "$(io_totals)" \
        "$(pressure_total /sys/fs/cgroup/cpu.pressure)" \
        "$(pressure_total /sys/fs/cgroup/io.pressure)"
    sleep 0.1
done
