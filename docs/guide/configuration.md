# Configuration

[README](../../README.md) · [Usage](usage.md) · [Configuration](configuration.md) · [CI and pull requests](ci.md) · [How it works](how-it-works.md) · [Development](development.md)

## Choosing a mode

`archdrift init --mode report|gate` writes a config for one of two ways of using a doc:

| Mode | Doc | Config | What fails |
|---|---|---|---|
| report (default for `init`) | overview doc that describes components and a few relations | `undocumented_edges.fail: false` | missing components, missing or forbidden edges |
| gate | rules doc (ADR) that states "A must not depend on B" / "A sends to B" | `undocumented_edges.fail: false`, `strict_protocols: true` | the same, with runtime protocols counted only when the quote names them |
| none | any | no `archdrift.json` | every failing finding type, including `UNDOCUMENTED_EDGE` |

See [Rules docs (gate mode)](development.md#rules-docs-gate-mode) for a measured example.

## `archdrift.json`

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
