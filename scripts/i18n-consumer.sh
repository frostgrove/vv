#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

version=${V:-}
[[ -n $version ]] || { echo 'usage: make check-i18n-consumer V=v0.1.0' >&2; exit 1; }

directory=$(mktemp -d "${TMPDIR:-/tmp}/vv-i18n-consumer.XXXXXX")
trap 'rm -rf "$directory"' EXIT

proxy_root="$directory/proxy"
commit_time=$(git show -s --format=%cI HEAD)

write_proxy_metadata() {
	local module_path=$1
	local module_directory=$2
	local proxy_directory="$proxy_root/$module_path/@v"
	mkdir -p "$proxy_directory"
	cp "$module_directory/go.mod" "$proxy_directory/$version.mod"
	printf '{"Version":"%s","Time":"%s"}\n' "$version" "$commit_time" >"$proxy_directory/$version.info"
	printf '%s\n' "$version" >"$proxy_directory/list"
}

write_proxy_metadata "$VV_MODULE" .
root_paths=(.)
while IFS= read -r module; do
	[[ $module == . ]] || root_paths+=(":(exclude)${module#./}")
done < <(all_modules)
git archive \
	--format=zip \
	--prefix="$VV_MODULE@$version/" \
	--output="$proxy_root/$VV_MODULE/@v/$version.zip" \
	HEAD -- "${root_paths[@]}"

write_proxy_metadata "$VV_MODULE/i18n" i18n
git archive \
	--format=zip \
	--prefix="$VV_MODULE/i18n@$version/" \
	--output="$proxy_root/$VV_MODULE/i18n/@v/$version.zip" \
	HEAD:i18n -- .

write_proxy_metadata "$VV_MODULE/crud/rpc/crudgrpc" crud/rpc/crudgrpc
git archive \
	--format=zip \
	--prefix="$VV_MODULE/crud/rpc/crudgrpc@$version/" \
	--output="$proxy_root/$VV_MODULE/crud/rpc/crudgrpc/@v/$version.zip" \
	HEAD:crud/rpc/crudgrpc -- .

upstream_proxy=$("$GO" env GOPROXY)
[[ $upstream_proxy != off ]] || upstream_proxy=direct
consumer_proxy="file://$proxy_root,$upstream_proxy"
cp "$SCRIPT_DIR/i18n-consumer-fixture/main.go.txt" "$directory/main.go"
(
	cd "$directory"
	GOWORK=off "$GO" mod init example.com/vv-i18n-consumer
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" get \
		"$VV_MODULE@$version" "$VV_MODULE/i18n@$version" "$VV_MODULE/crud/rpc/crudgrpc@$version"
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" run \
		"$VV_MODULE/cmd/vv@$version" -dir . -types Product -adapter -out vv_gen.go
	test -s vv_gen.go
	grep -q 'func MountProduct' vv_gen.go
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" mod tidy
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" test -race ./...
	VV_I18N_SOURCE_OUT="$directory/messages.source.json" GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" run -race .
	GOBIN="$directory/bin" GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" install "$VV_MODULE/i18n/cmd/vv-i18n@$version"
	"$directory/bin/vv-i18n" export-ts -source "$directory/messages.source.json" -publication-root "$directory/public.i18n"
	"$directory/bin/vv-i18n" export-ts -source "$directory/messages.source.json" -publication-root "$directory/public.i18n" -check
	cp "$SCRIPT_DIR/i18n-consumer-fixture/publication_reader.go.txt" "$directory/publication_reader.go"
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" run publication_reader.go "$directory/public.i18n"
	test -s "$directory/public.i18n/current.json"
	grep -q 'frostgrove.i18n.publication/v1' "$directory/public.i18n/current.json"
	GONOSUMDB="$VV_MODULE,$VV_MODULE/*" GOPROXY="$consumer_proxy" GOWORK=off "$GO" list -m all
)
echo "check-i18n-consumer: $VV_MODULE/i18n@$version ok"
