#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

TIER0=(crud crud/crudtest crud/query errs errs/sqlerr port port/porthttp)
TIER0_SEALED=(errs)
TIER0_STDLIB=(crud)
SUBSYSTEMS=(crud auth port remote)
TRIPLETS=(
	'crud/http/crudnet,crud/http/crudgin,crud/http/crudfiber'
	'auth/http/authnet,auth/http/authgin,auth/http/authfiber'
)

check_deps() {
	local dependencies module count
	dependencies=$("$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | grep -v "^$VV_MODULE" || true)
	if [[ -n $dependencies ]]; then
		echo 'the root module has third-party dependencies:'
		echo "$dependencies" | sed 's/^/  /'
		return 1
	fi
	while IFS= read -r module; do
		dependencies=$(cd "$module" && "$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | grep -v "^$VV_MODULE" || true)
		count=$(awk 'NF { count++ } END { print count + 0 }' <<<"$dependencies")
		echo "$module: $count external packages"
	done < <(satellites)
	echo 'check-deps: ok'
}

check_tiers() {
	local package dependencies invalid failed=0 tier0_re
	tier0_re=$(IFS='|'; echo "${TIER0[*]}")
	for package in "${TIER0[@]}"; do
		[[ -d $package ]] || continue
		if ! dependencies=$("$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$package" 2>&1); then
			echo "cannot list $package — it does not build:"
			echo "$dependencies" | sed 's/^/  /'
			failed=1
			continue
		fi
		invalid=$(echo "$dependencies" | grep "^$VV_MODULE/" | sed "s|^$VV_MODULE/||" | grep -Ev "^($tier0_re)$" || true)
		if [[ -n $invalid ]]; then
			echo "contract package $package imports non-contract packages:"
			echo "$invalid" | sort -u | sed 's/^/  /'
			failed=1
		fi
	done
	for package in "${TIER0_STDLIB[@]}"; do
		[[ -d $package ]] || continue
		if ! dependencies=$("$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$package" 2>&1); then
			echo "cannot list $package — it does not build:"
			echo "$dependencies" | sed 's/^/  /'
			failed=1
			continue
		fi
		invalid=$(echo "$dependencies" | grep "^$VV_MODULE" | sed "s|^$VV_MODULE/||" | grep -Ev "^$package$" || true)
		if [[ -n $invalid ]]; then
			echo "stdlib-only package $package may import only the standard library:"
			echo "$invalid" | sort -u | sed 's/^/  /'
			failed=1
		fi
	done
	for package in "${TIER0_SEALED[@]}"; do
		[[ -d $package ]] || continue
		if ! dependencies=$("$GO" list -deps -test -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "./$package/..." 2>&1); then
			echo "cannot list $package — it does not build:"
			echo "$dependencies" | sed 's/^/  /'
			failed=1
			continue
		fi
		invalid=$(echo "$dependencies" | grep "^$VV_MODULE" | sed "s|^$VV_MODULE/||" | sed -E 's/ \[.*\]$//; s/\.test$//; s/_test$//' | grep -Ev "^$package(/|$)" || true)
		if [[ -n $invalid ]]; then
			echo "sealed package $package may import only the standard library and $package/...:"
			echo "$invalid" | sort -u | sed 's/^/  /'
			failed=1
		fi
	done
	(( failed == 0 )) || return 1
	echo 'check-tiers: ok'
}

check_utils() {
	local dependencies invalid subsystems_re module
	subsystems_re=$(IFS='|'; echo "${SUBSYSTEMS[*]}")
	if ! dependencies=$(
		{
			"$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./utils/...
			while IFS= read -r module; do
				[[ $module == ./utils/* ]] || continue
				(cd "$module" && "$GO" list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...)
			done < <(all_modules)
		} 2>&1
	); then
		echo 'cannot list utils/ — it does not build:'
		echo "$dependencies" | sed 's/^/  /'
		return 1
	fi
	invalid=$(echo "$dependencies" | grep "^$VV_MODULE/" | sed "s|^$VV_MODULE/||" | grep -E "^($subsystems_re)(/|$)" || true)
	if [[ -n $invalid ]]; then
		echo 'a package under utils/ imports a subsystem — it is not a utility (D-058):'
		echo "$invalid" | sort -u | sed 's/^/  /'
		return 1
	fi
	echo 'check-utils: ok'
}

test_names() {
	local directory=$1 output=$2
	(grep -h '^func Test[A-Za-z0-9_]*' "$directory"/*_test.go 2>/dev/null || true) |
		sed 's/^func //' | { grep -v '^TestMain$' || true; } | sort -u > "$output.all"
	(grep -h '^func Test[A-Za-z0-9_]*' "$directory"/routing_test.go "$directory"/binding_test.go 2>/dev/null || true) |
		sed 's/^func //' | sort -u > "$output.exempt"
	comm -23 "$output.all" "$output.exempt" > "$output"
}

check_triplets() {
	local temporary set directory reference reference_directory output missing extra failed=0
	temporary=$(mktemp -d)
	for set in "${TRIPLETS[@]}"; do
		reference=
		reference_directory=
		IFS=',' read -r -a directories <<< "$set"
		for directory in "${directories[@]}"; do
			if [[ ! -d $directory ]]; then
				echo "$directory is named as part of a triplet and does not exist"
				failed=1
				continue
			fi
			output="$temporary/${directory//\//_}"
			test_names "$directory" "$output"
			if [[ -z $reference ]]; then
				reference=$output
				reference_directory=$directory
				continue
			fi
			missing=$(comm -23 "$reference" "$output")
			extra=$(comm -13 "$reference" "$output")
			if [[ -n $missing || -n $extra ]]; then
				echo "$reference_directory and $directory do not carry the same test names. A test that only makes"
				echo 'sense for one binding belongs in its routing_test.go or binding_test.go, and the'
				echo 'difference it pins belongs in FL-013:'
				[[ -z $missing ]] || echo "$missing" | sed "s|^|  only in $reference_directory: |"
				[[ -z $extra ]] || echo "$extra" | sed "s|^|  only in $directory: |"
				failed=1
			fi
		done
	done
	rm -rf "$temporary"
	(( failed == 0 )) || return 1
	echo 'check-triplets: ok'
}

check_todo() {
	local stale
	stale=$(find . -name TODO.md -not -path './.git/*' -printf '%h\n' 2>/dev/null | while IFS= read -r directory; do
		compgen -G "$directory/*.go" >/dev/null && echo "$directory"
	done)
	if [[ -n $stale ]]; then
		echo 'TODO.md left beside real code — delete it in the change that added the code:'
		echo "$stale" | sed 's/^/  /'
		return 1
	fi
	echo 'check-todo: ok'
}

check_tidy() {
	local module root output failed=0
	while IFS= read -r module; do
		root=$(realpath --relative-to="$module" .)
		output=$(cd "$module" && GOWORK=off "$GO" mod edit -replace "$VV_MODULE=$root" && GOWORK=off "$GO" mod tidy -diff 2>&1; GOWORK=off "$GO" mod edit -dropreplace "$VV_MODULE")
		if [[ -n $output ]]; then
			echo "$module is not tidy — run make tidy"
			failed=1
		fi
	done < <(satellites)
	while IFS= read -r module; do
		output=$(cd "$module" && GOWORK=off "$GO" mod tidy -diff 2>&1)
		if [[ -n $output ]]; then
			echo "$module is not tidy — run make tidy"
			failed=1
		fi
	done < <(printf '.\n./test\n./_examples\n')
	(( failed == 0 )) || return 1
	echo 'check-tidy: ok'
}

case ${1:-} in
	all)
		check_deps
		check_tiers
		check_utils
		check_triplets
		check_todo
		check_tidy
		;;
	deps) check_deps ;;
	tiers) check_tiers ;;
	utils) check_utils ;;
	triplets) check_triplets ;;
	todo) check_todo ;;
	tidy) check_tidy ;;
	*) echo "unknown check: ${1:-}" >&2; exit 2 ;;
esac
