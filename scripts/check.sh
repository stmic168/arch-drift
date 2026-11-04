#!/usr/bin/env bash
# Fast local checks, the same ones CI runs on a pull request. Run before every push.
# usage: scripts/check.sh            (from the repository root)
set -uo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0
step() { printf '== %s\n' "$1"; }
bad() { printf 'FAIL: %s\n' "$1"; fail=1; }

step "gofmt"
unformatted=$(gofmt -l cmd internal)
[ -z "$unformatted" ] || bad "gofmt needed: $unformatted"

step "go vet"
go vet ./... || bad "go vet"

step "go test"
go test -count=1 ./... || bad "go test"

step "build"
go build -o "$tmp/archdrift" ./cmd/archdrift || { bad "build"; exit 1; }

expect() { # expect <exit code> <description> <args...>
  local want=$1 what=$2; shift 2
  "$tmp/archdrift" "$@" > "$tmp/out.txt" 2>&1
  local got=$?
  [ "$got" -eq "$want" ] || { cat "$tmp/out.txt"; bad "$what: exit $got, want $want"; }
}

step "fixture with the recorded doc model (drift expected)"
expect 1 "fixture --doc-model" check --repo testdata/shop --doc testdata/shop/design.md \
  --doc-model testdata/shop/docmodel.json --out "$tmp/fixture"

step "fixture with the committed lock (drift expected)"
expect 1 "fixture lock" check --repo testdata/shop --doc testdata/shop/design.md --out "$tmp/lock"

step "dogfood: this repository against ADR-001 (no drift expected)"
expect 0 "dogfood" check --repo . --doc docs/architecture-rules.md --out "$tmp/dogfood"

step "README architecture diagram, read by code (no drift expected)"
expect 0 "README diagram" check --repo . --doc README.md --no-llm --config archdrift.readme.json --out "$tmp/readme"

if [ "$fail" -ne 0 ]; then echo "checks FAILED"; exit 1; fi
echo "all checks passed"
