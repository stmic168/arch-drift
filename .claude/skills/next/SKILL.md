---
name: next
description: Continue the arch-drift roadmap - check CI on main after a merge, take the next item from docs/ROADMAP.md, implement it, and update the roadmap. Use when Mic says merged, check CI and do the next step, do the next step, or làm bước tiếp theo.
---

# Do the next roadmap item

1. **CI on main.** `git fetch origin main`. List workflow runs for the head commit of `main` (GitHub MCP `actions_list`, `list_workflow_runs`). If any run is still going, wait for it to finish (a background loop with `gh api repos/stmic168/arch-drift/actions/runs?head_sha=<sha>`), not repeated manual checks. If one is red, fixing it is the next step: open a `fix/` branch.
2. **Roadmap bookkeeping.** In `docs/ROADMAP.md`, mark items whose PR has merged as `done` (with the PR number) if they are not already.
3. **Pick the item.** The `next` item, else the first `todo`. Skip `blocked` items, but mention them to Mic if Mic can unblock them (for example the release or repository access).
4. **Plan briefly.** Read the item's "Done when". If it needs a decision only Mic can make (cost, public effect, scope), ask one question before building. Otherwise go.
5. **Build** on a new branch from `origin/main`, following `CLAUDE.md`. Tests first where practical. Measure against "Done when" (offline eval in `pilot/eval/` for anything that changes findings).
6. **Update the roadmap.** Mark the item `done` ("this PR") and set the following item to `next`.
7. **Run `scripts/check.sh`, commit, push.** Do not open the PR unless Mic asked (then use `/ship`).
8. **Report**: CI result on `main`, what was built, how it measured against "Done when", what Mic should decide (usually: open the PR?).
