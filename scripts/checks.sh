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
SUBSYSTEMS=(crud auth port remote storage app tenancy event)

# One sha256 and one path per file under event/ outside event/eventpg. Everything
# in that set is frozen against this manifest: a second store is added with zero
# diffs to the vocabulary, or the kernel gap it needs is reported out loud rather
# than patched. It is a manifest and not a digest because a digest names nothing —
# a diff of two manifests names the file that changed, the one that appeared and
# the one that disappeared — and it needs no git, so the arm runs from a tarball
# or a vendor directory.
EVENT_KERNEL_MANIFEST=scripts/event_kernel.sha256
TRIPLETS=(
	'crud/http/crudnet,crud/http/crudgin,crud/http/crudfiber'
	'auth/http/authnet,auth/http/authgin,auth/http/authfiber'
	'auth/access/http/accessnet,auth/access/http/accessgin,auth/access/http/accessfiber'
)

dependency_modules() {
	satellites | awk -F/ '
		$2 == "test" || $2 == "_examples" { next }
		{
			for (part = 2; part <= NF; part++) {
				if ($part == "testdata" || $part == "vendor" || substr($part, 1, 1) == ".") next
			}
			print
		}
	'
}

dependency_require_directives() {
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

dependency_replace_directives() {
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
				if (word[position] == "=>") {
					arrow = position
					break
				}
			}
			if (arrow == 0 || arrow == words) next
			version = arrow == first + 2 ? word[first + 1] : "-"
			print word[first], version, word[arrow + 1]
		}
	' "$1"
}

local_dependency_sentinel() {
	[[ $1 == v0.0.0 || $1 == v0.0.0-00010101000000-000000000000 ]]
}

