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
- `labels.json` maps finding IDs to a grade: `TP` (real drift), `FP-mapping`
  (wrong because the doc mapping or relations were wrong or missing), `FP-runtime`
  (the relation exists at runtime but not as an import), `FP-actor` (an external
  system reported missing), `FP-config` (wrong because of how the config grouped
  packages), `NOISE` (true but not worth an architect's time).
- A failing finding without a label is printed as `UNGRADED`; grade it and add it.
- `_important_undocumented` lists real undocumented components; the score shows
  their rank in the non-failing UNDOCUMENTED_COMPONENT list (lower is better).
