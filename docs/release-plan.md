# Release plan: November 2026 – January 2027

Eight releases, one every Thursday, from v0.3.0 (Nov 12) to v1.0.0 (Dec 31). Each release has one main theme, a "Done when" that is measured before the release goes out, and the same release gate. [`ROADMAP.md`](ROADMAP.md) stays the list of items and their status; this plan puts dates on them.

Starting point (v0.2.0, Nov 4): in report mode, 0 false positives on five of six public pilots; 9 on the held-out Istio doc (doc parts finer than the code components). The Cortex rules pilot (gate mode) finds all 5 planted violations. Nobody outside the project has used the tool yet.

## Calendar

| Release | Date (Thu) | Theme | Main measure |
|---|---|---|---|
| v0.3.0 | Nov 12 | Docs finer than the code components | Istio held-out: 9 → ≤ 1 report-mode FP |
| v0.4.0 | Nov 19 | `init` that fits the repository | `init` proposes the services and splits that pilots needed by hand |
| v0.5.0 | Nov 26 | First real project | one non-public repository checked, every failing finding graded |
| v0.6.0 | Dec 3 | Generalization | ≥ 9 pilot cases, report-mode FP ≤ 10% overall |
| v0.7.0 | Dec 10 | Reviewer experience | a finding can be understood and acted on from the PR comment alone |
| v0.8.0 | Dec 17 | One expansion, chosen from pilot data | the expansion's own measure (decided Dec 10) |
| v0.9.0 | Dec 24 | Release candidate: frozen formats | versioned schemas for config, lock and report, with compatibility tests |
| v1.0.0 | Dec 31 | Stable | the v1.0 criteria below; slips to Jan 7 if the holidays need it |

## Weekly release gate

The same checks every week; a release ships only when all of them pass.

1. `scripts/check.sh` passes on `main` (gofmt, vet, tests, fixture runs, dogfood, README diagram).
2. Offline eval on every lock set, compared finding by finding with the previous release: no true positive lost, no new false positive that is not explained in the release notes.
3. Cortex gate pilot: 5/5.
4. `CHANGELOG.md` has a `## vX.Y.Z (date)` section with the measured results (the release workflow refuses a tag without one).
5. After publishing: the `release` workflow is green, the release has 5 archives and `checksums.txt`, and `go install github.com/stmic168/arch-drift/cmd/archdrift@vX.Y.Z` prints the version.

Rhythm: work Thursday to Tuesday; pull requests ready for merge by Tuesday evening; Mic merges by Wednesday; Mic publishes the release from the Releases page on Thursday. A theme that is not done by Tuesday ships in the next release; the weekly release still goes out with what is done.

Budget: live API runs (pilot and live-check workflows) up to about $0.50 per week, each run approved by Mic as `CLAUDE.md` requires. So far a pilot costs $0.01–0.14.

## Releases

### v0.3.0 (Nov 12): docs finer than the code components

The Istio held-out doc names istiod's internals (CRD Client, Ingress Controller, Tag Watcher) that live in packages below the component IDs. Today they are false `MISSING_COMPONENT` findings.

- Deterministic first: when a doc component is unmapped and a package below some component has the doc's name (`crdclient` for "CRD Client"), report it as info with the package as a suggestion, not as a failing `MISSING_COMPONENT`. No prompt change unless this falls short.
- `scripts/eval-compare.sh <old binary> <new binary>`: the old-vs-new comparison done by hand so far (all lock sets, finding diff, gate pilot, the "what does this loosen" sweep), as one command used by every later release gate.

Done when: Istio has at most 1 report-mode false positive; the other five cases stay at 0; gate 5/5.

### v0.4.0 (Nov 19): `init` that fits the repository

Every pilot so far needed a hand-written config: `services` for Thanos, Cortex and Loki, and a split of `pilot/pkg` for Istio.

- `init` proposes `services` when one deployable has a file per service (`cmd/thanos/rule.go` next to `pkg/rules`) or a module list.
- `init --doc` reads the doc's names (no LLM; plain text) and splits a merged component whose children the doc names.
- A short "choosing services" section in the configuration guide.

Done when: on Thanos, Cortex and Loki, `init` proposes at least 80% of the services in the hand-written pilot configs and no component that is not a service; on Istio it splits `pilot/pkg`.

### v0.5.0 (Nov 26): first real project

The test that matters most: a repository nobody tuned the rules on, with a team's own design doc or rules.

