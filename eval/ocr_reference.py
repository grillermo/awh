#!/usr/bin/env python3
"""usage: ocr_reference.py <manifest.jsonl> <out.jsonl>  (run from the repo root)"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import cv2  # noqa: E402

from auto_reset import get_ocr_reader, read_display  # noqa: E402
from display_reading import classify  # noqa: E402

manifest, out_path = sys.argv[1], sys.argv[2]
rows = [json.loads(line) for line in open(manifest)]
reader = get_ocr_reader()
first = cv2.imread(rows[0]["path"])
for _ in range(2):
    read_display(reader, first)

with open(out_path, "w") as out:
    for row in rows:
        img = cv2.imread(row["path"])  # BGR uint8, the same as the live crop
        t0 = time.perf_counter()
        text, score = read_display(reader, img)
        ms = (time.perf_counter() - t0) * 1000
        kind, value = classify(text, score)
        out.write(json.dumps({"id": row["id"], "text": text, "score": round(score, 5),
                              "kind": kind, "value": value, "ms": round(ms, 2)},
                             ensure_ascii=False) + "\n")
print(f"wrote {len(rows)} results to {out_path}")
