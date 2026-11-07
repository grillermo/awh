#!/usr/bin/env python3
"""
Builds ocr-examples.html (outside ocr_samples/, so it does not eat the sample disk budget): one self-contained page (images inlined)
showing every collected OCR sample, grouped by kind, for eyeballing misreads
and comparing OCR models. Run: python ocr_examples.py [output.html]
"""

import base64
import collections
import html
import json
import os
import sys
import time

from ocr_samples import SAMPLES_DIR

KINDS = [
    ("text", "Letters or words read", "OCR returned something that is not a number."),
    ("digit", "Lone single digit", "A single digit, almost always a misread of the two-digit display."),
    ("empty", "Nothing read", "OCR returned no text at all. Randomly sampled."),
]


def data_uri(path):
    try:
        with open(path, "rb") as fh:
            return "data:image/png;base64," + base64.b64encode(fh.read()).decode()
    except OSError:
        return ""


def load_records(root):
    path = os.path.join(root, "index.jsonl")
    recs = []
    with open(path) as fh:
        for line in fh:
            try:
                recs.append(json.loads(line))
            except ValueError:
                pass
    return recs


def card(rec):
    label = rec["ocr_text"] or "∅"
    when = time.strftime("%d %b %H:%M:%S", time.localtime(rec["at"]))
    return f"""
    <figure class="card">
      <div class="imgs">
        <div><img src="{data_uri(rec['ocr'])}" alt="enhanced frame"><span>OCR input</span></div>
        <div><img class="raw" src="{data_uri(rec['raw'])}" alt="raw crop"><span>raw crop</span></div>
      </div>
      <figcaption><code>{html.escape(label)}</code><time>{when}</time></figcaption>
    </figure>"""


def build(root=SAMPLES_DIR):
    recs = sorted(load_records(root), key=lambda r: r["at"], reverse=True)
    by_kind = collections.defaultdict(list)
    for rec in recs:
        by_kind[rec["kind"]].append(rec)

    texts = collections.Counter(r["ocr_text"] for r in recs if r["kind"] in ("text", "digit"))
    chips = "".join(
        f'<li><code>{html.escape(t)}</code><b>{n}</b></li>' for t, n in texts.most_common()
    )
    summary = " · ".join(f"{len(by_kind[k])} {k}" for k, _, _ in KINDS)
    sections = []
    for kind, title, blurb in KINDS:
        items = by_kind.get(kind, [])
        body = "".join(card(r) for r in items) or '<p class="none">No samples yet.</p>'
        sections.append(
            f'<section><h2>{title} <small>{len(items)}</small></h2><p class="blurb">{blurb}</p>'
            f'<div class="grid">{body}</div></section>'
        )

    generated = time.strftime("%Y-%m-%d %H:%M")
    return f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OCR Examples</title>
<style>
:root {{ --bg:#f6f5f2; --fg:#1d1d1b; --muted:#6b6a66; --card:#fff; --line:#e3e1db; --accent:#c2410c; }}
@media (prefers-color-scheme: dark) {{
  :root {{ --bg:#141413; --fg:#ecebe6; --muted:#9a9890; --card:#1e1e1c; --line:#2e2d2a; --accent:#fb923c; }}
}}
* {{ box-sizing:border-box; }}
body {{ margin:0; background:var(--bg); color:var(--fg); font:15px/1.5 -apple-system, system-ui, sans-serif; }}
main {{ max-width:1200px; margin:0 auto; padding:24px 16px 64px; }}
h1 {{ margin:0 0 4px; font-size:28px; }}
.meta {{ color:var(--muted); margin:0 0 20px; }}
h2 {{ font-size:20px; margin:36px 0 2px; }}
h2 small {{ color:var(--muted); font-weight:500; font-size:15px; }}
.blurb {{ color:var(--muted); margin:0 0 14px; }}
ul.chips {{ list-style:none; padding:0; margin:0; display:flex; flex-wrap:wrap; gap:6px; }}
ul.chips li {{ background:var(--card); border:1px solid var(--line); border-radius:999px; padding:2px 10px; }}
ul.chips b {{ color:var(--muted); font-weight:500; margin-left:6px; }}
code {{ font:600 15px ui-monospace, Menlo, monospace; color:var(--accent); }}
.grid {{ display:grid; grid-template-columns:repeat(auto-fill, minmax(230px, 1fr)); gap:12px; }}
.card {{ margin:0; background:var(--card); border:1px solid var(--line); border-radius:10px; padding:10px; }}
.imgs {{ display:grid; grid-template-columns:2fr 1fr; gap:8px; align-items:start; }}
.imgs img {{ width:100%; display:block; border-radius:6px; image-rendering:pixelated; background:#000; }}
.imgs span {{ display:block; font-size:11px; color:var(--muted); margin-top:2px; }}
figcaption {{ display:flex; justify-content:space-between; align-items:baseline; margin-top:8px; gap:8px; }}
time {{ font-size:12px; color:var(--muted); white-space:nowrap; }}
.none {{ color:var(--muted); }}
</style></head><body><main>
<h1>OCR Examples</h1>
<p class="meta">{len(recs)} samples ({summary}) · generated {generated}</p>
<h2>Readings seen <small>{len(texts)} distinct</small></h2>
<ul class="chips">{chips}</ul>
{''.join(sections)}
</main></body></html>
"""


if __name__ == "__main__":
    out = sys.argv[1] if len(sys.argv) > 1 else "ocr-examples.html"
    with open(out, "w") as fh:
        fh.write(build())
    print(out)
