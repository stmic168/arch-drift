# Development

[README](../../README.md) · [Usage](usage.md) · [Configuration](configuration.md) · [CI and pull requests](ci.md) · [How it works](how-it-works.md) · [Development](development.md)

## Status

| Path | State |
|---|---|
| Offline (`--doc-model`) | working, covered by the golden end-to-end test |
| Live API (`AnthropicExtractor`) | verified with `claude-sonnet-5-5` through the `live-check` workflow: the 5 failing findings on the fixture match `expected.json`, 0 dropped claims, about 1.7K input / 1.2K output tokens (~$0.015) per uncached run. Output varies between calls (sampling cannot be pinned on current models): one early run returned a near-empty doc model, which now exits 2 instead of producing a report. |

## Layout

```
action.yml                  composite GitHub Action
cmd/archdrift/              CLI (main.go, init.go)
docs/architecture-rules.md  ADR-001, the rules archdrift checks itself against (with its .lock.json)
docs/guide/                 user and developer documentation
internal/codemodel          scanner
internal/docmodel           Extractor interface, Anthropic client, cache, verifier
internal/drift              deterministic diff
internal/config             archdrift.json parsing
internal/report             Markdown, Mermaid, JSON, SARIF, suggestions
pilot/                      archdrift.json configs, rules docs and offline eval for pilot repositories
testdata/shop/              fixture: Go repo, design.md, recorded docmodel.json, design.lock.json, expected.json
```

The fixture plants one finding of each type (see the `planted` comments in its code) plus three bad claims in `docmodel.json` (a fake quote, an unknown code ID, a fake `forbidden` relation that would otherwise create a false `FORBIDDEN_EDGE`). It also contains a `_test.go`, a `testdata/` dir, a `vendor/` dir and a prompt-injection comment in the doc to exercise the skips.

## Tests

`scripts/check.sh` runs everything a pull request needs locally: gofmt, `go vet`, `go test`, the two fixture runs (exit 1 expected) and the dogfood check against ADR-001 (exit 0 expected). Run it before every push.

```bash
go vet ./... && go test ./...
```

Covered: scanner on the fixture, verifier dropping fake quotes and unknown IDs, a table test of all six finding types, the request shape (structured output, `fallbacks: "default"`, only doc + IDs sent), refusal/max_tokens/fallback decoding, the cache, byte-identical output across two runs, and a golden end-to-end run with `--doc-model` that must produce exactly `expected.json` and exit 1.

## CI

- `.github/workflows/ci.yml` runs on every pull request and on pushes to `main`: `go vet`, `go test`, an offline fixture run with `--doc-model`, and a run that reads the committed `testdata/shop/design.lock.json`; both must exit 1.
- `.github/workflows/live-check.yml` is manual (`workflow_dispatch`) and calls the real API on the fixture (with `--no-lock`), so it costs tokens. It needs the repository secret `ANTHROPIC_API_KEY`. It runs twice (the second run must hit the cache), prints the doc model and dropped claims to the log, fails if the failing findings differ from `expected.json` (a different `UNVERIFIABLE` set is only a notice, since it depends on how the model reads runtime relations), puts `report.md` in the job summary, and uploads the reports as an artifact. You can pick a branch under **Use workflow from**. GitHub only shows **Run workflow** once the workflow file is on the default branch.

## Working with Claude

Claude sessions follow [`CLAUDE.md`](../../CLAUDE.md) (conventions, limits of the cloud session) and take work from [`docs/ROADMAP.md`](../ROADMAP.md). Project skills in `.claude/skills/`:

- `/ship`: `scripts/check.sh`, commit, push, open the pull request with the Problem / Change / Verification template, remove the footer the server adds, subscribe to CI.
- `/next`: check CI on `main`, take the next roadmap item, build it, update the roadmap.
- `/pilot`: run the `pilot` workflow on a public repository (one API call), save the lock, replay it offline and grade the findings.

