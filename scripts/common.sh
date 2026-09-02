#!/usr/bin/env bash

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
VV_MODULE=github.com/frostgrove/vv
GO=${GO:-go}

all_modules() {
	find . -name go.mod -not -path './.git/*' -exec dirname {} \; | LC_ALL=C sort
}

workspace_modules() {
	all_modules | awk '$0 != "./_examples"'
}

satellites() {
	all_modules | awk '$0 != "." && $0 != "./test" && $0 != "./_examples"'
}

module_path() {
	if [[ $1 == . ]]; then
		printf '%s\n' "$VV_MODULE"
	else
		printf '%s/%s\n' "$VV_MODULE" "${1#./}"
	fi
}

# Prints "<replaced module> <target>" per replace, reading both the one-line form
# `go mod edit` writes and the parenthesised block a person writes by hand. The
# target is whatever follows `=>`: a directory here, a module path in a fork.
replace_directives() {
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
			print word[first], word[arrow + 1]
		}
	' "$1"
}
