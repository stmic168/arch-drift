# ADR-001: archdrift component boundaries

Status: accepted

archdrift's core principle is that the LLM only reads the design doc and maps
names; code facts and drift conclusions come from deterministic code. These
rules keep that principle in the package structure. archdrift checks them on
every pull request to this repository (`.github/workflows/archdrift.yml`).

## Components

- **Command line** (`cmd/archdrift`) parses flags and runs the pipeline.
- **Code model** (`internal/codemodel`) scans Go imports with `go/parser`.
- **Doc model** (`internal/docmodel`) extracts, verifies, caches and locks the doc model.
- **Drift engine** (`internal/drift`) compares the code model with the doc model.
- **Report** (`internal/report`) renders Markdown, JSON and SARIF.
- **Config** (`internal/config`) loads `archdrift.json`.
- **Anthropic API** is the external service the Doc model calls through the official SDK.

## Rules that must hold

1. The Command line runs the Code model, the Doc model, the Drift engine and the Report.
2. The Command line loads options through the Config.
3. The Doc model calls the Anthropic API.
4. The Drift engine compares the Code model with the Doc model.
5. The Report renders the findings of the Drift engine.
6. The Config builds options for the Code model and the Drift engine.

## Prohibited dependencies

7. The Code model must not depend on the Doc model: code facts never come from the LLM.
8. The Code model must not depend on the Anthropic API.
9. The Drift engine must not depend on the Anthropic API: drift conclusions are deterministic.
10. The Report must not depend on the Anthropic API.
11. The Config must not depend on the Anthropic API.
12. The Code model must not depend on the Drift engine.
13. The Doc model must not depend on the Drift engine.
14. The Doc model must not depend on the Report.
15. The Drift engine must not depend on the Report.
16. The Report must not depend on the Command line.
