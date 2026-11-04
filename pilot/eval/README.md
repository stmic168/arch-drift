# Offline pilot evaluation

Replays the pilot runs against the locks they recorded, so rule changes can
be measured without calling the API, and scores failing findings against
hand-graded labels.

```bash
go build -o /tmp/archdrift ./cmd/archdrift
pilot/eval/run.sh /tmp/archdrift /tmp/archdrift-eval
```

- `locks/` holds the lock JSON printed by the pilot workflow runs after phase 0.5
  (thanos-design 41570777478, thanos-multi 41570780951, cortex 41570784103,
  temporal 41570787204).
- `locks-p06/` holds the locks from the phase 0.6 prompt runs (41573056751,
  41573059694, 41573062580, 41573065740); replay them with
  `LOCKS=pilot/eval/locks-p06 pilot/eval/run.sh ...`.
- `run.sh` shallow-clones each repository at the pinned commit (first run only),
  runs `archdrift check` from the lock, and calls `score.py`.
- The Thanos and Cortex cases use `pilot/thanos.json` and `pilot/cortex.json`, which list
  each project's services (separately run modes of one binary).
  Score with these configs: 46 failing (5 TP, 24 FP, 17 NOISE); in report mode
  (`undocumented_edges.fail=false`) 1 failing, an external actor graded FP. Before
  `services`: 49 failing (27 FP), report mode 4 (all FP: three runtime calls between
  services, one actor).
- `labels.json` maps finding IDs to a grade: `TP` (real drift), `FP-mapping`
  (wrong because the doc mapping or relations were wrong or missing), `FP-runtime`
  (the relation exists at runtime but not as an import), `FP-actor` (an external
  system reported missing), `FP-config` (wrong because of how the config grouped
  packages), `NOISE` (true but not worth an architect's time).
- A failing finding without a label is printed as `UNGRADED`; grade it and add it.
- `_important_undocumented` lists real undocumented components; the score shows
  their rank in the non-failing UNDOCUMENTED_COMPONENT list (lower is better).

## Gate-mode pilot (rules doc)

`locks-gate/run1.lock.json` and `run2.lock.json` come from pilot runs 41575578152 and
41575581758: cortexproject/cortex @ a55f3193 checked against `pilot/docs/cortex-rules.md`.
Replay one with a Cortex clone linked as `target` and this repo's `pilot/` linked as `pilot`:

```bash
archdrift check --repo target --doc pilot/docs/cortex-rules.md --config pilot/cortex-rules.json \
  --lock pilot/eval/locks-gate/run1.lock.json --out out
```

Expected failing findings are in `pilot/docs/cortex-rules.expected.json`. Without
`strict_protocols`, run 1 reports Purger -> Compactor as UNVERIFIABLE (the model tagged it
`network`) instead of MISSING_EDGE.
