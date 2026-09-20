#!/usr/bin/env bash
#
# test.sh - unit test script for the vugu repository, focused on the static
# HTML optimization (compactNodeTree) in the gen package.
#
# Usage:
#   ./test.sh                  run all unit tests (offline-safe)
#   ./test.sh --with-network   also run integration tests that need network
#                              (TestRun / TestMissingFixer run `go mod tidy`)
#
# Environment overrides:
#   VUGU_HTML_DIR    local copy of github.com/vugu/html   (default: auto-detect)
#   VUGU_XXHASH_DIR  local copy of github.com/vugu/xxhash (default: auto-detect)
#
set -euo pipefail
cd "$(dirname "$0")"

WITH_NETWORK=0
[ "${1:-}" = "--with-network" ] && WITH_NETWORK=1

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
warn() { printf '\033[33mWARN\033[0m %s\n' "$*"; }
die()  { printf '\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. Writable Go build environment (the sandbox may make $HOME read-only)
# ---------------------------------------------------------------------------
WORK="${TMPDIR:-/tmp}/vugu-test-work"
mkdir -p "$WORK/gocache" "$WORK/gotmp"
export GOCACHE="$WORK/gocache"
export GOTMPDIR="$WORK/gotmp"

# ---------------------------------------------------------------------------
# 2. Dependency wiring
#
# github.com/vugu/html and github.com/vugu/xxhash may not be downloadable
# (no network).  If local copies are available, generate a go.mod with
# replace directives and use it via -modfile.  The file is made read-only
# so the go tool cannot rewrite it.
# ---------------------------------------------------------------------------
for d in /private/tmp/vugudeps2/html /private/tmp/vugudeps/html; do
	if [ -z "${VUGU_HTML_DIR:-}" ] && [ -f "$d/go.mod" ] && grep -q OrigData "$d/node.go" 2>/dev/null; then
		VUGU_HTML_DIR="$d"
	fi
done
for d in /private/tmp/vugudeps/xxhash /private/tmp/vugudeps2/xxhash; do
	if [ -z "${VUGU_XXHASH_DIR:-}" ] && [ -f "$d/go.mod" ]; then
		VUGU_XXHASH_DIR="$d"
	fi
done

MODFILE="$WORK/vugu_test.go.mod"
if [ -n "${VUGU_HTML_DIR:-}" ] && [ -n "${VUGU_XXHASH_DIR:-}" ]; then
	say "Using local dependency copies"
	echo "  github.com/vugu/html   => $VUGU_HTML_DIR"
	echo "  github.com/vugu/xxhash => $VUGU_XXHASH_DIR"
	[ -f "$MODFILE" ] && chmod 644 "$MODFILE"
	cat > "$MODFILE" <<EOF
module github.com/vugu/vugu

go 1.22.3

require (
	github.com/stretchr/testify v1.10.0
	github.com/vugu/html v0.0.0-20190914200101-c62dc20b8289
	github.com/vugu/xxhash v0.0.0-20191111030615-ed24d0179019
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/vugu/html => $VUGU_HTML_DIR

replace github.com/vugu/xxhash => $VUGU_XXHASH_DIR
EOF
	chmod 444 "$MODFILE"
	export GOFLAGS="-mod=mod -modfile=$MODFILE"
	export GOPROXY=off
else
	warn "local copies of vugu/html and vugu/xxhash not found;"
	warn "falling back to the repository go.mod (needs network or a populated module cache)"
fi

# If the default module cache is not writable and a probe fails, sync a
# writable copy once and point GOMODCACHE at it.
if ! go test ./gen/ -count=1 -run 'TestIsStaticEl' >/dev/null 2>&1; then
	DEFAULT_GOMODCACHE="$(go env GOMODCACHE)"
	if [ ! -w "$DEFAULT_GOMODCACHE" ]; then
		say "Module cache is read-only, syncing a writable copy (one-time)"
		export GOMODCACHE="$WORK/gomod"
		if [ ! -d "$GOMODCACHE/cache/download" ]; then
			mkdir -p "$GOMODCACHE"
			cp -R "$DEFAULT_GOMODCACHE/cache" "$GOMODCACHE/" 2>/dev/null || true
		fi
	fi
fi

# ---------------------------------------------------------------------------
# 3. Unit tests
# ---------------------------------------------------------------------------
say "go vet ./gen/"
go vet ./gen/ || die "go vet ./gen/"
pass "go vet ./gen/"

say "gen package unit tests (compactNodeTree / parser-go)"
# TestRun and TestMissingFixer execute `go mod tidy` against the network and
# are skipped here; run with --with-network to include them.
go test ./gen/ -count=1 -v -skip 'TestRun|TestMissingFixer' || die "gen package unit tests"
pass "gen package unit tests"

say "root package unit tests"
go test . -count=1 || die "root package unit tests"
pass "root package unit tests"

# ---------------------------------------------------------------------------
# 4. Optional: integration tests that require network access
# ---------------------------------------------------------------------------
if [ "$WITH_NETWORK" = "1" ]; then
	say "integration tests (require network: go mod tidy)"
	# run without the offline overrides so the inner `go` commands behave normally
	env -u GOFLAGS -u GOPROXY -u GOMODCACHE \
		go test ./gen/ -count=1 -v -run 'TestRun|TestMissingFixer' \
		|| die "integration tests (TestRun / TestMissingFixer)"
	pass "integration tests"
else
	warn "skipping TestRun / TestMissingFixer (need network; use --with-network)"
fi

say "ALL TESTS PASSED"
