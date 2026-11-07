#!/usr/bin/env python3
"""
Auto water heater reset.
Captures webcam stream, detects error on display, runs macOS shortcut.
"""

import asyncio
import cv2
import functools
import numpy as np
import os
import sqlite3
import sys

RTSP_URL = "rtsp://grillermo:123456789@192.168.1.21/stream1"
TAPO_IP = "192.168.1.86"
TAPO_EMAIL = os.environ.get("TAPO_EMAIL", "guillermo.siliceo@gmail.com")
TAPO_PASSWORD = os.environ.get("TAPO_PASSWORD", "2qEP@Pxiy32*qtd")

# Crop coords for the circular display (from crop_display.py, tuned for 640x480)
# Expressed as fractions so resolution changes don't break them
DISPLAY_CROP = ((0.631, 0.585, 0.676, 0.691))  # left, top, right, bottom

# Hardcoded quad corners relative to the cropped display region
# (top-left, top-right, bottom-right, bottom-left) — detected once via detect_perspective.py
DISPLAY_QUAD_REL = np.array([
    [0.2759, 0.2895],
    [0.7931, 0.1316],
    [0.7586, 0.6579],
    [0.2069, 0.8421],
], dtype=np.float32)
ERROR_DB_PATH = "errors.sqlite3"


def capture_frame(rtsp_url=RTSP_URL):
    cap = cv2.VideoCapture(rtsp_url)
    if not cap.isOpened():
        print("[capture] ERROR: Failed to open stream", file=sys.stderr)
        return None

    ret, frame = cap.read()
    cap.release()

    if not ret or frame is None or frame.size == 0:
        print("[capture] ERROR: Failed to read frame", file=sys.stderr)
        return None

    return frame


def crop_frame(frame, coords=DISPLAY_CROP):
    left_f, top_f, right_f, bottom_f = coords
    h, w = frame.shape[:2]
    l, t, r, b = int(w * left_f), int(h * top_f), int(w * right_f), int(h * bottom_f)
    cropped = frame[t:b, l:r]
    if cropped.size == 0:
        raise ValueError(f"crop_frame produced empty array: frame={w}x{h}, crop=l{l}:r{r},t{t}:b{b} from coords={coords}")
    return cropped


def fix_perspective_frame(frame):
    h, w = frame.shape[:2]
    src = DISPLAY_QUAD_REL * np.array([w, h], dtype=np.float32)
    w_out = h_out = 200
    dst = np.array([[0, 0], [w_out - 1, 0], [w_out - 1, h_out - 1], [0, h_out - 1]], dtype=np.float32)
    M = cv2.getPerspectiveTransform(src, dst)
    return cv2.warpPerspective(frame, M, (w_out, h_out))


# Auto-exposure limits. Gamma outside this range means the frame is so dark (or
# so blown out) that pushing it to mid-grey would mostly amplify sensor noise —
# and noise that OCRs as text triggers a false reset, so we cap instead.
ENHANCE_GAMMA_RANGE = (0.35, 3.0)
ENHANCE_TARGET_MEAN = 0.5
# A genuinely dark but readable display still spreads p1..p99 over 50+ levels;
# below ~24 there is no signal to recover and the stretch would just gain up noise.
ENHANCE_MIN_SPREAD = 24


def enhance_frame(frame):
    """
    Auto contrast + luminosity for OCR: percentile stretch, then gamma to pull
    mean luminance toward mid-grey.

    Stats come from the luma channel and the resulting curve is applied as a
    single LUT to every channel, so the transform is global and monotone: it
    cannot invent structure that was not already in the frame.
    """
    gray = frame if frame.ndim == 2 else cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
    lo, hi = np.percentile(gray, (1, 99))

    ramp = np.arange(256, dtype=np.float32)
    if hi - lo >= ENHANCE_MIN_SPREAD:
        ramp = np.clip((ramp - lo) * (255.0 / (hi - lo)), 0, 255)
    else:
        print(f"[enhance] Flat frame (p1={lo:.0f}, p99={hi:.0f}) → skipping stretch")

    # Mean of the stretched image, without materialising it
    hist = np.bincount(gray.reshape(-1), minlength=256).astype(np.float32)
    mean = float(np.dot(ramp, hist) / hist.sum()) / 255.0
    mean = min(max(mean, 0.02), 0.98)  # keep log() finite
    gamma = np.log(ENHANCE_TARGET_MEAN) / np.log(mean)
    gamma = min(max(gamma, ENHANCE_GAMMA_RANGE[0]), ENHANCE_GAMMA_RANGE[1])

    lut = np.rint(np.clip(255.0 * (ramp / 255.0) ** gamma, 0, 255)).astype(np.uint8)
    print(f"[enhance] p1={lo:.0f} p99={hi:.0f} mean={mean:.2f} gamma={gamma:.2f}")
    return cv2.LUT(frame, lut)


def get_error_db_connection(path=ERROR_DB_PATH):
    db_path = os.fspath(path)
    db_dir = os.path.dirname(db_path)
    if db_dir:
        os.makedirs(db_dir, exist_ok=True)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS settings (
            key TEXT PRIMARY KEY,
            value TEXT NOT NULL
        )
        """
    )
    return conn


def save_display_crop(coords, path=ERROR_DB_PATH):
    import json
    with get_error_db_connection(path) as conn:
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES ('display_crop', ?)",
            (json.dumps(list(coords)),),
        )


def load_display_crop(path=ERROR_DB_PATH):
    import json
    try:
        with get_error_db_connection(path) as conn:
            row = conn.execute(
                "SELECT value FROM settings WHERE key = 'display_crop'"
            ).fetchone()
        if row:
            return tuple(json.loads(row["value"]))
    except Exception:
        pass
    return DISPLAY_CROP


def save_display_view(view, path=ERROR_DB_PATH):
    import json
    with get_error_db_connection(path) as conn:
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES ('display_view', ?)",
            (json.dumps(view),),
        )


def load_display_view(path=ERROR_DB_PATH):
    import json
    try:
        with get_error_db_connection(path) as conn:
            row = conn.execute(
                "SELECT value FROM settings WHERE key = 'display_view'"
            ).fetchone()
        if row:
            return json.loads(row["value"])
    except Exception:
        pass
    return None


@functools.lru_cache(maxsize=1)
def get_ocr_reader():
    """
    RapidOCR's PP-OCRv6 recogniser, run without text detection: the crop is
    already the display. In the ocr_bench shoot-out (Nov 2026) it read 33/33
    hand-picked "45" frames in ~10 ms on the raw crop; EasyOCR read 6/33.
    """
    from rapidocr import RapidOCR
    print("[ocr] Initialising RapidOCR (recognition only)")
    return RapidOCR()


def read_display(reader, cropped):
    """OCR the raw display crop. Returns (text, confidence 0..1)."""
    res = reader(cropped, use_det=False, use_cls=False, use_rec=True)
    if not res.txts:
        return "", 0.0
    return res.txts[0], float(res.scores[0])


def reset_tapo100():
    from tapo import ApiClient

    async def _do():
        client = ApiClient(TAPO_EMAIL, TAPO_PASSWORD)
        device = await client.p100(TAPO_IP)
        print(f"[tapo] Turning off {TAPO_IP}")
        await device.off()
        print("[tapo] Waiting 10 seconds")
        await asyncio.sleep(10)
        print(f"[tapo] Turning on {TAPO_IP}")
        await device.on()
        print("[tapo] Reset complete")

    print("[tapo] Resetting P100 plug")
    try:
        asyncio.run(_do())
    except Exception as e:
        print(f"[tapo] ERROR: {e}", file=sys.stderr)
