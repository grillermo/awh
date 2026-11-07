"""
Collects sample images of OCR misses, for tuning the pipeline later.

A reading is a miss when it is not a plain number. Misses where OCR read
*something* (a letter, a word, digits mixed with junk) are the valuable ones:
every distinct text gets saved, up to a few frames each, spaced apart so the
copies differ. Misses where OCR read nothing are common and mostly alike, so
only a small random share of them is kept, with a hard cap.

A lone single digit ("5", "6") is a number but almost always a misread of the
two-digit display, so those are kept like texts, for comparing OCR models.

Samples land in ocr_samples/{text,digit,empty}/ as the raw crop and the enhanced
frame OCR actually saw, plus one JSON line per sample in ocr_samples/index.jsonl.
"""

import json
import os
import random
import re
import threading
import time

import cv2

SAMPLES_DIR = "ocr_samples"

# Per distinct text: how many frames to keep and how far apart they must be.
TEXT_MAX_PER_TEXT = 3
TEXT_MIN_INTERVAL_S = 60
# Empty readings: keep ~1 in 50, at most one every 10 minutes, 40 in total.
EMPTY_PROBABILITY = 0.02
EMPTY_MIN_INTERVAL_S = 600
EMPTY_MAX_TOTAL = 40

_NUMBER_RE = re.compile(r"\d+")


def classify(ocr_text):
    """'number', 'digit' (a lone single digit), 'empty' or 'text'."""
    compact = re.sub(r"\s+", "", ocr_text or "")
    if not compact:
        return "empty"
    if _NUMBER_RE.fullmatch(compact):
        return "digit" if len(compact) == 1 else "number"
    return "text"


def _slug(text, limit=40):
    slug = re.sub(r"[^A-Za-z0-9]+", "-", text).strip("-")
    return slug[:limit] or "x"


class SampleCollector:
    def __init__(self, root=SAMPLES_DIR, rng=None, clock=time.time):
        self.root = root
        self.rng = rng or random.Random()
        self.clock = clock
        self._lock = threading.Lock()
        self._text_seen = {}  # text -> (count, last_saved_at)
        self._empty_count = 0
        self._empty_last = 0.0
        self._load_existing()

    def _load_existing(self):
        """Rebuild counters from index.jsonl so restarts don't re-save the same texts."""
        path = os.path.join(self.root, "index.jsonl")
        if not os.path.exists(path):
            return
        with open(path) as fh:
            for line in fh:
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                if rec.get("kind") in ("text", "digit"):
                    count, last = self._text_seen.get(rec["ocr_text"], (0, 0.0))
                    self._text_seen[rec["ocr_text"]] = (count + 1, max(last, rec["at"]))
                elif rec.get("kind") == "empty":
                    self._empty_count += 1
                    self._empty_last = max(self._empty_last, rec["at"])

    def should_save(self, ocr_text):
        """Decide, and reserve the slot if yes. Returns the kind to save as, or None."""
        kind = classify(ocr_text)
        now = self.clock()
        with self._lock:
            if kind in ("text", "digit"):
                key = ocr_text.strip()
                count, last = self._text_seen.get(key, (0, 0.0))
                if count >= TEXT_MAX_PER_TEXT or now - last < TEXT_MIN_INTERVAL_S:
                    return None
                self._text_seen[key] = (count + 1, now)
                return kind
            if kind == "empty":
                if self._empty_count >= EMPTY_MAX_TOTAL or now - self._empty_last < EMPTY_MIN_INTERVAL_S:
                    return None
                if self.rng.random() >= EMPTY_PROBABILITY:
                    return None
                self._empty_count += 1
                self._empty_last = now
                return kind
        return None

    def maybe_save(self, ocr_text, raw, processed):
        kind = self.should_save(ocr_text)
        if kind is None:
            return None
        now = self.clock()
        stamp = time.strftime("%Y%m%d-%H%M%S", time.localtime(now))
        base = f"{stamp}_{'empty' if kind == 'empty' else _slug(ocr_text)}"
        folder = os.path.join(self.root, kind)
        os.makedirs(folder, exist_ok=True)
        raw_path = os.path.join(folder, f"{base}_raw.png")
        proc_path = os.path.join(folder, f"{base}_ocr.png")
        cv2.imwrite(raw_path, raw)
        cv2.imwrite(proc_path, processed)
        rec = {"at": now, "kind": kind, "ocr_text": ocr_text.strip(), "raw": raw_path, "ocr": proc_path}
        with self._lock, open(os.path.join(self.root, "index.jsonl"), "a") as fh:
            fh.write(json.dumps(rec) + "\n")
        print(f"[samples] saved {kind} sample {ocr_text.strip()!r} → {proc_path}")
        return rec
