#!/usr/bin/env bash

set -euo pipefail

source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"
cd "$REPO_ROOT"

# SHARED is the first-party stdlib-only tier every other tier may import: types
# that are a primitive of the model and the wire rather than of any subsystem.
# A package joins it only if it imports the standard library and nothing else,
# which the TIER0_STDLIB arm below checks for each of them. See D-069.
SHARED=(utils)
TIER0=(crud crud/crudtest crud/query errs errs/sqlerr port port/porthttp "${SHARED[@]}")
TIER0_SEALED=(errs)
# A stdlib-only package may import the standard library and SHARED, and nothing
# else of this repository.
TIER0_STDLIB=(crud "${SHARED[@]}")
SUBSYSTEMS=(crud auth port remote storage app)
TRIPLETS=(
	'crud/http/crudnet,crud/http/crudgin,crud/http/crudfiber'
	'auth/http/authnet,auth/http/authgin,auth/http/authfiber'
	'auth/access/http/accessnet,auth/access/http/accessgin,auth/access/http/accessfiber'
)

# `go mod tidy` reads every build configuration, so a third-party import inside a
# _test.go is a requirement of the published module and the tag it hides behind
# exempts nothing — hence -test and the tag this repository puts fixtures behind.
# A listing that fails is a refusal rather than an empty answer: a test importing
# a package the module does not require fails in exactly that way.
root_third_party() {
	local listing status=0
	listing=$("$GO" list -deps -test -tags=integration -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>&1) || status=$?
	if (( status != 0 )); then
		printf '%s\n' "$listing"
		return "$status"
	fi
	printf '%s\n' "$listing" | grep -v "^$VV_MODULE" | grep -v '^$' || true
}

