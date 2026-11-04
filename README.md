# archdrift

`archdrift` compares a Go repository with a Markdown design doc and reports architecture drift. Every finding carries evidence: `file:line` from the code and/or a verbatim quote from the doc.

**Core principle:** the LLM only reads the doc and maps the doc's names onto code IDs. Code facts (components, imports) and every drift conclusion come from deterministic Go code. Only the doc text and the list of component IDs go to the API, never source code.

## Status

| Path | State |
|---|---|
| Offline (`--doc-model`) | working, covered by the golden end-to-end test |
| Live API (`AnthropicExtractor`) | verified with `claude-sonnet-5-5` through the `live-check` workflow: the 5 failing findings on the fixture match `expected.json`, 0 dropped claims, about 1.7K input / 1.2K output tokens (~$0.015) per uncached run. Output varies between calls (sampling cannot be pinned on current models): one early run returned a near-empty doc model, which now exits 2 instead of producing a report. |

## Requirements

- Go 1.25+ (required by `github.com/anthropics/anthropic-sdk-go/v2`; with an older Go installed, the toolchain is downloaded automatically)
- For the live path: Anthropic credentials (`ANTHROPIC_API_KEY`, or any source the SDK resolves, e.g. `ant auth login`)
- The only external dependency is the official `anthropic-sdk-go`; parsing uses the stdlib `go/parser`

## Getting started

```bash
go install github.com/stmic168/arch-drift/cmd/archdrift@main
archdrift init --repo . --doc docs/architecture.md            # overview doc: report mode
archdrift init --repo . --doc docs/architecture-rules.md --mode gate   # rules doc (ADR)
```

`init` makes no API call. It:

- proposes `archdrift.json`: components for top-level directories outside `cmd/`, `internal/`, `pkg/` (one per child directory, or one for the whole directory when it has more than 10 children, e.g. `common/`), excludes for test, tooling and mock directories, and the options for the mode (`undocumented_edges.fail: false`; in gate mode also `strict_protocols`);
- writes `.github/workflows/archdrift.yml` at the git root, using the action with `upload-sarif`;
- in gate mode, starts the rules doc from an ADR template listing the detected components, if it does not exist;
- prints the coverage and the next steps (`lock`, review, `check`, `baseline`, commit). Existing files are kept unless `--force`.

On Temporal (a `service/`, `common/`, `api/` layout) it proposes 23 components with 89% coverage, against 5% for the default layout.

## Usage

```bash
go build -o archdrift ./cmd/archdrift
```

```bash
./archdrift check --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path> | --no-lock]
./archdrift lock  --repo <path> --doc <design.md> [--out <dir>] [--model <id>] [--doc-model <json>] [--lock <path>] [--fresh]
./archdrift baseline --repo <path> --doc <design.md> [same flags as check]
./archdrift scan --repo <path> [--config <json>] [--json]
./archdrift init --repo <path> --doc <design.md> [--mode report|gate] [--force]
```

`scan` prints the code model only (components, which are deployables, packages, import edges in and out) without reading a doc or calling the API. Use it to see how archdrift sees a repo and to write `archdrift.json`.

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | required | Go repo root (must contain `go.mod`) |
| `--doc` | required | Markdown design doc (Mermaid blocks are fine). Repeat it to read several files as one doc; findings then name the file each quote came from. |
| `--out` | `archdrift-out` | output dir: `report.md`, `report.json`, `cache/` |
| `--model` | `claude-sonnet-5-5` | Claude model used for doc extraction |
| `--doc-model` | | load a recorded doc model JSON and skip the API (ignores any lock) |
| `--lock` | next to the doc (`design.md` -> `design.lock.json`) | lock file to read (`check`) or write (`lock`) |
| `--no-lock` | | `check` only: ignore the lock and extract from the doc |
| `--config` | `archdrift.json` at the repo root, if present | per-repo configuration (below) |
| `--baseline` | `archdrift.baseline.json` at the repo root, if present | accepted known drift (below) |
| `--base` | | `check` only: git revision to compare with (e.g. `origin/main`). Only drift introduced or worsened since then fails (below). |
| `--sarif-root` | `--repo` | `check` only: directory SARIF paths are relative to; set it to the checkout root when `--repo` is a subdirectory |
| `--today` | current UTC date | `YYYY-MM-DD` used for baseline expiry; set it for reproducible runs |

