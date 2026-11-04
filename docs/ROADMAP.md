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
| Versioned releases, MIT license | #15, #16 |
| Agent workflow: `CLAUDE.md`, roadmap, skills, `scripts/check.sh`, Dependabot | #17 |

## Open

### 1. Publish v0.1.0 (blocked: Mic)

Needs #16 merged, then Mic publishes the release in the GitHub UI (steps in `docs/guide/development.md`). The session cannot push tags.
Done when: the `release` workflow is green, the release has 5 archives and `checksums.txt`, and `go install github.com/stmic168/arch-drift/cmd/archdrift@v0.1.0` prints `archdrift v0.1.0`.

### 2. Pilot on one of Mic's work repositories (blocked: repository access)

The most important open test: nobody but the author has used the tool on a real project. Mic runs `archdrift init --mode gate` and writes 3–5 rules, or attaches the repository to a session.
Done when: lock reviewed, `check` run, every failing finding graded, false-positive rate recorded here.

### 3. Deterministic Mermaid diagrams in the doc (next)

Read `flowchart`/`graph` blocks in the doc with Go code (nodes, labelled edges, `-.->`/`-->`), so structured parts of a doc need no LLM and give the same model every time. The LLM still maps names to code IDs and reads prose. Quotes are the Mermaid source lines, so the verifier keeps working.
Done when: the fixture's design doc diagram yields the same components and relations with and without the API; a doc that is only a Mermaid diagram plus a name mapping can be checked with no API call; offline eval does not get worse.

### 4. Pull request comment from the action (todo)

The action writes the report to the job summary only. Post (and update in place) one PR comment with failing findings and their status (new / worsened), when `comment: true`.
Done when: the self-test workflow shows one comment that updates on a second push instead of adding another.

### 5. Runtime relations from `.proto` files (todo)

Many `UNVERIFIABLE` relations are gRPC calls. Parse `service` definitions in `.proto` files (a small hand-written parser, no dependency) and map generated clients to the serving component, so "A calls B over gRPC" becomes checkable.
Done when: on the Thanos or Cortex pilot, the share of `UNVERIFIABLE` relations drops, with no new false positives in the offline eval.

### 6. Claude GitHub Action for issues (later)

Mic opens an issue and mentions `@claude`, and the action works from `CLAUDE.md` and opens a pull request. Restricted to Mic. Uses the `ANTHROPIC_API_KEY` secret, so each run costs tokens. Do it once `CLAUDE.md` has been stable through a few pull requests.

### Later (only when pilots ask for it)

- Other languages behind a `CodeModel` interface (Java/Kotlin, TypeScript, Python).
- docker-compose and Kubernetes manifests (needs YAML, which conflicts with the single-dependency rule).
- Several repositories against one system-level doc.
- Drift over time, per-component owners, periodic reports for an architecture board.
