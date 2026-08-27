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

tags=("$V")
while IFS= read -r module; do
	tags+=("${module#./}/$V")
done < <(satellites)
for tag in "${tags[@]}"; do
	git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git tag -a "$tag" -m "$tag"
done
git push origin --atomic "${tags[@]}"

echo consumers:
echo "  go get $VV_MODULE@$V"
while IFS= read -r module; do
	echo "  go get $VV_MODULE/${module#./}@$V"
done < <(satellites)