Exit codes: `check` returns `0` no drift, `1` drift, `2` error; `lock` and `baseline` return `0` written, `2` error. `UNVERIFIABLE` findings never fail the check.

### Lock file (recommended workflow)

The LLM mapping can differ between uncached runs. To make `check` deterministic and free, lock the mapping once:

1. `archdrift lock --repo . --doc docs/design.md` calls the API once and writes `docs/design.lock.json`. It prints a review: each doc component with its code IDs, every relation, code IDs nothing maps to, and claims that failed verification (these are not written).
2. Review the mapping, edit the lock by hand if needed, and commit it.
3. `archdrift check --repo . --doc docs/design.md` now reads the lock and never calls the API. The report shows `Doc model source: lock`.

Rules:

- The lock stores the doc's SHA-256. If the doc changes, `check` exits 2 with "lock file is stale"; re-run `archdrift lock`.
- Re-running `lock` keeps the review. If the doc is unchanged it does nothing (no API call). After a doc edit it extracts again and merges: reviewed claims whose quote is still in the doc are kept as they are, new ones are added, and claims whose quote is gone are removed. It prints "Changes since the previous lock" with `conflict` (reviewed claim kept, the model now suggests something else), `added`, `removed` and `kept` (still in the doc but not extracted this time) lines, so only the changes need review. `--fresh` ignores the previous lock.
- If code components were added or removed since the lock was written, `check` still runs and adds a note at the top of the report. New components show as `UNDOCUMENTED_COMPONENT` until you re-lock; mappings to removed components are dropped as unknown code IDs.
- The verifier runs on every `check`, so a hand edit with a quote that is not in the doc or an unknown code ID is dropped and listed, not trusted.
- Without a lock next to the doc, `check` extracts live as before. `--no-lock` forces that, `--doc-model` overrides both.

Offline demo on the fixture:

```bash
./archdrift check --repo testdata/shop --doc testdata/shop/design.md --doc-model testdata/shop/docmodel.json --out /tmp/shop-report
```

Output on the fixture:

```
archdrift: drift detected: 5 failing finding(s), 1 unverifiable, 3 dropped claim(s)
report: /tmp/shop-report/report.md
```

What is sent to the API: the system prompt, the doc text, and the list of component IDs (e.g. `cmd/api`, `internal/orders`, `ext:postgres`). No source code, file paths or import sites leave the machine.

### Baseline (`archdrift.baseline.json`)

A real repo usually has drift on day one. The baseline accepts known drift so that only new drift fails the check.

1. `archdrift baseline --repo . --doc docs/design.md` writes every current failing finding to `archdrift.baseline.json`. New entries get the reason `TODO: explain why this drift is accepted` and expire in 90 days. Re-running keeps the reason and expiry of entries that still match.
2. Replace each TODO with the real reason (for example a ticket), adjust `expires`, and commit.

```json
{
  "version": 1,
  "accepted": [
    {"id": "FORBIDDEN_EDGE:API Gateway -> Store", "reason": "Legacy path, removal tracked in ARCH-42", "expires": "2027-01-31"}
  ]
}
```

On `check`:

- An accepted finding does not fail the check. It moves to an "Accepted drift (baseline)" table in the report with its reason, expiry and evidence, and is counted as `accepted` (not in `by_type`).
- An entry is valid through its `expires` date (optional; omit it for no expiry). After that the finding fails again and the report says so.
- The report also notes entries that no longer match any finding (remove them) and entries still holding the TODO reason.
- `id` is the finding's `id` from `report.json`. A missing explicit `--baseline` is an error, so a typo cannot silently fail every accepted finding.

### Configuration (`archdrift.json`)

