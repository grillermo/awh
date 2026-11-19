#!/usr/bin/env python3
"""usage: mem_summary.py <memwatch.csv> [...]  -> median/p95/max and slope (MB per hour)"""
import csv
import statistics
import sys

for path in sys.argv[1:]:
    rows = list(csv.DictReader(open(path)))
    t = [float(r["epoch"]) for r in rows]
    y = [float(r["total_mb"]) for r in rows]
    if len(y) < 2:
        print(f"{path}: not enough samples")
        continue
    ys = sorted(y)
    p95 = ys[int(0.95 * (len(ys) - 1))]
    tm, ym = statistics.fmean(t), statistics.fmean(y)
    den = sum((ti - tm) ** 2 for ti in t)
    slope_h = (sum((ti - tm) * (yi - ym) for ti, yi in zip(t, y)) / den * 3600) if den else 0.0
    hours = (t[-1] - t[0]) / 3600
    print(f"{path}: n={len(y)} over {hours:.1f} h  median={statistics.median(y):.1f} MB  "
          f"p95={p95:.1f}  max={max(y):.1f}  slope={slope_h:+.2f} MB/h")
