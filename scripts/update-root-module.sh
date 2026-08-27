#!/usr/bin/env bash

set -euo pipefail

revision=${1:-main}
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
root_module=$(cd "$repo_root" && GOWORK=off go list -m -f '{{.Path}}')

# Resolve the revision once so the output tells a reviewer exactly what every
# satellite was pinned to. The pseudo-version is valid in go.mod; branch names
# are queries, not require versions.
# The module proxy caches pseudo-versions for branches. A development command
# must use the VCS directly: otherwise `@main` can pin a module to yesterday's
# main and preserve the dependency this command is meant to replace.
root_version=$(GOWORK=off GOPROXY=direct go list -m -f '{{.Version}}' "${root_module}@${revision}")
if [[ -z "$root_version" ]]; then
	echo "could not resolve ${root_module}@${revision}" >&2
	exit 1
fi

updated=0
while IFS= read -r -d '' mod_file; do
	module_dir=$(dirname "$mod_file")
	if [[ "$module_dir" == "$repo_root" ]]; then
		continue
	fi

	# A module that does not import the root library must not gain a needless
	# dependency merely because it lives in this repository.
	if ! awk -v module="$root_module" '$1 == module { found = 1 } END { exit !found }' "$mod_file"; then
		continue
	fi

	relative_dir=${module_dir#"$repo_root"/}
	echo "==> ${relative_dir}: ${root_module}@${root_version}"
	(
		cd "$module_dir"
		# go get first resolves the version already written in go.mod. That
		# cannot repair an old v0.0.0 requirement because v0.0.0 was never a
		# published tag. Edit the requirement directly, then download the new
		# module to record its checksum.
		GOWORK=off go mod edit -require="${root_module}@${root_version}"
		GOWORK=off GOPROXY=direct go mod download "${root_module}@${root_version}"
	)
	updated=$((updated + 1))
done < <(find "$repo_root" -name go.mod -not -path "$repo_root/.git/*" -not -path '*/vendor/*' -print0 | sort -z)

if [[ "$updated" -eq 0 ]]; then
	echo "no nested module requires ${root_module}" >&2
	exit 1
fi

echo "updated ${updated} module(s) to ${root_module}@${root_version}"
