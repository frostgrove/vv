#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

collector_image='otel/opentelemetry-collector-contrib@sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6'
prometheus_image='prom/prometheus@sha256:76947e7ef22f8a698fc638f706685909be425dbe09bd7a2cd7aca849f79b5f64'
weaver_image='otel/weaver@sha256:d1fb16d279f39810c340fbbf1cf9e5e995a3a9cefa531938e9012437e3bc00c1'
collector_directory="$REPO_ROOT/_examples/otel-collector"
operations_directory="$REPO_ROOT/docs/operations/otel"

offline() {
	GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" test -mod=readonly -count=1 ./scripts -run '^TestOTelOperations'
	GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" test -mod=readonly -count=1 ./scripts/otel-wire-validator
}

collector() {
	command -v docker >/dev/null 2>&1 || { echo 'check-otel-collector requires Docker' >&2; return 1; }
	local config container started storage
	storage=$(mktemp -d "${TMPDIR:-/tmp}/vv-otelcol-storage.XXXXXX")
	trap 'rm -rf -- "$storage"' RETURN
	for config in collector.dev.yaml collector.production.yaml collector.production-wal.yaml collector.tail.yaml collector.metrics-only-management-filter.yaml; do
		echo "==> validate $config"
		docker run --rm --entrypoint /otelcol-contrib \
			-v "$collector_directory/$config:/etc/otelcol-contrib/config.yaml:ro" \
			"$collector_image" validate --config=/etc/otelcol-contrib/config.yaml
		container="vv-otelcol-check-$$-${config//[^a-zA-Z0-9]/-}"
		docker run --rm -d --name "$container" \
			-v "$collector_directory/$config:/etc/otelcol-contrib/config.yaml:ro" \
			-v "$storage:/var/lib/otelcol" \
			"$collector_image" --config=/etc/otelcol-contrib/config.yaml >/dev/null
		started=false
		for _ in 1 2 3 4 5 6 7 8; do
			if [[ $(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || true) == true ]]; then
				started=true
				break
			fi
			sleep 0.25
		done
		if [[ $started != true ]]; then
			docker logs "$container" >&2 || true
			docker rm -f "$container" >/dev/null 2>&1 || true
			return 1
		fi
		docker stop "$container" >/dev/null
	done
	echo 'check-otel-collector: pinned configs validate and start'
}

prometheus() {
	if command -v promtool >/dev/null 2>&1; then
		(cd "$operations_directory" && promtool check rules prometheus-rules.yaml && promtool test rules prometheus-tests.yaml)
	elif command -v docker >/dev/null 2>&1; then
		docker run --rm --entrypoint /bin/promtool -v "$operations_directory:/work:ro" -w /work "$prometheus_image" check rules prometheus-rules.yaml
		docker run --rm --entrypoint /bin/promtool -v "$operations_directory:/work:ro" -w /work "$prometheus_image" test rules prometheus-tests.yaml
	else
		echo 'check-otel-prometheus skipped: install promtool or Docker' >&2
		return 0
	fi
	echo 'check-otel-prometheus: rules and fixtures pass'
}

weaver() {
	command -v docker >/dev/null 2>&1 || { echo 'check-otel-weaver skipped: Docker is unavailable' >&2; return 0; }
	command -v curl >/dev/null 2>&1 || { echo 'check-otel-weaver requires curl' >&2; return 1; }
	command -v jq >/dev/null 2>&1 || { echo 'check-otel-weaver requires jq' >&2; return 1; }
	local container listener_pid ready report status work
	container="vv-weaver-live-$$"
	work=$(mktemp -d "${TMPDIR:-/tmp}/vv-weaver-live.XXXXXX")
	report="$work/report.json"
	trap 'docker rm -f "$container" >/dev/null 2>&1 || true; rm -rf -- "$work"' RETURN
	if curl --silent --fail --max-time 1 http://127.0.0.1:14320/health >/dev/null 2>&1; then
		echo 'check-otel-weaver requires free localhost ports 14317 and 14320' >&2
		return 1
	fi
	docker run --rm --name "$container" --network host "$weaver_image" \
		registry live-check --v2 \
		-r 'https://github.com/open-telemetry/semantic-conventions@v1.40.0[model]' \
		--input-source otlp \
		--otlp-grpc-address 127.0.0.1 \
		--otlp-grpc-port 14317 \
		--admin-port 14320 \
		--inactivity-timeout 120 \
		--format json \
		--output http \
		--diagnostic-format json >"$work/weaver.log" 2>&1 &
	listener_pid=$!
	ready=false
	for _ in {1..300}; do
		if curl --silent --fail --max-time 1 http://127.0.0.1:14320/health >/dev/null 2>&1; then
			ready=true
			break
		fi
		kill -0 "$listener_pid" >/dev/null 2>&1 || break
		sleep 1
	done
	if [[ $ready != true ]]; then
		cat "$work/weaver.log" >&2
		return 1
	fi
	(
		cd test
		env -u OTEL_SEMCONV_STABILITY_OPT_IN GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" run ./otelnative/cmd/weaver-native-http -endpoint 127.0.0.1:14317
	)
	curl --silent --show-error --fail --max-time 30 -X POST http://127.0.0.1:14320/stop >"$report"
	status=0
	wait "$listener_pid" || status=$?
	listener_pid=
	if (( status != 0 )); then
		cat "$work/weaver.log" >&2
		[[ ! -s $report ]] || cat "$report" >&2
		return "$status"
	fi
	if ! jq -e '
		.statistics as $statistics |
		($statistics.total_entities | type == "number" and . > 0) and
		($statistics.total_entities_by_type.span | type == "number" and . >= 1) and
		($statistics.seen_registry_metrics["http.server.request.duration"] | type == "number" and . >= 1) and
		(($statistics.advice_level_counts.violation // 0) | type == "number" and . == 0)
	' "$report" >/dev/null; then
		cat "$report" >&2
		return 1
	fi
	if [[ -n ${VV_OTEL_WEAVER_REPORT:-} ]]; then
		cp -- "$report" "$VV_OTEL_WEAVER_REPORT"
		echo "check-otel-weaver: report retained at $VV_OTEL_WEAVER_REPORT"
	fi
	echo 'check-otel-weaver: pinned live-check observed native HTTP spans and metrics without violations'
}

wire() {
	[[ -n ${OTEL_WIRE_INPUT:-} ]] || { echo 'check-otel-wire requires OTEL_WIRE_INPUT' >&2; return 1; }
	local -a options=()
	[[ ${VV_OTEL_WIRE_ALLOW_PARTIAL:-0} != 1 ]] || options+=(-allow-partial)
	GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" run ./scripts/otel-wire-validator -manifest otel/wire_manifest.json -input "$OTEL_WIRE_INPUT" "${options[@]}"
	echo 'check-otel-wire: captured Frostgrove OTLP matches vv-otel/v2'
}

case ${1:-} in
	offline) offline ;;
	collector) collector ;;
	prometheus) prometheus ;;
	weaver) weaver ;;
	wire) wire ;;
	*) echo "unknown OTel operation check: ${1:-}" >&2; exit 2 ;;
esac
