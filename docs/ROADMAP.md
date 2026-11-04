# Roadmap

Goal: a tool architects run on CI every day, which they trust because each finding has `file:line` and doc-quote evidence and false positives are rare.

How to use this file: items are in priority order. Status is `done`, `next` (the one to do now), `todo`, `blocked` (with the reason) or `later`. "Do the next step" takes the `next` item; when it is done, mark it `done` with its PR and promote the following item. Each item states when it counts as done.

Success measures: false-positive rate under 10% on pilot repositories; under a minute to act on a finding; fewer `UNVERIFIABLE` relations over time.

## Done

| Item | PR |
|---|---|
| Core check: code model, doc model with verifier, six finding types, md/json report | #1, #2, #3 |
| Phase 0: lock file, `archdrift.json`, baseline | #4 |
| Phase 0.5 and 0.6: noise reduction, undocumented ranking, protocol edges, offline eval | #5, #6 |
| Gate mode (rules docs, `strict_protocols`), Cortex pilot 5/5 with no false positives | #7 |
| Phase 1: `--base` diff mode, SARIF, GitHub Action | #8, #9 |
| Phase 2: incremental re-lock, suggested doc updates, as-built diagram | #11 |
| `archdrift init` | #12 |
| Dogfood: ADR-001 checked on every PR | #13 |
| Docs split into `docs/guide/` | #14 |
| Versioned releases, MIT license; v0.1.0 published | #15, #16 |
| Agent workflow: `CLAUDE.md`, roadmap, skills, `scripts/check.sh`, Dependabot | #17 |
| Dependency diagrams read by code (`%% archdrift: dependencies`, `--no-llm`), README diagrams checked in CI | #18 |
| Pull request comment from the action | #19 |
| `services` config for single-binary services (report-mode FPs on pilots 4 -> 1) | #21 |
| Live re-run of the 4 pilots (current prompt, pinned commits): external actors stable; `pilot` accepts commit SHAs | this PR |

## Open

### 1. Pilot on one of Mic's work repositories (blocked: repository access)

The most important open test: nobody but the author has used the tool on a real project. Mic runs `archdrift init --mode gate` and writes 3–5 rules, or attaches the repository to a session.
Done when: lock reviewed, `check` run, every failing finding graded, false-positive rate recorded here.

### 2. Relations wired by the binary (todo)

The report-mode false positive left on the pilots, after a fresh run with the current prompt: Thanos' doc says Rule's "block files can be backed up to object storage". In the code, `cmd/thanos` (the composition root) passes a `pkg/shipper` uploader to the rule service, so `pkg/rules` never imports object storage and `MISSING_EDGE Rule -> Object Storage` fails in both Thanos cases. (The external-actor false positive this item first targeted is already fixed by the current prompt: Alert Component is external in both live runs.)
Options to weigh before building: let a required relation be satisfied when the deployable that imports the source component also imports a component that reaches the target; or map shared helpers such as `pkg/shipper` to every doc component that uses them. Either must not hide the Cortex gate pilot's two true `MISSING_EDGE` findings.
Done when: report mode on all 4 pilots has 0 false positives with the `locks-p07` locks, and the Cortex gate pilot stays 5/5.

### 3. Claude GitHub Action for issues (later)

Mic opens an issue and mentions `@claude`, and the action works from `CLAUDE.md` and opens a pull request. Restricted to Mic. Uses the `ANTHROPIC_API_KEY` secret, so each run costs tokens. Do it once `CLAUDE.md` has been stable through a few pull requests.

### Later (only when pilots ask for it)

- gRPC relations from `.proto` files. Measured before building: none of the `UNVERIFIABLE` relations in the 4 pilots is a gRPC call between two internal components (9 of 10 involve an external system, 1 is HTTP), because generated-client imports already prove them as protocol edges. Worth it only for a repository whose gRPC clients live outside the serving component.
- Other languages behind a `CodeModel` interface (Java/Kotlin, TypeScript, Python).
- docker-compose and Kubernetes manifests (needs YAML, which conflicts with the single-dependency rule).
- Several repositories against one system-level doc.
- Drift over time, per-component owners, periodic reports for an architecture board.
