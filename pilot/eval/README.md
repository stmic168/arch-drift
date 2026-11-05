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
- `locks-p07/` holds fresh locks from pilot runs 41798769126 (thanos-design),
  41798773477 (thanos-multi), 41798777715 (cortex) and 41798781963 (temporal), made
  with the current prompt at the pinned commits and the `services` configs
  ($0.22 in total). Replay with `LOCKS=pilot/eval/locks-p07`: 43 failing (5 TP,
  18 FP, 20 NOISE); report mode 2 failing, both `MISSING_EDGE Rule -> Object
  Storage` in the Thanos cases (FP: Thanos' binary wires Rule to `pkg/shipper`,
  which uploads blocks, so `pkg/rules` never imports object storage). Alert
  Component is marked external in both Thanos runs. In cortex this run missed the
  doc's "hash ring -> Consul/Etcd" relations that both earlier runs had, so
  `pkg/ring -> ext:consul` and `-> ext:etcd` are undocumented edges (FP-mapping):
  run-to-run LLM variation, which reviewing the lock catches.
  With the wired-by-the-binary rule (a service's external system imported by
  the same deployable file as the service), the two Rule -> Object Storage
  findings are satisfied: 41 failing (5 TP, 16 FP, 20 NOISE), report mode 0 on
  all four cases (also 0 with `locks-p06`).
- `run.sh` shallow-clones each repository at the pinned commit (first run only),
  runs `archdrift check` from the lock, and calls `score.py`.
- The Thanos and Cortex cases use `pilot/thanos.json` and `pilot/cortex.json`, which list
  each project's services (separately run modes of one binary).
  Score with these configs: 46 failing (5 TP, 24 FP, 17 NOISE); in report mode
  (`undocumented_edges.fail=false`) 1 failing, an external actor graded FP. Before
  `services`: 49 failing (27 FP), report mode 4 (all FP: three runtime calls between
  services, one actor).
- **Held out**: `loki` and `istio` were added after the rules were tuned on the
  four cases above, to check for overfitting (runs 41809520171 and 41809524645,
  $0.06; locks in `locks-p07/`; configs `pilot/loki.json` and `pilot/istio.json`
  start from `archdrift init --mode report`).
  - Loki (single binary, `services` from its module list): 0 failing; the 7 doc
    relations are checked by imports and hold.
  - Istio (`architecture/networking/pilot.md`, a doc on istiod's internals): 9
    failing, all FP-mapping: `MISSING_COMPONENT` for parts that exist in packages
    finer than the components (CRD Client is `pilot/pkg/config/kube/crdclient`
    inside `pilot/config`; Ingress Controller, Gateway Deployment, Tag Watcher,
    Discovery Filter, Mesh Config, Multicluster Secret, CRD Watcher likewise).
    The model leaves them unmapped rather than guess. Two `MISSING_EDGE` between
    doc components mapped to the same code component were an archdrift bug, now
    `UNVERIFIABLE`.
    Since v0.3.0, an unmapped doc component whose name a package or exported type
    has (`crdclient`, `TagWatcher`, `multicluster/secretcontroller.go`) is info,
    not failing: Istio report mode 9 -> 1 (Ingress Status Controller has no
    named code). With `locks-p07`: 42 failing (5 TP, 17 FP, 20 NOISE).
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