Optional, at the repo root (or `--config`). JSON rather than YAML because the stdlib has no YAML parser and the Anthropic SDK stays the only dependency. Unknown fields and invalid values are errors.

```json
{
  "components": [
    {"id": "billing", "paths": ["services/billing"]},
    {"id": "ledger",  "paths": ["libs/ledger", "libs/ledgerutil"]}
  ],
  "external": [
    {"id": "ext:s3", "prefixes": ["github.com/aws/aws-sdk-go-v2/service/s3"]}
  ],
  "exclude": ["gen", "tools"],
  "default_layout": true,
  "transitive": {"required": true, "forbidden": false}
}
```

| Field | Meaning |
|---|---|
| `components` | Map directories to component IDs. A path matches itself and everything below it; the longest matching path wins. Use this for monorepos and layouts other than `cmd/` / `internal/` / `pkg/`. |
| `external` | Extra `ext:*` IDs by import-path prefix, checked before the built-in catalog (e.g. internal clients, S3, Elasticsearch). |
| `exclude` | Directories to skip entirely (generated code, tools). |
| `shared` | Directories of shared code inside a component directory (protocol types, small helpers such as `pkg/querier/stats`). They belong to no component and importing them creates no edge. |
| `shared_protocol_packages` | Treat generated protobuf packages as shared: a directory name ending in `pb` (e.g. `storepb`), or a directory holding `*.pb.go` files below a component root (e.g. `api/matchingservice/v1` when `api/matchingservice` is mapped to a component). Default `true`. |
| `undocumented_components.fail` | Make `UNDOCUMENTED_COMPONENT` fail the check. Default `false`: they are listed but do not fail, ranked application-first (components that import many others and are imported by few come first, widely imported utilities last). |
| `strict_protocols` | Default `false`. When `true`, a runtime protocol on a relation counts only if the relation's doc quote names it; a protocol the model inferred (e.g. `network` for "A coordinates with B") is ignored and the relation is checked by imports. Use it for rules docs, where a missed violation is worse than an unverifiable runtime call. |
| `undocumented_edges.fail` | Default `true`. Set `false` for overview docs that state few relations: `UNDOCUMENTED_EDGE` is then listed as info and does not fail the check, so only missing components, missing or forbidden edges fail. |
| `undocumented_components.ignore` | Component ID patterns (`path.Match` syntax, e.g. `"pkg/ext*"`) never reported as undocumented. |
| `default_layout` | Keep the `cmd/x`, `internal/x`, `pkg/x` rule for directories no configured component matches. Default `true`; set `false` to model only configured components. |
| `transitive.required` | A required relation is also satisfied by an import path through other components (e.g. `api -> store -> ext:postgres` satisfies "API uses Postgres"). Each hop still needs its own doc relation, or it shows as `UNDOCUMENTED_EDGE`. |
| `transitive.forbidden` | A forbidden relation is also violated by an indirect path. The finding lists the path and the `file:line` of each hop. |

A component is a deployable (separately run program) when a `main` package sits at its root directory (`cmd/x`, or the configured path), whatever the layout. A helper tool nested deeper inside a component does not count. Deployables drive the `UNVERIFIABLE` rule.

## Pipeline

