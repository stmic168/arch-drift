"""Score failing findings against hand-graded labels.

Categories: TP (real drift), FP-mapping, FP-runtime, FP-actor, FP-config,
NOISE. A failing finding with no label is printed as UNGRADED so it can be
graded and added to labels.json.
"""
import json
import sys

work, labels_path, cases = sys.argv[1], sys.argv[2], sys.argv[3:]
labels = json.load(open(labels_path))
total = {}
print(f"{'case':<15}{'failing':>8}{'TP':>5}{'FP':>5}{'NOISE':>7}{'UNGR':>6}   FP%  noise%")
for case in cases:
    try:
        report = json.load(open(f"{work}/{case}/out/report.json"))
    except FileNotFoundError:
        print(f"{case:<15} no report (see {work}/{case}/stdout.txt)")
        continue
    known = labels.get(case, {})
    counts = {"TP": 0, "FP": 0, "NOISE": 0, "UNGRADED": 0}
    ungraded = []
    for f in report["findings"]:
        if not f["failing"]:
            continue
        cat = known.get(f["id"], "UNGRADED")
        key = "FP" if cat.startswith("FP") else cat
        counts[key] += 1
        if cat == "UNGRADED":
            ungraded.append(f["id"])
    n = sum(counts.values())
    for k, v in counts.items():
        total[k] = total.get(k, 0) + v
    edges_only = [known.get(f["id"], "UNGRADED") for f in report["findings"] if f["failing"] and f["type"] != "UNDOCUMENTED_EDGE"]
    report_mode = (sum(c == "TP" for c in edges_only), sum(c.startswith("FP") for c in edges_only), sum(c == "NOISE" for c in edges_only))
    pct = lambda x: f"{100 * x // n:>5}" if n else "    -"
    print(f"{case:<15}{n:>8}{counts['TP']:>5}{counts['FP']:>5}{counts['NOISE']:>7}{counts['UNGRADED']:>6} {pct(counts['FP'])} {pct(counts['NOISE'])}")
    for u in ungraded:
        print(f"    UNGRADED {u}")
    print(f"    with undocumented_edges.fail=false: failing {len(edges_only)} (TP {report_mode[0]}, FP {report_mode[1]}, NOISE {report_mode[2]})")
    ranked = [f["subject"] for f in report["findings"] if f["type"] == "UNDOCUMENTED_COMPONENT"]
    print(f"    undocumented (top 5 of {len(ranked)}): {', '.join(ranked[:5])}")
    for imp in labels.get("_important_undocumented", {}).get(case, []):
        rank = ranked.index(imp) + 1 if imp in ranked else "-"
        print(f"    rank of {imp}: {rank} of {len(ranked)}")
n = sum(total.values())
if n:
    print(f"{'TOTAL':<15}{n:>8}{total['TP']:>5}{total['FP']:>5}{total['NOISE']:>7}{total['UNGRADED']:>6} {100 * total['FP'] // n:>5} {100 * total['NOISE'] // n:>5}")
