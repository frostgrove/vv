#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

version=${V:-}
[[ -n $version ]] || { echo 'usage: make check-otel-consumer V=v0.1.0' >&2; exit 1; }

root=$(mktemp -d "${TMPDIR:-/tmp}/vv-otel-consumer.XXXXXX")
trap 'rm -rf "$root"' EXIT
directory="$root/consumer"
module_cache="$root/modcache"
build_cache="$root/buildcache"
consumer_gopath="$root/gopath"
mkdir -p "$directory" "$module_cache" "$build_cache" "$consumer_gopath"
cp "$SCRIPT_DIR/otel-consumer-fixture/main.go.txt" "$directory/main.go"
(
	cd "$directory"
	clean_env=(GOENV=off GOWORK=off GOFLAGS=-mod=mod GOMODCACHE="$module_cache" GOCACHE="$build_cache" GOPATH="$consumer_gopath")
	env "${clean_env[@]}" "$GO" mod init example.com/vv-otel-consumer
	env "${clean_env[@]}" "$GO" get "$VV_MODULE/otel@$version"
	resolved=$(env "${clean_env[@]}" "$GO" list -m -f '{{.Path}}|{{.Version}}|{{if .Replace}}{{.Replace.Path}}{{end}}|{{.Dir}}' "$VV_MODULE/otel")
	IFS='|' read -r resolved_path resolved_version resolved_replace resolved_directory <<<"$resolved"
	[[ $resolved_path == "$VV_MODULE/otel" && $resolved_version == "$version" && -z $resolved_replace ]] || {
		echo "consumer resolved $resolved, want exact no-replace $VV_MODULE/otel@$version" >&2
		exit 1
	}
	[[ $resolved_directory == "$module_cache"/* && $resolved_directory != "$REPO_ROOT" && $resolved_directory != "$REPO_ROOT"/* ]] || {
		echo "consumer resolved nested module from forbidden directory $resolved_directory" >&2
		exit 1
	}
	env "${clean_env[@]}" "$GO" test ./...
	env "${clean_env[@]}" "$GO" list -m all
)
echo "check-otel-consumer: $VV_MODULE/otel@$version ok"
