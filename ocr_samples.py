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

# Disk budget for the whole ocr_samples/ folder. Once it is used up, no more
# samples are saved; delete some to make room.
MAX_BYTES = 50 * 1024 * 1024

_NUMBER_RE = re.compile(r"\d+")


def classify(ocr_text):
    """'number', 'digit' (a lone single digit), 'empty' or 'text'."""
    compact = re.sub(r"\s+", "", ocr_text or "")
    if not compact:
        return "empty"
    if _NUMBER_RE.fullmatch(compact):
        return "digit" if len(compact) == 1 else "number"
    return "text"


def _dir_size(root):
    total = 0
    for dirpath, _dirs, files in os.walk(root):
        for name in files:
            try:
                total += os.path.getsize(os.path.join(dirpath, name))
            except OSError:
                pass
    return total


def _slug(text, limit=40):
    slug = re.sub(r"[^A-Za-z0-9]+", "-", text).strip("-")
    return slug[:limit] or "x"


class SampleCollector:
    def __init__(self, root=SAMPLES_DIR, rng=None, clock=time.time, max_bytes=MAX_BYTES):
        self.root = root
        self.max_bytes = max_bytes
        self._used_bytes = _dir_size(root)
        self._budget_warned = False
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
        if kind == "number":
            return None
        now = self.clock()
        with self._lock:
            if self._used_bytes >= self.max_bytes:
                if not self._budget_warned:
                    print(f"[samples] disk budget of {self.max_bytes // (1024 * 1024)} MB used up; not saving more samples")
                    self._budget_warned = True
                return None
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

    def maybe_save(self, ocr_text, raw, processed, score=None):
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
        written = os.path.getsize(raw_path) + os.path.getsize(proc_path)
        rec = {"at": now, "kind": kind, "ocr_text": ocr_text.strip(), "raw": raw_path, "ocr": proc_path}
        if score is not None:
            rec["score"] = round(score, 3)
        line = json.dumps(rec) + "\n"
        with self._lock, open(os.path.join(self.root, "index.jsonl"), "a") as fh:
            fh.write(line)
            self._used_bytes += written + len(line.encode())
        print(f"[samples] saved {kind} sample {ocr_text.strip()!r} → {proc_path}")
        return rec


# --- Browsing and picking samples (served to the iOS app) -------------------

def sample_id(rec):
    """Stable id for a sample: '<kind>/<file stem>', e.g. 'text/20261106-182945_4C'."""
    stem = os.path.basename(rec["ocr"])
    if stem.endswith("_ocr.png"):
        stem = stem[: -len("_ocr.png")]
    return f"{rec['kind']}/{stem}"


def list_samples(root=SAMPLES_DIR):
    """Every sample in index.jsonl whose images still exist, newest first."""
    path = os.path.join(root, "index.jsonl")
    if not os.path.exists(path):
        return []
    out = []
    with open(path) as fh:
        for line in fh:
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if not os.path.exists(rec.get("ocr", "")):
                continue
            out.append({
                "id": sample_id(rec),
                "kind": rec["kind"],
                "ocr_text": rec["ocr_text"],
                "at": rec["at"],
                "ocr_image": os.path.relpath(rec["ocr"], root),
                "raw_image": os.path.relpath(rec["raw"], root),
            })
    out.sort(key=lambda r: r["at"], reverse=True)
    return out


def _selection_table(conn):
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS ocr_sample_selection (
            sample_id TEXT PRIMARY KEY,
            selected_at REAL NOT NULL
        )
        """
    )


def load_selection(conn):
    _selection_table(conn)
    return {row[0] for row in conn.execute("SELECT sample_id FROM ocr_sample_selection")}


def update_selection(conn, select=(), deselect=(), now=None):
    _selection_table(conn)
    now = time.time() if now is None else now
    conn.executemany(
        "INSERT OR IGNORE INTO ocr_sample_selection (sample_id, selected_at) VALUES (?, ?)",
        [(sid, now) for sid in select],
    )
    conn.executemany(
        "DELETE FROM ocr_sample_selection WHERE sample_id = ?",
        [(sid,) for sid in deselect],
    )
    conn.commit()
