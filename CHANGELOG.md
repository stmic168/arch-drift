# Changelog

Releases are published by `.github/workflows/release.yml` when a tag `vX.Y.Z` on `main` is pushed. Each tag needs a section here; its text becomes the release notes when the release description is left empty (do not use "Generate release notes").

## Unreleased

Measured on the pilots at their pinned commits (no API call): `init` alone now produces the configs the pilots needed by hand. Replaying every eval lock set with the `init`-generated configs instead of the hand-written ones gives the same findings, finding by finding. `scripts/eval-compare.sh` against v0.3.0: no change on any lock set; Cortex gate pilot 5/5.

- **`init` proposes `services`** from a file per service in a binary (`cmd/thanos/rule.go` imports `pkg/rules`) or a dskit module list (`RegisterModule` calls; helper modules skipped). Thanos 6 of 6, Cortex 13 of 13, Loki 12 of 12 of the hand-written services; also four runnable targets the hand-written configs left out (Cortex `pkg/flusher`; Loki `pkg/limits`, `pkg/logline`, `pkg/labelaccess`), so the roadmap's "no component that is not a service" holds only if those count as services. Temporal: none proposed.
- **`init` splits a package bucket the doc describes**: `<top>/pkg` becomes one component per child when the doc's headings, code spans or bold text name code in at least 3 of its children and in at least a quarter of them. Istio: `pilot/pkg` is split (6 of 20 children named) and the generated config equals the hand-written `pilot/istio.json`; `istioctl/pkg` (4 of 30) is not.
- Configuration guide: "Choosing services".

## v0.3.0 (2026-11-05)

Measured with `scripts/eval-compare.sh` against v0.2.0 on the offline eval (locks from live runs with the current prompt): report-mode false positives are 0 on Thanos (two docs), Cortex, Temporal and Loki, and 1 on the held-out Istio doc (was 9). No true positive lost; the Cortex rules pilot (gate mode) still finds all 5 planted violations.

- **Docs finer than the code components**: a doc component the doc model maps to no code ID is no longer a failing `MISSING_COMPONENT` when a package or exported type below a component has its name ("CRD Client" -> `pilot/pkg/config/kube/crdclient` inside `pilot/config`). It is info, names the code with its `file:line`, and the suggested doc updates say to map it in the lock. On the held-out Istio pilot, report-mode false positives go from 9 to 1; the other cases stay at 0, the gate pilot 5/5.
- The scanner now reads exported type names; a file whose body uses syntax newer than the toolchain (a generic method in Istio) falls back to an imports-only parse, as before.
- Development: `scripts/eval-compare.sh <old> <new> <work>` compares two binaries on every eval lock set, finding by finding, plus the gate pilot, and fails on a lost true positive, a missing report or a gate mismatch.

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
