# Changelog

Releases are published by `.github/workflows/release.yml` when a tag `vX.Y.Z` on `main` is pushed. Each tag needs a section here; its text becomes the release notes.

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
- **version**: `archdrift version`; `init` pins the generated workflow to the release it was run from.
