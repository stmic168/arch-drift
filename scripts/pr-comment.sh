#!/usr/bin/env bash
# Post archdrift's comment.md on a pull request, or update the comment it
# posted before, so a pull request has one archdrift comment however often it
# is pushed. With no failing finding and no earlier comment, nothing is posted.
# Never fails the job: a token without write access (e.g. a fork's pull
# request) only gives a warning.
#
# env: REPO (owner/name), PR (number), OUT (archdrift output dir), KEY (tells
#      several archdrift steps on one pull request apart), GH_TOKEN.
set -uo pipefail

warn() { echo "::warning::archdrift comment: $1"; exit 0; }

body="$OUT/comment.md"
[ -f "$body" ] || warn "$body not found"
failing=$(jq -r '.summary.failing' "$OUT/report.json") || warn "cannot read $OUT/report.json"
marker="<!-- archdrift-comment: $KEY -->"

ids=$(gh api --paginate "repos/$REPO/issues/$PR/comments" \
  --jq ".[] | select(.body | startswith(\"$marker\")) | .id") || warn "cannot list comments (token needs pull-requests: write)"
id=$(printf '%s\n' "$ids" | head -n 1)

if [ -z "$id" ] && [ "$failing" = "0" ]; then
  echo "archdrift comment: no failing finding and no earlier comment; nothing posted"
  exit 0
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
{ printf '%s\n' "$marker"; cat "$body"; } > "$tmp"

if [ -n "$id" ]; then
  gh api -X PATCH "repos/$REPO/issues/comments/$id" -F "body=@$tmp" > /dev/null || warn "cannot update comment $id"
  echo "archdrift comment: updated $id"
else
  gh api -X POST "repos/$REPO/issues/$PR/comments" -F "body=@$tmp" > /dev/null || warn "cannot post a comment (token needs pull-requests: write)"
  echo "archdrift comment: posted"
fi