check_deps() {
	local dependencies module count status=0
	dependencies=$(root_third_party) || status=$?
	if (( status != 0 )); then
		echo 'the root module cannot be listed with its tests — a test importing a package'
		echo 'the module does not require reads exactly like this (D-036):'
		echo "$dependencies" | sed 's/^/  /'
		return 1
	fi
	if [[ -n $dependencies ]]; then
		echo 'the root module has third-party dependencies (D-036) — a package that imports'
		echo 'one belongs in a module of its own, and so does a test that imports one:'
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
	local package dependencies invalid failed=0 tier0_re shared_re
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
		shared_re=$(IFS='|'; echo "${SHARED[*]}")
		invalid=$(echo "$dependencies" | grep "^$VV_MODULE" | sed "s|^$VV_MODULE/||" | grep -Ev "^($package|$shared_re)$" || true)
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
	(grep -ho '^func Test[A-Za-z0-9_]*' "$directory"/*_test.go 2>/dev/null || true) |
		sed 's/^func //' | { grep -v '^TestMain$' || true; } | sort -u > "$output.all"
	(grep -ho '^func Test[A-Za-z0-9_]*' "$directory"/routing_test.go "$directory"/binding_test.go 2>/dev/null || true) |
		sed 's/^func //' | sort -u > "$output.exempt"
	comm -23 "$output.all" "$output.exempt" > "$output"
}

check_triplets() {
	local temporary set directory reference reference_directory output missing extra failed=0
	temporary=$(mktemp -d)
	trap 'rm -rf -- "$temporary"' RETURN
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

check_replaces() {
	local module replaced target declared failed=0
	while IFS= read -r module; do
		while read -r replaced target; do
			if [[ $replaced == "$VV_MODULE" ]]; then
				echo "$module replaces the library it requires:"
				echo "  replace $replaced => $target"
				echo '  a published go.mod carries no replace (D-033). check-tidy adds one for the'
				echo '  length of the check and go.work resolves it for every build, so all this'
				echo '  directive does is hide that the required version is a fiction.'
				failed=1
				continue
			fi
			declared=
			if [[ -f $module/$target/go.mod ]]; then
				declared=$(awk '$1 == "module" { print $2; exit }' "$module/$target/go.mod")
			fi
			if [[ $declared != "$replaced" ]]; then
				echo "$module replaces $replaced with something this repository does not carry:"
				echo "  replace $replaced => $target"
				echo '  a replace of an untagged sibling is the one directive a satellite may keep,'
				echo '  and it has to name the directory that sibling lives in'
				failed=1
			fi
		done < <(replace_directives "$module/go.mod")
	done < <(satellites)
	(( failed == 0 )) || return 1
	echo 'check-replaces: ok'
}

# The question can only be asked with the library replaced by this working tree,
# and asking it therefore writes to the file being checked. Restoring go.mod is
# not the same as dropping the replace again: a satellite that carries its own
# lost it to every `make check`, and `go mod edit` reformats what it rewrites.
# The answer is the exit status — 1 for untidy, 2 and up for a module that does
# not resolve — because `go mod tidy` also prints warnings a tidy module earns.
tidy_diff() {
	local module=$1 root saved saved_sum had_sum=0 status=0
	root=$(realpath --relative-to="$module" .)
	saved=$(mktemp)
	saved_sum=$(mktemp)
	if ! cp "$module/go.mod" "$saved"; then
		echo "cannot copy $module/go.mod aside, so it was left alone"
		rm -f "$saved"
		rm -f "$saved_sum"
		return 2
	fi
	if [[ -f $module/go.sum ]]; then
		had_sum=1
		cp "$module/go.sum" "$saved_sum"
		awk -v module="$VV_MODULE" '$1 != module && index($1, module "/") != 1' "$module/go.sum" >"$module/go.sum.filtered"
		mv "$module/go.sum.filtered" "$module/go.sum"
	fi
	(
		cd "$module" || exit 1
		GOWORK=off "$GO" mod edit -replace "$VV_MODULE=$root" || exit 1
		GOWORK=off "$GO" mod tidy -diff 2>&1
	) || status=$?
	cp "$saved" "$module/go.mod"
	if (( had_sum )); then
		cp "$saved_sum" "$module/go.sum"
	else
		rm -f "$module/go.sum"
	fi
	rm -f "$saved"
	rm -f "$saved_sum"
	return "$status"
}

check_tidy() {
	local module output status failed=0
	while IFS= read -r module; do
		status=0
		case $module in
			. | ./test | ./_examples)
				output=$(cd "$module" && GOWORK=off "$GO" mod tidy -diff 2>&1) || status=$?
				;;
			*)
				output=$(tidy_diff "$module") || status=$?
				;;
		esac
		if (( status == 1 )); then
			echo "$module is not tidy — run make tidy"
		elif (( status != 0 )); then
			echo "$module cannot be read as a module at all:"
		fi
		if (( status != 0 )); then
			[[ -z $output ]] || echo "$output" | sed 's/^/  /'
			failed=1
		fi
	done < <(all_modules)
	(( failed == 0 )) || return 1
	echo 'check-tidy: ok'
}

check_otel_schema() {
	(cd "$REPO_ROOT" && "$GO" run ./cmd/vv-otel-gen -check -registry internal/otelreg/registry.json -out otel/schema_gen.go)
	echo 'check-otel-schema: ok'
}

check_otel_module() {
	(cd "$REPO_ROOT/otel" && GOWORK=off "$GO" test ./...)
	echo 'check-otel-module: ok'
}

check_workspace() {
	local expected actual
	expected=$(workspace_modules | LC_ALL=C sort)
	actual=$(awk '
		/use[[:space:]]*\(/ { inside = 1; next }
		inside && /^\)/ { inside = 0; next }
		inside && $1 ~ /^\./ { print $1 }
		!inside && $1 == "use" && $2 ~ /^\./ { print $2 }
	' go.work | LC_ALL=C sort)
	if [[ "$expected" != "$actual" ]]; then
		echo 'go.work membership differs from discovered workspace modules:'
		diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") || true
		return 1
	fi
	echo 'check-workspace: ok'
}

case ${1:-} in
	all)
		check_deps
		check_tiers
		check_utils
		check_triplets
		check_todo
		check_replaces
		check_tidy
		check_otel_schema
		check_workspace
		;;
	deps) check_deps ;;
	tiers) check_tiers ;;
	utils) check_utils ;;
	triplets) check_triplets ;;
	todo) check_todo ;;
	replaces) check_replaces ;;
	tidy) check_tidy ;;
	otel-schema) check_otel_schema ;;
	otel-module) check_otel_module ;;
	workspace) check_workspace ;;
	*) echo "unknown check: ${1:-}" >&2; exit 2 ;;
esac
