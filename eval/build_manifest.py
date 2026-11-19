#!/usr/bin/env python3
"""
Builds eval/manifest.jsonl: one {"id", "path", "truth"?} per crop.

Sources: every ocr_samples/*/*_raw.png (the raw crops OCR reads in production),
the cropped images saved with past errors in errors.sqlite3 (written out as PNG
so both sides decode identical pixels), and truth labels from ocr_bench/dataset.json.
"""
import glob
import json
import os
import sqlite3

import cv2
import numpy as np

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)

truth = {}
for rec in json.load(open("ocr_bench/dataset.json")):
    truth[os.path.realpath(rec["raw"])] = rec["truth"]

rows = []
for path in sorted(glob.glob("ocr_samples/*/*_raw.png")):
    kind, name = path.split(os.sep)[-2:]
    real = os.path.realpath(path)
    row = {"id": kind + "/" + name[:-len("_raw.png")], "path": real}
    if real in truth:
        row["truth"] = truth[real]
    rows.append(row)

os.makedirs("eval/images/errors", exist_ok=True)
with sqlite3.connect(os.environ.get("ERRORS_DB", "errors.sqlite3")) as conn:
    for err_id, blob in conn.execute("SELECT id, cropped_image FROM errors ORDER BY id"):
        img = cv2.imdecode(np.frombuffer(blob, np.uint8), cv2.IMREAD_COLOR)
        out = os.path.abspath(f"eval/images/errors/{err_id}.png")
        cv2.imwrite(out, img)
        rows.append({"id": f"errors/{err_id}", "path": out})

with open("eval/manifest.jsonl", "w") as fh:
    for row in rows:
        fh.write(json.dumps(row) + "\n")
n_truth = sum(1 for r in rows if "truth" in r)
print(f"{len(rows)} images, {n_truth} with truth")
