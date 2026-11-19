#!/usr/bin/env python3
"""usage: compare_ocr.py <manifest> <before.jsonl> <after.jsonl> <title>  -> markdown on stdout"""
import json
import statistics
import sys

manifest, before_p, after_p, title = sys.argv[1:5]
truth = {r["id"]: r.get("truth") for r in map(json.loads, open(manifest))}
before = {r["id"]: r for r in map(json.loads, open(before_p))}
after = {r["id"]: r for r in map(json.loads, open(after_p))}
ids = [i for i in before if i in after]
missing = set(before) ^ set(after)

kind_diff = [i for i in ids if before[i]["kind"] != after[i]["kind"]]
text_diff = [i for i in ids if before[i]["text"] != after[i]["text"]]
go_only_errors = [i for i in kind_diff if after[i]["kind"] == "error"]
deltas = sorted(abs(before[i]["score"] - after[i]["score"]) for i in ids)
p99 = deltas[int(0.99 * (len(deltas) - 1))] if deltas else 0.0
cross = [i for i in ids if (before[i]["score"] >= 0.7) != (after[i]["score"] >= 0.7)]


def truth_hits(side):
    labelled = [i for i in ids if truth.get(i)]
    hits = sum(1 for i in labelled if side[i]["value"] == truth[i])
    return hits, len(labelled)


def ms(side):
    v = sorted(side[i]["ms"] for i in ids)
    return statistics.median(v), v[int(0.95 * (len(v) - 1))]


bh, bn = truth_hits(before)
ah, an = truth_hits(after)
gates = {
    "kind agrees on every image": not kind_diff,
    "no Go-only 'error'": not go_only_errors,
    "text agrees on >= 99%": len(text_diff) <= 0.01 * len(ids),
    "|Δscore| p99 <= 0.01": p99 <= 0.01,
    "truth set unchanged": ah == bh,
    "same images on both sides": not missing,
}
print(f"## {title}\n")
print(f"{len(ids)} images. Truth set: before {bh}/{bn}, after {ah}/{an}.\n")
print("| Gate | Result |\n|---|---|")
for name, ok in gates.items():
    print(f"| {name} | {'PASS' if ok else 'FAIL'} |")
print(f"\n|Δscore|: median {statistics.median(deltas):.5f}, p99 {p99:.5f}, max {deltas[-1]:.5f}. "
      f"Images crossing the 0.7 threshold on one side only: {len(cross)}.\n")
bm, bp = ms(before)
am, ap = ms(after)
print(f"OCR latency: before median {bm:.1f} ms / p95 {bp:.1f}; after median {am:.1f} ms / p95 {ap:.1f}.\n")
if text_diff or cross:
    print("| id | before | after |\n|---|---|---|")
    for i in sorted(set(text_diff) | set(cross)):
        b, a = before[i], after[i]
        print(f"| {i} | {b['text']!r} {b['score']:.3f} {b['kind']} | {a['text']!r} {a['score']:.3f} {a['kind']} |")
sys.exit(0 if all(gates.values()) else 1)