1. **Code model** (`internal/codemodel`). Parses every `.go` file with `go/parser` (imports only), skipping `_test.go`, `vendor/`, `testdata/`, hidden dirs and configured `exclude` dirs below the root. Components are configured `components`, then `cmd/x`, `internal/x`, `pkg/x`; subpackages roll up (`internal/orders/repo` belongs to `internal/orders`). Edges are imports of the module's own packages, with every `file:line`. External drivers map to catalog IDs:

   | Import prefix | ID |
   |---|---|
   | `github.com/jackc/pgx`, `github.com/lib/pq` | `ext:postgres` |
   | `github.com/redis/go-redis`, `github.com/go-redis/redis` | `ext:redis` |
   | `go.mongodb.org/mongo-driver` | `ext:mongodb` |
   | `github.com/segmentio/kafka-go`, `github.com/IBM/sarama`, `github.com/Shopify/sarama` | `ext:kafka` |
   | `google.golang.org/grpc` | `ext:grpc` |
   | `github.com/gocql/gocql`, `github.com/apache/cassandra-gocql-driver` | `ext:cassandra` |
   | `github.com/go-sql-driver/mysql` | `ext:mysql` |
   | `modernc.org/sqlite`, `github.com/mattn/go-sqlite3` | `ext:sqlite` |
   | `github.com/elastic/go-elasticsearch`, `github.com/olivere/elastic` | `ext:elasticsearch` |
   | `github.com/thanos-io/objstore` | `ext:objstore` |
   | `github.com/aws/aws-sdk-go-v2/service/s3`, `github.com/aws/aws-sdk-go/service/s3`, `github.com/minio/minio-go` | `ext:s3` |
   | `cloud.google.com/go/storage` | `ext:gcs` |
   | `github.com/Azure/azure-sdk-for-go/sdk/storage/azblob` | `ext:azure-blob` |
   | `github.com/nats-io/nats.go` | `ext:nats` |
   | `github.com/rabbitmq/amqp091-go`, `github.com/streadway/amqp` | `ext:rabbitmq` |
   | `go.etcd.io/etcd/client` | `ext:etcd` |
   | `github.com/hashicorp/consul/api` | `ext:consul` |
   | `github.com/bradfitz/gomemcache` | `ext:memcached` |

   Generated protobuf packages and configured `shared` directories create no edges. An import of another component's shared package is kept as a *protocol edge*: it proves a required relation (a querier importing the frontend's gRPC client depends on the frontend) but is never an undocumented dependency. The code model also records coverage: how many Go packages belong to a component. `check` adds a note when it is under 50%, naming the largest unmodeled directories.

2. **Doc model** (`internal/docmodel`). One Messages API call with structured output (`output_config.format`, JSON schema). Input: the doc (wrapped in `<design_doc>` tags and treated as untrusted data by the system prompt) plus the ID list. Output: `components[]` (`name`, `code_ids[]`, `quote`) and `relations[]` (`from`, `to`, `kind` = `required|forbidden`, `protocol`, `quote`). The LLM sits behind the `Extractor` interface; implementations are the live `AnthropicExtractor`, `FileExtractor` (`--doc-model`) and `CachedExtractor`.
   - Cache: `<out>/cache/<sha256(model, IDs, doc)>.json`. A hit costs nothing and is reported as source `cache`.
   - `stop_reason: refusal` and `max_tokens` are errors (exit 2) with the refusal category in the message.
   - Fallbacks: the Go SDK (`anthropic-sdk-go/v2` v2.4.0) has `BetaFallbacksParamUnion.OfDefault`, so requests send `fallbacks: "default"` with beta header `server-side-fallback-2026-07-01`. A refused request is re-served server-side by the model Anthropic recommends for the refusal category; the decoder reads the text after the last `fallback` block. This works on the first-party Claude API only (not Bedrock, Vertex or Foundry).
3. **Verifier**. A claim's quote must appear in the doc after whitespace normalization, or the claim is dropped. Unknown `code_ids` are dropped. Relations pointing at a dropped component, with an invalid kind, or with a fake quote are dropped. Every dropped claim is listed in the report.
4. **Diff** (`internal/drift`):

   | Type | Severity | Fails | Rule |
   |---|---|---|---|
   | `FORBIDDEN_EDGE` | high | yes | doc says A must not depend on B, and code has a direct import A to B |
   | `MISSING_COMPONENT` | medium | yes | doc component maps to no code ID |
   | `MISSING_EDGE` | medium | yes | doc requires A to depend on B, the relation can be checked by static import, and there is no direct import |
   | `UNDOCUMENTED_EDGE` | low | yes | direct import between two documented components (in different doc components) that no doc relation covers |
   | `UNDOCUMENTED_COMPONENT` | info (low when configured to fail) | no by default | code component that no doc component maps to, ranked by how many components import it |
   | `UNVERIFIABLE` | info | no | relation that imports cannot prove or disprove: the target is a deployable (a main package cannot be imported); the protocol is a runtime transport (HTTP, gRPC, Kafka, queue, ...) and there is no import; or one endpoint has no code (missing, or external by design) |

   Doc components the model marks `external` (users, SDK workers, dashboards, managed services) are not reported as `MISSING_COMPONENT` when they have no code ID. Relations to `ext:*` are always checkable: the driver import is the evidence, directly or through a client wrapper (`store -> cacheutil -> memcached` satisfies "Store uses Memcached"). A required relation is also satisfied by a protocol edge. The protocol `network` (separately running nodes that talk without a named protocol) counts as a runtime transport. By default only direct imports count; `transitive` in the config extends that to import paths. Edges touching an undocumented component are not reported again as `UNDOCUMENTED_EDGE`.
