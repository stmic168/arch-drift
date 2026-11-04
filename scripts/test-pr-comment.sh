#!/usr/bin/env bash
# Tests scripts/pr-comment.sh against a fake gh that keeps comments in a
# directory: one comment per key, updated in place, nothing posted without
# drift, other comments untouched, and API errors only warn.
set -euo pipefail
cd "$(dirname "$0")/.."
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
mkdir -p "$t/bin" "$t/comments"
cat > "$t/bin/gh" <<'GH'
#!/usr/bin/env bash
# fake: gh api [--paginate] [-X METHOD] PATH [--jq EXPR] [-F body=@FILE]
dir="$FAKE_GH"; [ -f "$dir/fail" ] && exit 1
method=GET path= jq= file=
shift # api
while [ $# -gt 0 ]; do
  case "$1" in
    --paginate) ;;
    -X) method=$2; shift ;;
    --jq) jq=$2; shift ;;
    -F) file=${2#body=@}; shift ;;
    *) path=$1 ;;
  esac
  shift
done
case "$method $path" in
  "GET "*/comments)
    for f in "$dir"/comments/*; do [ -f "$f" ] && jq -n --arg b "$(cat "$f")" --argjson id "$(basename "$f")" '{id:$id, body:$b}'; done | jq -s . | jq -r "$jq" ;;
  "POST "*/comments)
    n=$(( $(ls "$dir/comments" | sort -n | tail -n 1) + 1 )); cp "$file" "$dir/comments/$n"; echo "{}" ;;
  "PATCH "*/comments/*)
    cp "$file" "$dir/comments/${path##*/}"; echo "{}" ;;
esac
GH
chmod +x "$t/bin/gh"
export PATH="$t/bin:$PATH" FAKE_GH="$t" REPO=o/r PR=7 KEY=archdrift-out GH_TOKEN=x
fail() { echo "FAIL: $1"; exit 1; }
count() { ls "$t/comments" | wc -l | tr -d ' '; }
mk() { # mk <dir> <failing>
  mkdir -p "$t/$1"; echo "{\"summary\":{\"failing\":$2}}" > "$t/$1/report.json"
  echo "### archdrift: $2 failing" > "$t/$1/comment.md"
}
mk clean 0; mk drift 2; mk drift3 3
echo "someone else's comment" > "$t/comments/100"

OUT="$t/clean" scripts/pr-comment.sh | grep -q "nothing posted" || fail "posted without drift"
[ "$(count)" = 1 ] || fail "comment count after a clean run"
OUT="$t/drift" scripts/pr-comment.sh | grep -q posted || fail "no comment on drift"
[ "$(count)" = 2 ] || fail "comment count after drift"
OUT="$t/drift3" scripts/pr-comment.sh | grep -q "updated 101" || fail "second push did not update"
[ "$(count)" = 2 ] || fail "second push added a comment"
grep -q "3 failing" "$t/comments/101" || fail "update lost the new body"
head -n 1 "$t/comments/101" | grep -qx "<!-- archdrift-comment: archdrift-out -->" || fail "marker missing"
OUT="$t/clean" scripts/pr-comment.sh | grep -q "updated 101" || fail "clean run did not update the existing comment"
grep -q "0 failing" "$t/comments/101" || fail "comment not updated to clean"
KEY=other OUT="$t/drift" scripts/pr-comment.sh | grep -q posted || fail "a second key did not get its own comment"
[ "$(count)" = 3 ] || fail "comment count with two keys"
[ "$(cat "$t/comments/100")" = "someone else's comment" ] || fail "touched another comment"
touch "$t/fail"
OUT="$t/drift" scripts/pr-comment.sh | grep -q "::warning::" || fail "API error did not warn"
echo "pr-comment: all cases passed"
