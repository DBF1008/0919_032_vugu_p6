#!/usr/bin/env bash
#
# test.sh - manual unit-test runner for the compactNodeTree rework
#
# Run from the repository root:
#
#   ./test.sh            # run the targeted unit tests (parser-compact + parser-go)
#   ./test.sh -v         # verbose output
#   ./test.sh --all      # run the whole gen package test suite
#   ./test.sh --pkg <p>  # run an arbitrary package (repeatable / any go test flags
#                        # after "--" are passed straight through, e.g.
#                        ./test.sh -- -run TestCompactNodeTreeVGComp -v)
#
# The script intentionally does not start browsers, wasm runtimes or code
# generation against external modules - everything here is plain
# "go test" so it can be run by hand in an offline environment.
set -euo pipefail

cd "$(dirname "$0")"

GO_BIN="${GO:-go}"

# targeted tests that cover the three fixed defects:
#   1. static fragments nested inside non-compactable content are still compacted
#   2. component elements (vg-comp / pkg:Comp, original casing) are never compacted
#   3. vg-html compaction cleans residual dynamic directive attributes
TARGETED_RUN='TestCompactNodeTree|TestIsComponentElement|TestIsStaticEl|TestEmitForExpr'

VERBOSE=0
ALL=0
PKGS=()
PASSTHROUGH=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	-v | --verbose)
		VERBOSE=1
		shift
		;;
	--all)
		ALL=1
		shift
		;;
	--pkg)
		PKGS+=("$2")
		shift 2
		;;
	-h | --help)
		sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	--)
		shift
		PASSTHROUGH=("$@")
		break
		;;
	*)
		echo "unknown argument: $1 (see --help)" >&2
		exit 2
		;;
	esac
done

echo "==> Go version"
"$GO_BIN" version

if [[ "$ALL" -eq 1 ]]; then
	echo "==> Running full gen package test suite"
	exec "$GO_BIN" test ./gen/ -v "${PASSTHROUGH[@]}"
fi

if [[ ${#PKGS[@]} -gt 0 ]]; then
	for pkg in "${PKGS[@]}"; do
		echo "==> Testing $pkg"
		"$GO_BIN" test "$pkg" "${PASSTHROUGH[@]}"
	done
	exit 0
fi

FLAGS=()
if [[ "$VERBOSE" -eq 1 ]]; then
	FLAGS+=(-v)
fi
if [[ ${#PASSTHROUGH[@]} -gt 0 ]]; then
	FLAGS+=("${PASSTHROUGH[@]}")
else
	FLAGS+=(-run "$TARGETED_RUN")
fi

echo "==> Running compactNodeTree/parser-go unit tests"
"$GO_BIN" test ./gen/ "${FLAGS[@]}"

echo "==> go vet ./gen/"
"$GO_BIN" vet ./gen/

echo
echo "All targeted unit tests passed."
echo "Tip: ./test.sh -v          for per-test output"
echo "     ./test.sh --all       for the complete gen package suite"
echo "     ./test.sh -- -run X   to pass flags straight to 'go test'"
