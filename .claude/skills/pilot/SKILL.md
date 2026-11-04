---
name: pilot
description: Run archdrift on a public Go repository through the pilot workflow (live API, costs tokens), save the printed lock, replay it offline and grade the failing findings. Use when Mic asks to pilot, trial or test archdrift on a public repo, or to re-measure after a rule change.
---

# Pilot on a public repository

Only public repositories, only through `.github/workflows/pilot.yml`, and only with Mic's approval: each run makes one API call (about $0.02 to $0.13 per doc so far).

1. **Inputs.** `repo` (owner/name), optional `ref` (pin a commit for reproducibility), `doc` (path(s) in that repo), optional `local_docs` (`pilot/docs/<name>.md`, for a rules ADR written for the pilot), optional `config` (`pilot/<name>.json`, needed when the layout is not `cmd/` / `internal/` / `pkg/`; run `archdrift init` on a local shallow clone to propose one), `model`.
2. **Run.** Trigger with GitHub MCP `actions_run_trigger` (workflow `pilot.yml`, ref `main` or the branch under test). Wait for completion with a background loop, not repeated manual checks.
3. **Collect.** From the job log (`get_job_logs`): the doc model review, findings, cost, and the lock JSON. Save the lock under `pilot/eval/locks*/<case>.lock.json`.
4. **Replay offline.** Add the case to `pilot/eval/run.sh` (repo, pinned commit, config, docs), then `go build -o <tmp>/archdrift ./cmd/archdrift && pilot/eval/run.sh <tmp>/archdrift <tmp>/eval`.
5. **Grade.** Every `UNGRADED` failing finding gets a label in `pilot/eval/labels.json`: `TP`, `FP-mapping`, `FP-runtime`, `FP-actor`, `FP-config` or `NOISE` (definitions in `pilot/eval/README.md`). Grade from the code evidence and the doc quote, not from the finding's message.
6. **Report** to Mic: failing / TP / FP / noise counts, false-positive rate, the worst false positives with their cause, and cost. Compare with the previous run of the same case if there is one.