5. **Report** (`internal/report`): `report.md` (findings with evidence, suggested doc updates, an as-built diagram, a coloured drift diagram, dropped claims, token usage and estimated cost), `report.json` and `report.sarif`.

   **Suggested doc updates** turn each finding into a concrete edit, generated from the evidence without an LLM: which import to remove for a forbidden dependency, which doc sentence to update for a missing one, what to describe for an undocumented dependency, and the top application-like undocumented components (those that import more components than import them). The **as-built diagram** is a Mermaid flowchart of the documented components with the dependencies the code has between them today, under the doc's names (gRPC-client-only links dashed), ready to paste into the doc. Output has no timestamps and is byte-identical across runs on the same inputs.

## Layout

```
action.yml                  composite GitHub Action
cmd/archdrift/main.go       CLI
internal/codemodel          scanner
internal/docmodel           Extractor interface, Anthropic client, cache, verifier
internal/drift              deterministic diff
internal/report             Markdown, Mermaid, JSON
pilot/                      archdrift.json configs for pilot repositories
testdata/shop/              fixture: Go repo, design.md, recorded docmodel.json, design.lock.json, expected.json
```

The fixture plants one finding of each type (see the `planted` comments in its code) plus three bad claims in `docmodel.json` (a fake quote, an unknown code ID, a fake `forbidden` relation that would otherwise create a false `FORBIDDEN_EDGE`). It also contains a `_test.go`, a `testdata/` dir, a `vendor/` dir and a prompt-injection comment in the doc to exercise the skips.

## Tests

```bash
go vet ./... && go test ./...
```

Covered: scanner on the fixture, verifier dropping fake quotes and unknown IDs, a table test of all six finding types, the request shape (structured output, `fallbacks: "default"`, only doc + IDs sent), refusal/max_tokens/fallback decoding, the cache, byte-identical output across two runs, and a golden end-to-end run with `--doc-model` that must produce exactly `expected.json` and exit 1.

## CI

- `.github/workflows/ci.yml` runs on every pull request and on pushes to `main`: `go vet`, `go test`, an offline fixture run with `--doc-model`, and a run that reads the committed `testdata/shop/design.lock.json`; both must exit 1.
- `.github/workflows/live-check.yml` is manual (`workflow_dispatch`) and calls the real API on the fixture (with `--no-lock`), so it costs tokens. It needs the repository secret `ANTHROPIC_API_KEY`. It runs twice (the second run must hit the cache), prints the doc model and dropped claims to the log, fails if the failing findings differ from `expected.json` (a different `UNVERIFIABLE` set is only a notice, since it depends on how the model reads runtime relations), puts `report.md` in the job summary, and uploads the reports as an artifact. You can pick a branch under **Use workflow from**. GitHub only shows **Run workflow** once the workflow file is on the default branch.

## Pilot on public repositories