- `docs/guide/pilot-your-repo.md`: install a release, `init --mode gate`, write 3–5 rules, `lock` (only the doc and component IDs leave the machine), `check`, and what to send back for grading.
- Grade every failing finding and record the false-positive rate in the roadmap.

Needs from Mic: a work repository attached to a session, or a run on Mic's machine with `report.json` shared. If neither happens by Nov 24, this release ships the guide plus two more held-out public pilots, and the real-project pilot moves to v0.6.0.

Done when: one non-public repository is checked and every failing finding is graded.

### v0.6.0 (Dec 3): generalization

- At least three more held-out public repositories with design docs, picked for layouts not covered yet (several binaries, a monorepo with `services/*`, a doc written as ADRs).
- Fix the most frequent false-positive class they show, with the v0.3.0 comparison as the gate.
- The eval summary (failing, TP, FP, report-mode FP per case) printed in the release notes from now on.

Done when: at least nine pilot cases, report-mode false positives at or below 10% of failing findings overall, no case above 25%.

### v0.7.0 (Dec 10): reviewer experience

Developers see archdrift in a pull request. The comment must be enough to act on.

- PR comment: each row links the code line and the doc line; a worsened finding shows only the new evidence; a "how to fix or accept" hint per finding type (change the import, update the doc, or add a baseline entry).
- `archdrift explain <finding id>`: the evidence, the doc quote, why the rule fired, and the options, from `report.json`.
- Baseline: warn in the comment when an accepted entry expires within 14 days.
- Decision for v0.8.0 (Dec 10), from the pilot data: see below.

Done when: on the demo PR, every row of the comment has working links and a hint; `explain` works for each of the six finding types.

### v0.8.0 (Dec 17): one expansion, chosen from pilot data

One of these, decided on Dec 10 from what the pilots of v0.5.0 and v0.6.0 actually miss:

- **A second language**: a `CodeModel` interface and a dependency-free import scanner for the language of the real-project pilot (TypeScript, Python or Java import statements are simple to parse). Measure: the fixture ported to that language gives the same findings.
- **gRPC relations from `.proto` files**: only if pilots show `UNVERIFIABLE` gRPC calls between internal components (none of the current ten are).
- **Issue agent**: the Claude GitHub Action working from `CLAUDE.md` on `@claude` issues, restricted to Mic.

Done when: the chosen expansion's measure, written into this plan on Dec 10.

### v0.9.0 (Dec 24): release candidate

Holiday week: no new features.

- Version fields and documented schemas for `archdrift.json`, the lock file and `report.json`; a compatibility test that reads files written by v0.2.0 and later.
- Deprecations removed or marked; every flag and config key in the guides; a docs pass for a first-time user.
- `CLAUDE.md` and the skills updated to the process that has held for eight weeks.

Done when: the compatibility tests pass on files from v0.2.0 to v0.9.0, and a check of every `docs/guide/` page against `archdrift --help` and the config schema finds no gap.

### v1.0.0 (Dec 31): stable

Only fixes since v0.9.0.

- Compatibility promise: config, lock and report formats and the CLI flags do not break within 1.x; changes go through a deprecation release.
- Upgrade guide from 0.x.

Done when: all v1.0 criteria hold. If the holidays leave no time to check them, the release moves to Thursday Jan 7.

## v1.0 criteria

- Report-mode false positives at or below 10% across all pilot cases, no case above 25%.
- The Cortex gate pilot and every gate-mode pilot added later find all planted violations.
- At least one real (non-public) project has used it, with its results recorded.
- Formats versioned and covered by compatibility tests.
- Every release from v0.3.0 has passed the weekly gate.

## Risks

| Risk | Effect | Mitigation |
|---|---|---|
| No access to a real project | v0.5.0 and the v1.0 real-project criterion slip | fallback in v0.5.0; v1.0 waits for it rather than dropping it |
| Merges or publishing land late | a weekly release slips | merge by Wednesday; enable auto-merge and a ruleset on `main` (`docs/guide/development.md`) |
| New pilots bring a new false-positive class | v0.6.0 overruns | ship what is measured; the class moves to v0.7.0 with its numbers |
| Holidays | v0.9.0 / v1.0.0 have less time | both are scoped to no new features; v1.0.0 can move to Jan 7 |
| The LLM mapping varies between runs | numbers move without code changes | locks are reviewed and committed; the eval replays locks, never live output |
