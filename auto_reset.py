#!/usr/bin/env python3
"""
Auto water heater reset.
Captures webcam stream, detects error on display, runs macOS shortcut.
"""

import asyncio
import argparse
import base64
import cv2
import functools
import numpy as np
import os
import re
import sqlite3
import sys
import threading
import time
import urllib.request
from datetime import datetime

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
MAX_SAVED_ERRORS = 5


def parse_display_crop(crop_arg):
    parts = [part.strip() for part in crop_arg.split(",")]
    if len(parts) != 4:
        raise ValueError(
            f"DISPLAY_CROP must contain 4 comma-separated values, got {len(parts)}: {crop_arg!r}"
        )

    try:
        return tuple(float(part) for part in parts)
    except ValueError as exc:
        raise ValueError(f"DISPLAY_CROP values must be floats: {crop_arg!r}") from exc


def parse_args():
    parser = argparse.ArgumentParser(description="Auto water heater reset.")
    parser.add_argument(
        "--display-crop",
        default=",".join(str(value) for value in DISPLAY_CROP),
        help="Comma-separated crop fractions: left,top,right,bottom",
    )
    args = parser.parse_args()

    try:
        args.display_crop = parse_display_crop(args.display_crop)
    except ValueError as exc:
        parser.error(str(exc))

    return args


def capture_frame(rtsp_url=RTSP_URL):
    cap = cv2.VideoCapture(rtsp_url)
    if not cap.isOpened():
        print("[capture] ERROR: Failed to open stream", file=sys.stderr)
        return None

    ret, frame = cap.read()
    cap.release()

    if not ret:
        print("[capture] ERROR: Failed to read frame", file=sys.stderr)
        return None

    return frame


def capture_webcam(seconds=2):
    print(f"[capture] Opening RTSP stream for {seconds}s: {RTSP_URL}")
    cap = cv2.VideoCapture(RTSP_URL)
    if not cap.isOpened():
        print("[capture] ERROR: Failed to open stream", file=sys.stderr)
        return []

    fps = cap.get(cv2.CAP_PROP_FPS) or 10
    target = int(fps * seconds)
    frames = []

    start = time.time()
    while time.time() - start < seconds:
        ret, frame = cap.read()
        if not ret:
            print("[capture] WARNING: Failed to read frame, retrying…")
            continue
        frames.append(frame)

    cap.release()
    print(f"[capture] Captured {len(frames)} frames ({fps:.1f} fps reported)")
    return frames


def crop_frame(frame, coords=DISPLAY_CROP):
    left_f, top_f, right_f, bottom_f = coords
    h, w = frame.shape[:2]
    l, t, r, b = int(w * left_f), int(h * top_f), int(w * right_f), int(h * bottom_f)
    return frame[t:b, l:r]


def crop_to_screen(frames, coords=DISPLAY_CROP):
    print(f"[crop] Cropping {len(frames)} frames with relative coords {coords}")
    cropped = []
    for frame in frames:
        cropped.append(crop_frame(frame, coords=coords))
    print(f"[crop] Crop region (first frame): {cropped[0].shape if cropped else 'empty'}")
    return cropped


def fix_perspective_frame(frame):
    h, w = frame.shape[:2]
    src = DISPLAY_QUAD_REL * np.array([w, h], dtype=np.float32)
    w_out = h_out = 200
    dst = np.array([[0, 0], [w_out - 1, 0], [w_out - 1, h_out - 1], [0, h_out - 1]], dtype=np.float32)
    M = cv2.getPerspectiveTransform(src, dst)
    return cv2.warpPerspective(frame, M, (w_out, h_out))


def fix_perspective(frames):
    return [fix_perspective_frame(frame) for frame in frames]


def _frame_to_jpg_bytes(frame, quality=80):
    ok, buf = cv2.imencode(".jpg", frame, [cv2.IMWRITE_JPEG_QUALITY, quality])
    if not ok:
        raise ValueError("Failed to encode frame as JPEG")
    return buf.tobytes()


def get_error_db_connection(path=ERROR_DB_PATH):
    db_path = os.fspath(path)
    db_dir = os.path.dirname(db_path)
    if db_dir:
        os.makedirs(db_dir, exist_ok=True)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    conn.execute(
        """
        CREATE TABLE IF NOT EXISTS errors (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            created_at TEXT NOT NULL,
            ocr_text TEXT NOT NULL,
            raw_image BLOB NOT NULL,
            cropped_image BLOB NOT NULL,
            fixed_image BLOB NOT NULL
        )
        """
    )
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


def save_error(raw_frame, cropped_frame, fixed_frame, ocr_text, path=ERROR_DB_PATH):
    ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    raw_jpg = _frame_to_jpg_bytes(raw_frame)
    crop_jpg = _frame_to_jpg_bytes(cropped_frame)
    fixed_jpg = _frame_to_jpg_bytes(fixed_frame)

    with get_error_db_connection(path) as conn:
        conn.execute(
            """
            INSERT INTO errors (created_at, ocr_text, raw_image, cropped_image, fixed_image)
            VALUES (?, ?, ?, ?, ?)
            """,
            (ts, ocr_text, raw_jpg, crop_jpg, fixed_jpg),
        )
        conn.execute(
            """
            DELETE FROM errors
            WHERE id NOT IN (
                SELECT id
                FROM errors
                ORDER BY id DESC
                LIMIT ?
            )
            """,
            (MAX_SAVED_ERRORS,),
        )
    print(f"[db] Error saved → {path}")