`.github/workflows/pilot.yml` (manual) runs archdrift on any public Go repository: it shallow-clones the repo, prints `scan`, runs `lock` (one API call; only the doc and component IDs are sent), runs `check` from that lock, and prints the doc mapping and every finding to the log and job summary. Inputs: `repo` (owner/name), optional `ref`, `doc` (path in that repo; comma-separated for several files), optional `config` (a file under `pilot/` in this repo) and `model`. The log also prints the lock JSON, so a run can be replayed offline against the same mapping. Configs for pilot targets live in `pilot/` (e.g. `pilot/temporal.json`).

### Pull requests: `--base` and SARIF

`archdrift check --base origin/main ...` checks the base revision out in a temporary git worktree, runs the same doc model and options there, and compares. Each finding gets a status:

- `new`: absent on the base. Fails.
- `worsened`: present on the base, but with code evidence in files the base finding did not have (compared by file, so shifted lines do not count). Fails, and the report lists the new evidence.
- `existing`: already on the base and nothing new. Does not fail; listed under "Pre-existing drift".

Failing base findings that disappeared are listed as "Fixed by this change". `--repo` may be a subdirectory of the git repository; the base commit must be available locally (fetch it first in a shallow clone).

Every `check` also writes `<out>/report.sarif` (SARIF 2.1.0) for GitHub code scanning: one rule per finding type, failing findings as errors (warnings for low severity), others as notes, located at the code evidence or, for findings without code, at the quote's line in the doc.

### GitHub Action

`action.yml` makes the repository usable as a step. With a committed lock no API key is needed; on `pull_request` events it compares with the PR base by default, so only drift the PR introduces fails.

```yaml
name: architecture
on: pull_request
permissions:
  contents: read
  security-events: write   # only for upload-sarif
jobs:
  archdrift:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: stmic168/arch-drift@main
        with:
          docs: docs/architecture.md        # comma- or newline-separated
          # config: archdrift.json          # default: archdrift.json in repo-path
          # lock: docs/architecture.lock.json  # default: next to the first doc
          # base: none                      # check everything instead of the PR diff
          upload-sarif: "true"              # annotate the PR's changed lines
          # anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}  # only without a lock
```

Inputs: `docs`, `repo-path`, `config`, `lock`, `base`, `model`, `anthropic-api-key`, `out`, `upload-sarif`, `fail-on-drift`. Outputs: `exit-code`, `report`, `sarif`. `report.md` is added to the job summary. `.github/workflows/action-selftest.yml` runs the action against the fixture on every PR.

### Rules docs (gate mode)

archdrift is strongest when the doc states rules ("A must not depend on B", "A sends to B"), for example an ADR. `pilot/docs/cortex-rules.md` is such a doc for Cortex, with ground truth in `pilot/docs/cortex-rules.expected.json` (3 violated prohibitions, 2 absent requirements, 8 rules that hold). With `pilot/cortex-rules.json` (`strict_protocols`, undocumented edges report-only), two independent pilot runs each found exactly the 5 violations with `file:line` evidence and nothing else. Prohibitions are also violated by importing the target's generated gRPC client (a protocol edge).

This repository checks itself the same way: `docs/architecture-rules.md` (ADR-001) states the rules behind the core principle (only the Doc model may call the Anthropic API; the code model, drift engine, report and config never do) and the package layering, `docs/architecture-rules.lock.json` holds the reviewed mapping (7 components, 21 relations), and `.github/workflows/archdrift.yml` runs the action on every pull request. A test file in `internal/report` importing the Anthropic SDK is reported as `FORBIDDEN_EDGE Report -> Anthropic API` with its `file:line`.

### Offline evaluation

`pilot/eval/` replays the pilot runs from their recorded locks without calling the API and scores failing findings against hand-graded labels, so rule changes can be measured: `pilot/eval/run.sh <archdrift binary> <work dir>`. See `pilot/eval/README.md`.

## Limitations

- The doc model comes from an LLM and can differ between uncached runs; the cache makes repeated runs on the same doc stable.
- Cost is an estimate from list prices (cache writes 1.25x input, cache reads 0.1x input). With a server-side fallback, it is priced at the serving model's rate.
- Without a config, packages outside `cmd/`, `internal/`, `pkg/` are ignored.
