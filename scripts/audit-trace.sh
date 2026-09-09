#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
	printf 'usage: %s S<N>\n' "$0" >&2
	exit 2
fi

case "$1" in
	S0|S1|S2|S3|S4|S5|S6|S7) ;;
	*)
		printf 'invalid audit trace section: %s\n' "$1" >&2
		exit 2
		;;
esac

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd -- "$script_directory/.." && pwd)
cd -- "$repository_root"

bootstrap=(go test -json -count=1 -run '^TestAuditTraceRegistry$' ./scripts)
if ! bootstrap_output=$("${bootstrap[@]}"); then
	printf '%s\n' "$bootstrap_output" >&2
	exit 1
fi

AUDIT_TRACE_SECTION="$1" AUDIT_TRACE_BOOTSTRAP_JSON="$bootstrap_output" \
	go test -count=1 -run '^TestTraceCheckpointRunner$' ./scripts