def get_recent_errors(limit=MAX_SAVED_ERRORS, path=ERROR_DB_PATH):
    with get_error_db_connection(path) as conn:
        rows = conn.execute(
            """
            SELECT id, created_at, ocr_text, raw_image, cropped_image, fixed_image
            FROM errors
            ORDER BY id DESC
            LIMIT ?
            """,
            (limit,),
        ).fetchall()

    results = []
    for row in rows:
        results.append(
            {
                "id": row["id"],
                "created_at": row["created_at"],
                "ocr_text": row["ocr_text"],
                "raw_image_b64": base64.b64encode(row["raw_image"]).decode(),
                "cropped_image_b64": base64.b64encode(row["cropped_image"]).decode(),
                "fixed_image_b64": base64.b64encode(row["fixed_image"]).decode(),
            }
        )
    return results


@functools.lru_cache(maxsize=1)
def get_ocr_reader():
    import easyocr
    print("[ocr] Initialising EasyOCR reader (first call downloads model if needed)")
    return easyocr.Reader(["en"], gpu=False, verbose=False)


def classify_ocr_text(text):
    if not text:
        return {"ocr_text": "", "error_text": "", "is_error": False}

    is_error = not bool(re.match(r'^\d', text))
    return {
        "ocr_text": text,
        "error_text": text if is_error else "",
        "is_error": is_error,
    }


def error_showing_on_stream(frames):
    """
    Iterate all frames until one yields non-empty OCR text.
    Error state = first non-empty text does NOT begin with a digit.
    Returns (is_error: bool, ocr_results: list[dict])
    """
    reader = get_ocr_reader()

    print(f"[ocr] Scanning {len(frames)} frames until first non-empty OCR hit")
    for idx, frame in enumerate(frames):
        detections = reader.readtext(frame, detail=0)
        text = " ".join(detections).strip()
        print(f"[ocr] Frame {idx}: detected text = {repr(text)}")
        if not text:
            continue

        is_error = not bool(re.match(r'^\d', text))
        label = "ERROR STATE" if is_error else "no error"
        print(f"[ocr] First non-empty hit at frame {idx}: {repr(text)} → {label}")
        return is_error, [{"frame_idx": idx, "text": text, "error": is_error}]

    print("[ocr] All frames gave empty OCR → treating as no error")
    return False, []


async def _reset_tapo_async():
    from tapo import ApiClient

    client = ApiClient(TAPO_EMAIL, TAPO_PASSWORD)
    device = await client.p100(TAPO_IP)
    print(f"[tapo] Turning off {TAPO_IP}")
    await device.off()
    print("[tapo] Waiting 10 seconds")
    await asyncio.sleep(10)
    print(f"[tapo] Turning on {TAPO_IP}")
    await device.on()
    print("[tapo] Reset complete")


def reset_tapo100():
    print("[tapo] Resetting P100 plug")
    try:
        asyncio.run(_reset_tapo_async())
    except Exception as e:
        print(f"[tapo] ERROR: {e}", file=sys.stderr)


def monitor_and_reset(display_crop=DISPLAY_CROP):
    frames = capture_webcam(seconds=3)
    if not frames:
        raise RuntimeError("Failed to capture frames from RTSP stream")

    cropped = crop_to_screen(frames, coords=display_crop)
    fixed = fix_perspective(cropped)

    is_error, ocr_results = error_showing_on_stream(fixed)

    if is_error:
        print("[ocr] Non-digit detected → re-checking in 3 seconds")
        time.sleep(3)
        frames = capture_webcam(seconds=3)
        if not frames:
            raise RuntimeError("Failed to capture frames from RTSP stream")
        cropped = crop_to_screen(frames, coords=display_crop)
        fixed = fix_perspective(cropped)
        is_error, ocr_results = error_showing_on_stream(fixed)
        print(f"[ocr] Re-check result: {'error' if is_error else 'no error'}")

    if ocr_results:
        fidx = ocr_results[0]["frame_idx"]
        ocr_text = ocr_results[0]["text"]
    else:
        fidx = len(frames) - 1
        ocr_text = ""

    result = {
        "raw_frame": frames[fidx],
        "cropped_frame": cropped[fidx],
        "fixed_frame": fixed[fidx],
        **classify_ocr_text(ocr_text),
    }

    if is_error:
        print("[main] Error detected → resetting P100")
        save_error(frames[fidx], cropped[fidx], fixed[fidx], ocr_results[0]["text"])
        reset_tapo100()
    else:
        print("[main] No error → nothing to do")

    return result


if __name__ == "__main__":
    args = parse_args()
    end_time = time.time() + 1.5 * 3600
    while time.time() < end_time:
        monitor_and_reset(display_crop=args.display_crop)
        remaining = end_time - time.time()
        if remaining > 0:
            time.sleep(min(5, remaining))
