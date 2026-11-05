#!/usr/bin/env bash
# Compare two archdrift binaries on the offline eval, for the weekly release
# gate (docs/release-plan.md): every lock set, finding by finding, plus the
# Cortex gate pilot. No API call. Exit 1 if the new binary loses a true
# positive, adds an ungraded failing finding, or misses a gate violation.
#
# usage: scripts/eval-compare.sh <old binary> <new binary> <work dir>
# The work dir keeps the clones between runs (first run clones the pilots).
set -uo pipefail
cd "$(dirname "$0")/.."
old=$(realpath "$1") new=$(realpath "$2") work=$(mkdir -p "$3" && realpath "$3")
fail=0

for set in locks locks-p06 locks-p07; do
  echo "== $set"
  for side in old new; do
    bin=$old; [ "$side" = new ] && bin=$new
    LOCKS=pilot/eval/$set pilot/eval/run.sh "$bin" "$work/$side-$set" > "$work/$side-$set.txt" 2>&1
  done
  for side in old new; do
    printf '  %-4s %s\n' "$side" "$(grep '^TOTAL' "$work/$side-$set.txt" | tr -s ' ')"
  done
  printf '  report-mode failing per case: old %s| new %s\n' \
    "$(grep 'fail=false' "$work/old-$set.txt" | grep -o 'failing [0-9]*' | cut -d' ' -f2 | tr '\n' ' ')" \
    "$(grep 'fail=false' "$work/new-$set.txt" | grep -o 'failing [0-9]*' | cut -d' ' -f2 | tr '\n' ' ')"
  # A case without a lock in this set (added after it was recorded) is
  # skipped; a case with a lock and no report is a failure.
  for c in $(grep 'no report' "$work/new-$set.txt" | awk '{print $1}'); do
    if [ -f "pilot/eval/$set/$c.lock.json" ]; then
      echo "  new $c: NO REPORT (see $work/new-$set/$c/stdout.txt)"
      fail=1
    else
      echo "  $c: skipped (no lock in $set)"
    fi
  done
  if grep -q UNGRADED "$work/new-$set.txt"; then
    grep UNGRADED "$work/new-$set.txt" | sed 's/^ */  new /'
    fail=1
  fi
  for r in "$work"/new-"$set"/*/out/report.json; do
    c=$(basename "$(dirname "$(dirname "$r")")")
    o="$work/old-$set/$c/out/report.json"
    [ -f "$o" ] || continue
    diff <(jq -r '.findings[] | "\(.type)|\(.subject)|\(.failing)"' "$o" | sort) \
         <(jq -r '.findings[] | "\(.type)|\(.subject)|\(.failing)"' "$r" | sort) |
      grep '^[<>]' | sed "s/^</  $c removed:/; s/^>/  $c added:  /"
    # A true positive must still fail.
    for id in $(jq -r --arg c "$c" '.[$c] // {} | to_entries[] | select(.value == "TP") | .key' pilot/eval/labels.json); do
      if jq -e --arg id "$id" '[.findings[] | select(.id == $id and .failing)] | length == 1' "$o" > /dev/null &&
         ! jq -e --arg id "$id" '[.findings[] | select(.id == $id and .failing)] | length == 1' "$r" > /dev/null; then
        echo "  $c LOST TRUE POSITIVE: $id"
        fail=1
      fi
    done
  done
done

echo "== Cortex gate pilot (pilot/docs/cortex-rules.md, want 5/5)"
clone="$work/new-locks/cortex/target"
want=$(jq -r '.failing[] | "\(.type) \(.subject)"' pilot/docs/cortex-rules.expected.json | sort)
for run in run1 run2; do
  out="$work/gate-$run"
  mkdir -p "$out"
  ln -sfn "$(realpath "$clone")" "$out/target"
  ln -sfn "$(pwd)/pilot" "$out/pilot"
  (cd "$out" && "$new" check --repo target --doc pilot/docs/cortex-rules.md --config pilot/cortex-rules.json \
     --lock pilot/eval/locks-gate/$run.lock.json --out out > stdout.txt 2>&1)
  got=$(jq -r '.findings[] | select(.failing) | "\(.type) \(.subject)"' "$out/out/report.json" 2>/dev/null | sort)
  if [ "$got" = "$want" ]; then echo "  $run: 5/5"; else echo "  $run: MISMATCH"; diff <(echo "$want") <(echo "$got") | sed 's/^/    /'; fail=1; fi
done

[ "$fail" -eq 0 ] && echo "eval-compare: OK" || echo "eval-compare: FAILED"
exit "$fail"