## Repository settings

Set once by the owner in the GitHub UI, so pull requests merge themselves once approved and green:

1. **Settings, General, Pull Requests**: tick **Allow auto-merge** and **Automatically delete head branches**.
2. **Settings, Rules, Rulesets, New ruleset, New branch ruleset**: name `main`, enforcement **Active**, target **Include default branch**. Tick:
   - **Restrict deletions** and **Block force pushes**;
   - **Require a pull request before merging** (required approvals 0 for a single maintainer; reviews still possible);
   - **Require status checks to pass**, adding `test` (ci), `full-check` and `diff-check` (action-selftest), and `archdrift` (architecture). A check appears in the list after it has run once.
3. On a pull request, **Enable auto-merge** (squash or merge commit): it merges when the required checks pass.

`.github/dependabot.yml` opens weekly pull requests for GitHub Actions versions and the Go module; they go through the same checks.

## Releases

1. Add a `## vX.Y.Z (...)` section to [CHANGELOG.md](../../CHANGELOG.md) and merge it to `main`.
2. Tag the merge commit and push the tag: `git tag vX.Y.Z origin/main && git push origin vX.Y.Z`. Or in the GitHub UI: **Releases**, **Draft a new release**, type `vX.Y.Z` under **Choose a tag** and pick **Create new tag on publish**, target `main`, leave the description empty (the changelog section fills it), **Publish release**.
3. `.github/workflows/release.yml` checks that the tag is on `main` and has a changelog section, runs vet and tests, builds `archdrift` for Linux, macOS (amd64, arm64) and Windows (amd64) with the version embedded (`archdrift version`), and creates the GitHub release (or completes the one made in the UI) with the archives, `checksums.txt` and the changelog section as notes.

Users pin the action with `uses: stmic168/arch-drift@vX.Y.Z`; `go install ...@vX.Y.Z` reports the same version.

## Pilot on public repositories

`.github/workflows/pilot.yml` (manual) runs archdrift on any public Go repository: it shallow-clones the repo, prints `scan`, runs `lock` (one API call; only the doc and component IDs are sent), runs `check` from that lock, and prints the doc mapping and every finding to the log and job summary. Inputs: `repo` (owner/name), optional `ref`, `doc` (path in that repo; comma-separated for several files), optional `config` (a file under `pilot/` in this repo) and `model`. The log also prints the lock JSON, so a run can be replayed offline against the same mapping. Configs for pilot targets live in `pilot/` (e.g. `pilot/temporal.json`).

## Rules docs (gate mode)

archdrift is strongest when the doc states rules ("A must not depend on B", "A sends to B"), for example an ADR. `pilot/docs/cortex-rules.md` is such a doc for Cortex, with ground truth in `pilot/docs/cortex-rules.expected.json` (3 violated prohibitions, 2 absent requirements, 8 rules that hold). With `pilot/cortex-rules.json` (`strict_protocols`, undocumented edges report-only), two independent pilot runs each found exactly the 5 violations with `file:line` evidence and nothing else. Prohibitions are also violated by importing the target's generated gRPC client (a protocol edge).

This repository checks itself the same way: `docs/architecture-rules.md` (ADR-001) states the rules behind the core principle (only the Doc model may call the Anthropic API; the code model, drift engine, report and config never do) and the package layering, `docs/architecture-rules.lock.json` holds the reviewed mapping (7 components, 21 relations), and `.github/workflows/archdrift.yml` runs the action on every pull request. A test file in `internal/report` importing the Anthropic SDK is reported as `FORBIDDEN_EDGE Report -> Anthropic API` with its `file:line`.

## Offline evaluation

`pilot/eval/` replays the pilot runs from their recorded locks without calling the API and scores failing findings against hand-graded labels, so rule changes can be measured: `pilot/eval/run.sh <archdrift binary> <work dir>`. See `pilot/eval/README.md`.