# Exact local-development sentinel requirements resolve from the checkout; a
# released requirement remains on its selected MVS version. Every effective
# local replace is rebased into the temporary modfile before the final listing.
isolated_module_list() (
	local module=$1 temporary modfile replacements local_module local_path current required version target old old_version old_versioned key replacement
	local -a edits=()
	local -a pending=()
	local -A local_modules=()
	local -A explicit_replacements=()
	local -A injected=()
	local -A traversed=()
	shift
	temporary=$(mktemp -d)
	trap 'rm -rf -- "$temporary"' EXIT
	modfile="$temporary/check.mod"
	cp -- "$module/go.mod" "$modfile"
	if [[ -f $module/go.sum ]]; then
		cp -- "$module/go.sum" "$temporary/check.sum"
	fi
	while IFS= read -r local_module; do
		local_path=$(module_path "$local_module")
		local_modules["$local_path"]=$local_module
	done < <({ printf '.\n'; dependency_modules; })
	while read -r old old_version target; do
		key=$old
		[[ $old_version == - ]] || key+="@$old_version"
		explicit_replacements["$key"]=$target
	done < <(dependency_replace_directives "$module/go.mod")
	pending+=("$module")
	while (( ${#pending[@]} != 0 )); do
		current=${pending[0]}
		pending=("${pending[@]:1}")
		[[ -z ${traversed["$current"]+set} ]] || continue
		traversed["$current"]=1
		while read -r required version; do
			local_dependency_sentinel "$version" || continue
			local_module=${local_modules["$required"]-}
			[[ -n $local_module && $local_module != "$module" ]] || continue
			replacement=${explicit_replacements["$required@$version"]-${explicit_replacements["$required"]-}}
			if [[ -n $replacement ]]; then
				target=
				if [[ $replacement == /* ]]; then
					target=$replacement
				elif [[ $replacement == . || $replacement == ./* || $replacement == .. || $replacement == ../* ]]; then
					target="$module/$replacement"
				fi
				if [[ -n $target && -f $target/go.mod ]]; then
					target=$(cd "$target" && pwd)
					pending+=("$target")
				fi
				continue
			fi
			key="$required@$version"
			if [[ -z ${injected["$key"]+set} ]]; then
				target=$(cd "$local_module" && pwd)
				edits+=("-replace=$key=$target")
				injected["$key"]=1
			fi
			pending+=("$local_module")
		done < <(dependency_require_directives "$current/go.mod")
	done
	if (( ${#edits[@]} != 0 )); then
		(
			cd "$module"
			GOWORK=off "$GO" mod edit -modfile="$modfile" "${edits[@]}"
		)
	fi
	replacements=$(
		cd "$module"
		GOWORK=off "$GO" list -e -mod=mod -modfile="$modfile" -m \
			-f '{{if .Replace}}{{if not .Replace.Version}}{{printf "%s\n%s\n%s" .Path .Version .Replace.Dir}}{{end}}{{end}}' all
	)
	edits=()
	if [[ -n $replacements ]]; then
		while IFS= read -r old && IFS= read -r version && IFS= read -r target; do
			[[ -n $target ]] || continue
			old_versioned=$old
			[[ -z $version ]] || old_versioned+="@$version"
			edits+=("-replace=$old_versioned=$target")
		done <<< "$replacements"
	fi
	if (( ${#edits[@]} != 0 )); then
		(
			cd "$module"
			GOWORK=off "$GO" mod edit -modfile="$modfile" "${edits[@]}"
		)
	fi
	(
		cd "$module"
		GOWORK=off "$GO" list -mod=mod -modfile="$modfile" "$@"
	)
)

# `go mod tidy` reads every build configuration, so a third-party import inside a
# _test.go is a requirement of the published module and the tag it hides behind
# exempts nothing — hence -test and the tag this repository puts fixtures behind.
# A listing that fails is a refusal rather than an empty answer: a test importing
# a package the module does not require fails in exactly that way.
root_third_party() {
	local listing status=0
	listing=$(isolated_module_list . -deps -test -tags=integration -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>&1) || status=$?
	if (( status != 0 )); then
		printf '%s\n' "$listing"
		return "$status"
	fi
	printf '%s\n' "$listing" | grep -v "^$VV_MODULE" | grep -v '^$' || true
}

check_deps() {
	local dependencies module count otel_dependencies status=0 failed=0
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
		status=0
		dependencies=$(isolated_module_list "$module" -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... 2>&1) || status=$?
		if (( status != 0 )); then
			echo "cannot list production dependencies for $module:"
			echo "$dependencies" | sed 's/^/  /'
			failed=1
			continue
		fi
		dependencies=$(grep -v "^$VV_MODULE" <<<"$dependencies" || true)
		count=$(awk 'NF { count++ } END { print count + 0 }' <<<"$dependencies")
		echo "$module: $count external packages"
		if [[ $module != ./otel ]]; then
			otel_dependencies=$(grep '^go\.opentelemetry\.io/' <<<"$dependencies" | LC_ALL=C sort -u || true)
			if [[ -n $otel_dependencies ]]; then
				echo "$module reaches OpenTelemetry outside the isolated ./otel module:"
				echo "$otel_dependencies" | sed 's/^/  /'
				failed=1
			fi
		fi
	done < <(dependency_modules)
	(( failed == 0 )) || return 1
	if [[ -f scripts/otel_dependency_test.go ]]; then
		GOWORK=off "$GO" test -mod=readonly -count=1 ./scripts -run '^(TestPublishedModulesOutsideOTelRemainOTelFree|TestVVOTelProduction.*)$'
	fi
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
	(cd "$REPO_ROOT" && "$GO" run ./cmd/vv-otel-gen -check -registry internal/otelreg/registry.json -out otel/schema_gen.go -manifest otel/wire_manifest.json)
	echo 'check-otel-schema: ok'
}

check_otel_module() (
	local temporary modfile consumer
	temporary=$(mktemp -d)
	trap 'rm -rf -- "$temporary"' EXIT
	modfile="$temporary/otel.mod"
	cp "$REPO_ROOT/otel/go.mod" "$modfile"
	[[ ! -f $REPO_ROOT/otel/go.sum ]] || cp "$REPO_ROOT/otel/go.sum" "$temporary/otel.sum"
	GOWORK=off "$GO" mod edit -modfile="$modfile" -replace="$VV_MODULE=$REPO_ROOT"
	(cd "$REPO_ROOT/otel" && GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" test -count=1 -modfile="$modfile" ./...)
	consumer="$temporary/consumer"
	mkdir "$consumer"
	cp "$SCRIPT_DIR/otel-consumer-fixture/main.go.txt" "$consumer/main.go"
	(
		cd "$consumer"
		GOWORK=off "$GO" mod init example.com/vv-otel-local-consumer >/dev/null
		GOWORK=off "$GO" mod edit -require="$VV_MODULE/otel@v0.0.0"
		GOWORK=off "$GO" mod edit -replace="$VV_MODULE/otel=$REPO_ROOT/otel"
		GOWORK=off "$GO" mod edit -replace="$VV_MODULE=$REPO_ROOT"
		GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" test -mod=mod -count=1 ./...
	)
	echo 'check-otel-module: ok'
)

check_otel_operations() {
	"$SCRIPT_DIR/otel-operations.sh" offline
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

# Enumerated with find and sorted LC_ALL=C, so the manifest is byte-stable across
# machines and a file nobody tracked is in it too — the half the git spelling
# needed a second command for.
event_kernel_manifest() {
	find event -type f -not -path 'event/eventpg/*' -print0 2>/dev/null |
		LC_ALL=C sort -z |
		xargs -0 -r sha256sum || true
}

# Every arm refuses rather than reporting ok when it cannot ask its question: a
# check that passes because sha256sum is missing, because the manifest was
# deleted, or because event/ itself moved is a green line nobody earned.
check_event_kernel() {
	local computed difference
	if ! command -v sha256sum >/dev/null 2>&1; then
		echo 'check-event-kernel needs sha256sum and this environment has none, so the frozen kernel was compared with nothing'
		return 1
	fi
	if [[ ! -s $EVENT_KERNEL_MANIFEST ]]; then
		echo "check-event-kernel reads $EVENT_KERNEL_MANIFEST and this tree holds no such file, or it is empty"
		echo '  record one with make check-event-kernel-baseline'
		return 1
	fi
	computed=$(event_kernel_manifest)
	if [[ -z $computed ]]; then
		echo 'check-event-kernel found no file at all under event/ outside event/eventpg, and a directory that moved would otherwise read as a clean tree'
		return 1
	fi
	difference=$(diff -u --label "$EVENT_KERNEL_MANIFEST" --label 'event/ as it stands' "$EVENT_KERNEL_MANIFEST" <(printf '%s\n' "$computed")) || true
	if [[ -n $difference ]]; then
		echo 'event/ outside event/eventpg differs from the recorded manifest:'
		printf '%s\n' "$difference" | sed 's/^/  /'
		echo '  a second store is written with zero diffs to the vocabulary. Either the'
		echo '  constructor you need already exists and was not found, or this is a real'
		echo '  kernel gap — which is reported out loud rather than patched here.'
		echo '  A deliberate move is recorded with make check-event-kernel-baseline, in the'
		echo '  same change as the code, so what a reviewer reads is the diff above.'
		return 1
	fi
	echo 'check-event-kernel: ok'
}

# Re-baselining is a recorded act and not a relaxation, and the difference is
# mechanical: moving a digest is one opaque line in a diff, and regenerating this
# file lists every path that moved next to the code that moved it.
#
# Phase 3 moved it, and this is what moved and why. event/checkpoint.go is the
# checkpoint contract, its door and its fence — two store packages implement it
# and a third must be provable against it, so it cannot live in the consumer.
# event/bounds.go gains MaxCursorBytes, because the ceilings are one list.
# event/reader.go's checkPage takes the cursor, because store honesty is checked
# in one place. event/fact.go gains Family and Read, because only Fact holds the
# reader chain. event/eventtest/ gains the checkpoint conformance runner, because
# the suite is the kernel's evidence half. event/eventmemory/checkpoints.go
# arrives beside transaction.go, log.go and store.go, which carry the ambient
# join a checkpoint save inside a unit of work needs. And event/projection/ is
# the consumer itself, a package of the root module because phase 3 adds no
# module.
event_kernel_baseline() {
	local computed
	if ! command -v sha256sum >/dev/null 2>&1; then
		echo 'event-kernel-baseline needs sha256sum and this environment has none, so nothing was recorded'
		return 1
	fi
	computed=$(event_kernel_manifest)
	if [[ -z $computed ]]; then
		echo 'event-kernel-baseline found no file at all under event/ outside event/eventpg, and a manifest of nothing certifies nothing'
		return 1
	fi
	printf '%s\n' "$computed" >"$EVENT_KERNEL_MANIFEST"
	echo "event-kernel-baseline: $(printf '%s\n' "$computed" | wc -l) files recorded in $EVENT_KERNEL_MANIFEST"
}

# check-event-kernel is green by construction the instant the baseline is
# regenerated, so the arm that reads WHAT the re-baseline moved is the one thing
# standing between a phase and an unrecorded kernel edit. It is a command rather
# than a shell pipeline because a command can be given a predecessor that
# survives a commit, can be self-tested, and matches with bash's own [[ =~ ]].
#
# The moved set is every path whose line differs between the two manifests in
# either direction, so a file that appeared, one that changed and one that
# disappeared are all in it. An empty moved set fails: a section that moved no
# kernel file did not deliver.
event_kernel_moved() {
	local predecessor=${1:-} allowed=${2:-} moved path
	local unplanned=() missing=()
	if (( $# < 2 )); then
		echo 'event-kernel-moved reads what a section moved and needs both halves of the question:'
		echo '  ./scripts/checks.sh event-kernel-moved <predecessor> <allowed-ERE> [required-path...]'
		return 1
	fi
	if [[ ! -s $predecessor ]]; then
		echo "event-kernel-moved compares against $predecessor and this tree holds no such file, or it is empty"
		echo '  a section records one before it writes its first file:'
		echo "    ./scripts/checks.sh event-kernel-baseline && cp $EVENT_KERNEL_MANIFEST $predecessor"
		return 1
	fi
	if [[ ! -s $EVENT_KERNEL_MANIFEST ]]; then
		echo "event-kernel-moved reads $EVENT_KERNEL_MANIFEST and this tree holds no such file, or it is empty"
		echo '  record one with make check-event-kernel-baseline'
		return 1
	fi
	moved=$(LC_ALL=C sort "$predecessor" "$EVENT_KERNEL_MANIFEST" | uniq -u | sed 's/^[0-9a-f]\{64\}  //' | LC_ALL=C sort -u)
	if [[ -z $moved ]]; then
		echo "$EVENT_KERNEL_MANIFEST records the same kernel as $predecessor, and a section that moved no file under event/ did not deliver"
		return 1
	fi
	while IFS= read -r path; do
		[[ $path =~ $allowed ]] || unplanned+=("$path")
	done <<<"$moved"
	for path in "${@:3}"; do
		[[ $'\n'$moved$'\n' == *$'\n'$path$'\n'* ]] || missing+=("$path")
	done
	if (( ${#unplanned[@]} > 0 )); then
		echo 'these files under event/ moved and this section was not told about them:'
		printf '  %s\n' "${unplanned[@]}"
		echo '  an unplanned kernel edit is reported out loud rather than absorbed into the baseline'
		return 1
	fi
	if (( ${#missing[@]} > 0 )); then
		echo 'these files were to move in this section and did not:'
		printf '  %s\n' "${missing[@]}"
		echo '  a section that did not move what it promised did not deliver'
		return 1
	fi
	echo 'the files under event/ this section moved:'
	printf '%s\n' "$moved" | sed 's/^/  /'
	echo 'event-kernel-moved: ok'
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
		check_otel_module
		check_otel_operations
		check_workspace
		check_event_kernel
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
	otel-operations) check_otel_operations ;;
	workspace) check_workspace ;;
	event-kernel) check_event_kernel ;;
	event-kernel-baseline) event_kernel_baseline ;;
	event-kernel-moved) shift; event_kernel_moved "$@" ;;
	*) echo "unknown check: ${1:-}" >&2; exit 2 ;;
esac
