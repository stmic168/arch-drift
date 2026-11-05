# Roadmap

Goal: a tool architects run on CI every day, which they trust because each finding has `file:line` and doc-quote evidence and false positives are rare.

How to use this file: items are in priority order. Status is `done`, `next` (the one to do now), `todo`, `blocked` (with the reason) or `later`. "Do the next step" takes the `next` item; when it is done, mark it `done` with its PR and promote the following item. Each item states when it counts as done.

Success measures: false-positive rate under 10% on pilot repositories; under a minute to act on a finding; fewer `UNVERIFIABLE` relations over time.

Dates: [`release-plan.md`](release-plan.md) puts these items into weekly releases up to v1.0.0.

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
| Live re-run of the 4 pilots (current prompt, pinned commits): external actors stable; `pilot` accepts commit SHAs | #22 |
| Relations wired by the binary: report mode 0 false positives on all 4 pilots | #23 |
| Held-out pilots (Loki 0 FP, Istio 9 FP); relation inside one component is UNVERIFIABLE; v0.2.0 | #24 |
| Release plan to v1.0.0 | #25 |
| Docs finer than the code components: named code suggested, Istio report-mode FP 9 -> 1; `scripts/eval-compare.sh` (v0.3.0) | this PR |

## Open

### 1. Pilot on one of Mic's work repositories (blocked: repository access)

The most important open test: nobody but the author has used the tool on a real project. Mic runs `archdrift init --mode gate` and writes 3–5 rules, or attaches the repository to a session.
Done when: lock reviewed, `check` run, every failing finding graded, false-positive rate recorded here.

### 2. `init` that fits the repository (next, v0.4.0)

Every pilot needed a hand-written config: `services` for Thanos, Cortex and Loki, and a split of `pilot/pkg` for Istio. `init` should propose `services` when one deployable has a file per service (`cmd/thanos/rule.go` next to `pkg/rules`) or a module list, and split a merged component whose children the doc names (plain text, no LLM).
Done when: on Thanos, Cortex and Loki, `init` proposes at least 80% of the services in the hand-written pilot configs and no component that is not a service; on Istio it splits `pilot/pkg`.

### 3. Claude GitHub Action for issues (later)

Mic opens an issue and mentions `@claude`, and the action works from `CLAUDE.md` and opens a pull request. Restricted to Mic. Uses the `ANTHROPIC_API_KEY` secret, so each run costs tokens. Do it once `CLAUDE.md` has been stable through a few pull requests.

### Later (only when pilots ask for it)

- gRPC relations from `.proto` files. Measured before building: none of the `UNVERIFIABLE` relations in the 4 pilots is a gRPC call between two internal components (9 of 10 involve an external system, 1 is HTTP), because generated-client imports already prove them as protocol edges. Worth it only for a repository whose gRPC clients live outside the serving component.
- Other languages behind a `CodeModel` interface (Java/Kotlin, TypeScript, Python).
- docker-compose and Kubernetes manifests (needs YAML, which conflicts with the single-dependency rule).
- Several repositories against one system-level doc.
- Drift over time, per-component owners, periodic reports for an architecture board.
