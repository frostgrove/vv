#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

# Three module graphs a consumer can actually have, each resolved outside the
# workspace with the proxy off, so what is measured is the graph and not what
# go.work happened to select. The library is untagged, so every vv module is
# reached through a replace onto this tree; everything else resolves from the
# module cache, which is what keeps this arm inside an offline `make check`.
#
# Every step states its own refusal rather than leaning on errexit. This
# repository has been bitten once by a scan that passed because the module could
# not be loaded at all — `make vuln` under GOWORK=off, where a loading error
# reads exactly like a clean report — and a graph that did not resolve links
# nothing, which satisfies every question about what it must not link. So a
# failed `go` command stops its graph on the spot, and each graph asserts a floor
# on what it linked before it asserts anything about what it did not.

consumer_root=
prepared=
consumer_output=

consumer_environment=(GOWORK=off GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-mod=mod)

# Runs one go command in the consumer and reports what it was asking when the
# command refused. What it read is left in consumer_output rather than written to
# stdout, because a caller that redirected stdout to quieten `go mod init` would
# redirect the refusal with it — and a refusal nobody sees is the failure mode
# this whole script exists to avoid.
consumer_go() {
	local label=$1 directory=$2 what=$3
	shift 3
	local status=0
	consumer_output=$(cd "$directory" && env "${consumer_environment[@]}" "$GO" "$@" 2>&1) || status=$?
	if (( status != 0 )); then
		echo "$label could not $what, so nothing about this graph was measured:"
		printf '%s\n' "$consumer_output" | sed 's/^/  /'
		return 1
	fi
}

# A consumer module with no `go.work` above it, requiring exactly the vv modules
# this graph names and replacing each onto its directory here.
prepare_consumer() {
	local label=$1 fixture=$2
	shift 2
	local directory="$consumer_root/$fixture" module path
	local -a edits=()
	mkdir -p "$directory"
	cp "$SCRIPT_DIR/event-consumer-fixture/$fixture/main.go.txt" "$directory/main.go"
	for module in "$@"; do
		path=$(module_path "$module")
		edits+=("-require=$path@v0.0.0" "-replace=$path=$REPO_ROOT/${module#./}")
	done
	consumer_go "$label" "$directory" 'initialise a module of its own' mod init "example.com/vv-event-consumer-$fixture"
	consumer_go "$label" "$directory" 'name the modules this graph is made of' mod edit "${edits[@]}"
	consumer_go "$label" "$directory" 'resolve its module graph with go mod tidy' mod tidy
	consumer_go "$label" "$directory" 'build' build ./...
	consumer_go "$label" "$directory" 'vet' vet ./...
	prepared=$directory
}

vv_modules_are() {
	local label=$1 directory=$2
	shift 2
	local found expected
	consumer_go "$label" "$directory" 'list its module graph' list -m -f '{{.Path}}' all
	found=$(printf '%s\n' "$consumer_output" | grep "^$VV_MODULE\$\|^$VV_MODULE/" | LC_ALL=C sort || true)
	expected=$(printf '%s\n' "$@" | LC_ALL=C sort)
	if [[ -z $found ]]; then
		echo "$label resolved no $VV_MODULE module at all, so its graph was never loaded and nothing below it was measured"
		return 1
	fi
	if [[ $found != "$expected" ]]; then
		echo "$label resolves a different set of $VV_MODULE modules than the graph it is named for:"
		diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$found") | sed 's/^/  /' || true
		return 1
	fi
	echo "  modules: $(printf '%s\n' "$expected" | tr '\n' ' ')"
}

# What the program links, which is a different question from what the module
# graph requires: `eventpg` requires pgx for its own live fixtures and imports no
# package of it, so an event-only consumer carries the requirement and links none
# of it. Only this list can say so.
linked_packages() {
	consumer_go "$1" "$2" 'list what it links' list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...
}

# The floor, and it is the whole reason this script can fail honestly: a graph
# that resolved nothing links nothing, and a list of forbidden packages is
# satisfied by an empty list.
linked_at_least() {
	local label=$1 directory=$2 floor=$3 count
	linked_packages "$label" "$directory"
	count=$(printf '%s\n' "$consumer_output" | grep -c "^$VV_MODULE" || true)
	if (( count < floor )); then
		echo "$label links $count packages of $VV_MODULE and this graph is written to link at least $floor, so it resolved something other than this library"
		return 1
	fi
	echo "  linked: $count packages of $VV_MODULE"
}

# Every non-standard package the program links, minus the consumer's own module
# and minus this library, compared with what the graph is allowed to carry.
links_nothing_but() {
	local label=$1 directory=$2
	shift 2
	local foreign allowed unexpected
	linked_packages "$label" "$directory"
	foreign=$(printf '%s\n' "$consumer_output" | grep -v "^$VV_MODULE" | grep -v "^example\.com/" | LC_ALL=C sort -u || true)
	if (( $# == 0 )); then
		if [[ -n $foreign ]]; then
			echo "$label links third-party packages and this graph is written to link none:"
			printf '%s\n' "$foreign" | sed 's/^/  /'
			return 1
		fi
		echo '  third party: none'
		return 0
	fi
	allowed=$(IFS='|'; echo "$*")
	unexpected=$(printf '%s\n' "$foreign" | grep -Ev "^($allowed)(/|\$)" || true)
	if [[ -n $unexpected ]]; then
		echo "$label links third-party packages outside the ecosystems this graph is named for ($*):"
		printf '%s\n' "$unexpected" | sed 's/^/  /'
		return 1
	fi
	echo "  third party: $(printf '%s\n' "$foreign" | grep -c . || true) packages, all under $*"
}

root_only() {
	local label='root-only'
	echo "$label: the vocabulary, the in-memory store, projections and receipts, from the root module alone"
	prepare_consumer "$label" root-only .
	vv_modules_are "$label" "$prepared" "$VV_MODULE"
	linked_at_least "$label" "$prepared" 8
	links_nothing_but "$label" "$prepared"
}

event_only() {
	local label='event-only'
	echo "$label: the same program over the PostgreSQL store and nothing else of ours"
	prepare_consumer "$label" event-only . event/eventpg
	vv_modules_are "$label" "$prepared" "$VV_MODULE" "$VV_MODULE/event/eventpg"
	linked_at_least "$label" "$prepared" 12
	links_nothing_but "$label" "$prepared"
}

composed() {
	local label='composed'
	echo "$label: event beside tenancy, audit and OpenTelemetry, in one application"
	prepare_consumer "$label" composed . event/eventpg audit/auditpg otel
	vv_modules_are "$label" "$prepared" "$VV_MODULE" "$VV_MODULE/event/eventpg" \
		"$VV_MODULE/audit/auditpg" "$VV_MODULE/otel"
	linked_at_least "$label" "$prepared" 20
	links_nothing_but "$label" "$prepared" go.opentelemetry.io github.com/cespare/xxhash/v2
}

consumer_root=$(mktemp -d "${TMPDIR:-/tmp}/vv-event-consumer.XXXXXX")
trap 'rm -rf "$consumer_root"' EXIT

root_only
event_only
composed
echo 'check-event-consumer: ok'
