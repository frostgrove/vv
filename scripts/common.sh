#!/usr/bin/env bash

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
VV_MODULE=github.com/frostgrove/vv
GO=${GO:-go}

all_modules() {
	find . -name go.mod -not -path './.git/*' -exec dirname {} \; | LC_ALL=C sort
}

workspace_modules() {
	all_modules | awk '$0 != "./_examples"'
}

satellites() {
	all_modules | awk '$0 != "." && $0 != "./test" && $0 != "./_examples"'
}

module_path() {
	if [[ $1 == . ]]; then
		printf '%s\n' "$VV_MODULE"
	else
		printf '%s/%s\n' "$VV_MODULE" "${1#./}"
	fi
}
