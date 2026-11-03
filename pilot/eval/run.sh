#!/usr/bin/env bash
# Replay the pilot offline: run `archdrift check` against the locks recorded
# by the pilot workflow (no API call) and score failing findings against the
# hand-graded labels in labels.json.
#
# usage: pilot/eval/run.sh <archdrift binary> <work dir>
# The work dir holds shallow clones at the pinned commits and the reports.
set -euo pipefail

bin=$(realpath "$1")
work=$(mkdir -p "$2" && realpath "$2")
here=$(cd "$(dirname "$0")" && pwd)

# case | repo | commit | config (relative to pilot/, or -) | docs (comma-separated)
cases=(
  "thanos-design|thanos-io/thanos|176ba656c70f426291995db8097a262b2e80124c|-|docs/design.md"
  "thanos-multi|thanos-io/thanos|176ba656c70f426291995db8097a262b2e80124c|-|docs/design.md,docs/components/receive.md,docs/components/query-frontend.md,docs/components/query.md,docs/components/store.md"
  "cortex|cortexproject/cortex|a55f3193543cc386f2b32de3a17f24f9d86973ef|-|docs/architecture.md"
  "temporal|temporalio/temporal|8fcaa51f023112a34f1180d8ea679a4912eacca4|temporal.json|docs/architecture/README.md"
)

for c in "${cases[@]}"; do
  IFS='|' read -r name repo commit config docs <<< "$c"
  dir="$work/$name"
  mkdir -p "$dir"
  clone="$work/clones/${repo//\//_}"
  if [ ! -d "$clone" ]; then
    git init -q "$clone"
    git -C "$clone" fetch -q --depth 1 "https://github.com/$repo" "$commit"
    git -C "$clone" checkout -q FETCH_HEAD
  fi
  # The pilot ran with the clone at ./target; doc paths are part of the lock hash.
  ln -sfn "$clone" "$dir/target"
  args=(check --repo target --lock "$here/locks/$name.lock.json" --out out)
  IFS=',' read -ra ds <<< "$docs"
  for d in "${ds[@]}"; do args+=(--doc "target/$d"); done
  [ "$config" != "-" ] && args+=(--config "$here/../$config")
  (cd "$dir" && "$bin" "${args[@]}" > stdout.txt 2>&1) || true
done

python3 -I "$here/score.py" "$work" "$here/labels.json" "${cases[@]%%|*}"
