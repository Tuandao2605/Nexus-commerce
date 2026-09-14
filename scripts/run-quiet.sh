#!/bin/sh
# File này chạy một verification command với output ngắn khi PASS và giữ đầy đủ diagnostic khi FAIL.
set -eu

if [ "$#" -lt 2 ]; then
    printf 'usage: %s <label> <command> [args...]\n' "$0" >&2
    exit 64
fi

label=$1
shift
log_file=$(mktemp "${TMPDIR:-/tmp}/nexus-agent-check.XXXXXX")

# cleanup xóa đúng log tạm do runner tạo khi command kết thúc hoặc bị ngắt.
cleanup() {
    rm -f "$log_file"
}

trap cleanup EXIT HUP INT TERM

if "$@" >"$log_file" 2>&1; then
    printf 'PASS: %s\n' "$label"
    exit 0
else
    exit_code=$?
fi

printf 'FAIL: %s (exit %s)\n' "$label" "$exit_code" >&2
cat "$log_file" >&2
exit "$exit_code"
