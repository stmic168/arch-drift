# Changelog

Releases are published by `.github/workflows/release.yml` when a tag `vX.Y.Z` on `main` is pushed. Each tag needs a section here; its text becomes the release notes.

## Unreleased

## v0.2.0 (2026-11-04)

Measured on six public pilots (offline eval in `pilot/eval/`, locks from live runs with the current prompt): in report mode, 0 false positives on Thanos (two docs), Cortex, Temporal and the held-out Loki; on the held-out Istio doc, 9 false positives remain (doc parts finer than the code components; see the roadmap). The Cortex rules pilot (gate mode) finds all 5 planted violations.

- **Relation inside one component**: a required relation between two doc components that map to the same code component is `UNVERIFIABLE` (imports inside a component are not modeled) instead of `MISSING_EDGE`.
- **Dependency diagrams**: Mermaid blocks marked `%% archdrift: dependencies` are read by Go code (arrow = depends on, `must not` label = forbidden, `Name<br/>code/id` labels). `--no-llm` builds the doc model from them alone, with no API call; otherwise they are merged into the LLM model. The as-built diagram carries the marker and code IDs, so it can be pasted into the doc and read back.
- **Wired by the binary**: a configured service's required external system counts as present when one file of a deployable imports both (Thanos' `cmd/thanos/rule.go` builds the bucket for the rule service). Report-mode false positives on the pilots with current locks: 2 -> 0; Cortex gate pilot 5/5.
- **`services`** in `archdrift.json`: components that run as separate processes inside one binary (Thanos, Cortex). A required relation between two services without an import is `UNVERIFIABLE` instead of `MISSING_EDGE`; ignored with `strict_protocols`. On the pilots, report-mode false positives go from 4 to 1 and the Cortex gate pilot stays 5/5.
- **Pull request comment**: the action's `comment: "true"` posts one comment with the failing findings, their status and first evidence, and updates it on later pushes (`scripts/pr-comment.sh`). `check` writes it as `comment.md`. `init` turns it on.
- README: a pipeline diagram, and archdrift's architecture as a dependency diagram that CI checks against the code with `--no-llm` (`archdrift.readme.json`).
- Development: `CLAUDE.md`, `docs/ROADMAP.md`, project skills (`/ship`, `/next`, `/pilot`), `scripts/check.sh`, Dependabot.

## v0.1.0 (first release)

First tagged release. Everything below landed through pull requests #1 to #14.

- **check**: compares a Go repository with a Markdown design doc. Findings: `FORBIDDEN_EDGE`, `MISSING_COMPONENT`, `MISSING_EDGE`, `UNDOCUMENTED_EDGE`, `UNDOCUMENTED_COMPONENT`, `UNVERIFIABLE`, each with `file:line` and/or a verbatim doc quote. Output: `report.md` (with suggested doc updates and an as-built Mermaid diagram), `report.json`, `report.sarif`.
- **Doc model**: one Claude API call per doc version (doc text and component IDs only, never source code), verified deterministically: claims with a quote not in the doc or an unknown code ID are dropped and listed.
- **lock**: commits the reviewed mapping so `check` is deterministic and makes no API call; re-locking after a doc edit merges with the reviewed lock and prints only the changes.
- **baseline**: accepts known drift with a reason and expiry.
- **init**: proposes `archdrift.json`, writes a GitHub workflow and, in gate mode, an ADR template. No API call.
- **scan**: prints the code model.
- **Configuration**: custom components, external IDs, excludes, shared and protocol packages, transitive rules, `strict_protocols`, report-only undocumented edges.
- **CI**: composite GitHub Action with `--base` diff mode (only new or worsened drift fails) and SARIF upload.
- **License**: MIT.
- **version**: `archdrift version`; `init` pins the generated workflow to the release it was run from.
