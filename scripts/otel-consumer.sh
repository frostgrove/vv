#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

version=${V:-}
[[ -n $version ]] || { echo 'usage: make check-otel-consumer V=v0.1.0' >&2; exit 1; }

directory=$(mktemp -d "${TMPDIR:-/tmp}/vv-otel-consumer.XXXXXX")
trap 'rm -rf "$directory"' EXIT
cp "$SCRIPT_DIR/otel-consumer-fixture/main.go.txt" "$directory/main.go"
(
	cd "$directory"
	GOWORK=off "$GO" mod init example.com/vv-otel-consumer
	GOWORK=off "$GO" get "$VV_MODULE/otel@$version"
	GOWORK=off "$GO" test ./...
	GOWORK=off "$GO" list -m all
)
echo "check-otel-consumer: $VV_MODULE/otel@$version ok"
