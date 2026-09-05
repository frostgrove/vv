#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

[[ -n ${V:-} ]] || { echo 'usage: make release V=v0.1.0' >&2; exit 1; }
git diff --quiet || { echo 'working tree is dirty' >&2; exit 1; }

while IFS= read -r module; do
	grep -qE "$VV_MODULE $V( //.*)?$" "$module/go.mod" || {
		echo "$module/go.mod does not require the library at $V; run make version V=$V" >&2
		exit 1
	}
done < <(satellites)

scope_version=$(sed -n 's/^[[:space:]]*ScopeVersion[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' otel/schema_gen.go)
[[ $scope_version == "$V" ]] || {
	echo "otel/schema_gen.go has ScopeVersion=$scope_version, want $V; run make version V=$V" >&2
	exit 1
}

"$SCRIPT_DIR/checks.sh" otel-schema
(cd otel && GOWORK=off "$GO" test ./...)
"$SCRIPT_DIR/modules.sh" vet
V="$V" "$SCRIPT_DIR/otel-consumer.sh"

tags=("$V")
while IFS= read -r module; do
	tags+=("${module#./}/$V")
done < <(satellites)
for tag in "${tags[@]}"; do
	if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
		[[ $(git rev-list -n 1 "$tag") == "$(git rev-parse HEAD)" ]] || {
			echo "$tag exists but does not point at HEAD" >&2
			exit 1
		}
	fi
done
for tag in "${tags[@]}"; do
	git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git tag -a "$tag" -m "$tag"
done
git push origin --atomic "${tags[@]}"

echo consumers:
echo "  go get $VV_MODULE@$V"
while IFS= read -r module; do
	echo "  go get $VV_MODULE/${module#./}@$V"
done < <(satellites)
