#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

release_root=${VV_RELEASE_ROOT:-$REPO_ROOT}
release_module=${VV_RELEASE_MODULE:-$VV_MODULE}

published_modules() {
	local manifest directory relative
	while IFS= read -r manifest; do
		directory=${manifest%/go.mod}
		relative=${directory#"$release_root"}
		relative=${relative#/}
		case $relative in
			test|test/*|_examples|_examples/*) continue ;;
		esac
		if [[ -z $relative ]]; then
			printf '%s\n' "$release_root"
		else
			printf '%s/%s\n' "$release_root" "$relative"
		fi
	done < <(find "$release_root" -name go.mod -not -path '*/.git/*' -print | LC_ALL=C sort)
}

requires() {
	awk '
		{
			line = $0
			sub(/\/\/.*/, "", line)
			gsub(/^[ \t]+|[ \t]+$/, "", line)
			words = split(line, word, /[ \t]+/)
		}
		words == 0 { next }
		word[1] == "require" && word[2] == "(" { block = 1; next }
		block && word[1] == ")" { block = 0; next }
		word[1] == "require" && words >= 3 { print word[2], word[3]; next }
		block && words >= 2 { print word[1], word[2] }
	' "$1"
}

replaces() {
	awk '
		{
			line = $0
			sub(/\/\/.*/, "", line)
			gsub(/^[ \t]+|[ \t]+$/, "", line)
			words = split(line, word, /[ \t]+/)
		}
		words == 0 { next }
		word[1] == "replace" && word[2] == "(" { block = 1; next }
		block && word[1] == ")" { block = 0; next }
		{
			first = word[1] == "replace" ? 2 : 1
			if (first == 1 && !block) next
			arrow = 0
			for (position = first; position <= words; position++) {
				if (word[position] == "=>") { arrow = position; break }
			}
			if (arrow == 0 || arrow == words) next
			old_version = arrow == first + 2 ? word[first + 1] : "-"
			new_version = words > arrow + 1 ? word[arrow + 2] : "-"
			print word[first], old_version, word[arrow + 1], new_version
		}
	' "$1"
}

first_party() {
	[[ $1 == "$release_module" || $1 == "$release_module/"* ]]
}

local_target() {
	[[ $1 == /* || $1 == . || $1 == ./* || $1 == .. || $1 == ../* ]]
}

module_path_at() {
	awk '$1 == "module" { print $2; exit }' "$1/go.mod"
}

rewrite_requirements() {
	local module dependency current
	while IFS= read -r module; do
		while read -r dependency current; do
			first_party "$dependency" || continue
			(cd "$module" && GOWORK=off "$GO" mod edit -require="$dependency@$V")
		done < <(requires "$module/go.mod")
	done < <(published_modules)
}

drop_local_first_party_replaces() {
	local module dependency old_version target target_version old
	while IFS= read -r module; do
		while read -r dependency old_version target target_version; do
			first_party "$dependency" || continue
			local_target "$target" || continue
			old=$dependency
			[[ $old_version == - ]] || old+="@$old_version"
			(cd "$module" && GOWORK=off "$GO" mod edit -dropreplace="$old")
		done < <(replaces "$module/go.mod")
	done < <(published_modules)
}

tidy_with_checkout() {
	local -a modules paths
	local module path other other_path status
	mapfile -t modules < <(published_modules)
	for module in "${modules[@]}"; do
		paths+=("$(module_path_at "$module")")
	done
	for module in "${modules[@]}"; do
		(
			status=0
			cleanup() {
				local index
				for index in "${!modules[@]}"; do
					[[ ${modules[$index]} == "$module" ]] && continue
					GOWORK=off "$GO" mod edit -dropreplace="${paths[$index]}" >/dev/null 2>&1 || true
				done
			}
			trap cleanup EXIT
			cd "$module"
			for index in "${!modules[@]}"; do
				other=${modules[$index]}
				other_path=${paths[$index]}
				[[ $other == "$module" ]] && continue
				GOWORK=off "$GO" mod edit -replace="$other_path=$other" || status=1
			done
			(( status == 0 )) && GOWORK=off "$GO" mod tidy || status=1
			exit "$status"
		)
	done
}

rewrite() {
	[[ -n ${V:-} ]] || { echo 'release module rewrite requires V' >&2; return 1; }
	rewrite_requirements
	drop_local_first_party_replaces
	tidy_with_checkout
	rewrite_requirements
	drop_local_first_party_replaces
	check
}

check() {
	[[ -n ${V:-} ]] || { echo 'release module preflight requires V' >&2; return 1; }
	local module dependency current old_version target target_version failed=0
	while IFS= read -r module; do
		while read -r dependency current; do
			first_party "$dependency" || continue
			if [[ $current != "$V" ]]; then
				echo "$module/go.mod requires first-party $dependency@$current, want exact $V" >&2
				failed=1
			fi
		done < <(requires "$module/go.mod")
		while read -r dependency old_version target target_version; do
			echo "$module/go.mod contains release-forbidden replace: $dependency => $target" >&2
			failed=1
		done < <(replaces "$module/go.mod")
	done < <(published_modules)
	(( failed == 0 )) || return 1
	echo "release-modules: every published first-party requirement is $V and no published replace remains"
}

case ${1:-} in
	rewrite) rewrite ;;
	check) check ;;
	*) echo "unknown release module task: ${1:-}" >&2; exit 2 ;;
esac
