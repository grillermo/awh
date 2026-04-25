#!/usr/bin/env python3
"""
Auto water heater reset.
Captures webcam stream, detects error on display, runs macOS shortcut.
"""

import asyncio
import base64
import cv2
import numpy as np
import os
import sys
import time
from datetime import datetime
from tapo import ApiClient

RTSP_URL = "rtsp://grillermo:123456789@192.168.1.21/stream2"
TAPO_IP = "192.168.1.86"
TAPO_EMAIL = os.environ.get("TAPO_EMAIL", "guillermo.siliceo@gmail.com")
TAPO_PASSWORD = os.environ.get("TAPO_PASSWORD", "2qEP@Pxiy32*qtd")

# Crop coords for the circular display (from crop_display.py, tuned for 640x480)
# Expressed as fractions so resolution changes don't break them
DISPLAY_CROP = (0.637, 0.748, 0.682, 0.855)  # left, top, right, bottom


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


def crop_to_screen(frames, coords=DISPLAY_CROP):
    print(f"[crop] Cropping {len(frames)} frames with relative coords {coords}")
    left_f, top_f, right_f, bottom_f = coords
    cropped = []
    for frame in frames:
        h, w = frame.shape[:2]
        l, t, r, b = int(w * left_f), int(h * top_f), int(w * right_f), int(h * bottom_f)
        cropped.append(frame[t:b, l:r])
    print(f"[crop] Crop region (first frame): {cropped[0].shape if cropped else 'empty'}")
    return cropped


def fix_perspective(frames):
    """
    For each frame find the largest quadrilateral contour (the display border),
    then warp it to a flat square for easier OCR.
    Falls back to original crop if contour detection fails.
    """
    print(f"[perspective] Processing {len(frames)} frames")
    results = []
    for i, frame in enumerate(frames):
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        blurred = cv2.GaussianBlur(gray, (5, 5), 0)
        edges = cv2.Canny(blurred, 30, 100)

        contours, _ = cv2.findContours(edges, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
        if not contours:
            print(f"[perspective] Frame {i}: no contours, using raw crop")
            results.append(frame)
            continue

        # Largest contour by area
        contours = sorted(contours, key=cv2.contourArea, reverse=True)
        quad = None
        for cnt in contours:
            peri = cv2.arcLength(cnt, True)
            approx = cv2.approxPolyDP(cnt, 0.02 * peri, True)
            if len(approx) == 4:
                quad = approx
                break

        if quad is None:
            print(f"[perspective] Frame {i}: no quad found, using raw crop")
            results.append(frame)
            continue

        pts = quad.reshape(4, 2).astype(np.float32)
        # Order: top-left, top-right, bottom-right, bottom-left
        rect = _order_points(pts)
        w_out = h_out = 200
        dst = np.array([[0, 0], [w_out - 1, 0], [w_out - 1, h_out - 1], [0, h_out - 1]], dtype=np.float32)
        M = cv2.getPerspectiveTransform(rect, dst)
        warped = cv2.warpPerspective(frame, M, (w_out, h_out))
        print(f"[perspective] Frame {i}: warped to {warped.shape}")
        results.append(warped)

    return results


def _order_points(pts):
    rect = np.zeros((4, 2), dtype=np.float32)
    s = pts.sum(axis=1)
    rect[0] = pts[np.argmin(s)]   # top-left
    rect[2] = pts[np.argmax(s)]   # bottom-right
    diff = np.diff(pts, axis=1)
    rect[1] = pts[np.argmin(diff)]  # top-right
    rect[3] = pts[np.argmax(diff)]  # bottom-left
    return rect


def error_showing_on_stream(frames):
    """
    Iterate all frames until one yields non-empty OCR text.
    Error state = first non-empty text does NOT begin with a digit.
    Returns (is_error: bool, ocr_results: list[dict])
    """
    import re
    import easyocr
    print("[ocr] Initialising EasyOCR reader (first call downloads model if needed)")
    reader = easyocr.Reader(["en"], gpu=False, verbose=False)

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


def _frame_to_b64(frame):
    _, buf = cv2.imencode(".jpg", frame, [cv2.IMWRITE_JPEG_QUALITY, 80])
    return base64.b64encode(buf).decode()


def save_debug_html(raw_frames, cropped_frames, fixed_frames, ocr_results, is_error, path="debug/index.html"):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    print(f"[html] Building debug page → {path}")
    ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    status_color = "#c0392b" if is_error else "#27ae60"
    status_label = "ERROR DETECTED" if is_error else "OK — no error"

    # Build OCR lookup: frame_idx → result dict
    ocr_map = {r["frame_idx"]: r for r in ocr_results}

    # Sample frames for display (cap at 30 to keep page size sane)
    total = len(raw_frames)
    step = max(1, total // 30)
    indices = list(range(0, total, step))

    rows_html = []
    for idx in indices:
        raw_b64 = _frame_to_b64(raw_frames[idx])
        crop_b64 = _frame_to_b64(cropped_frames[idx])
        fixed_b64 = _frame_to_b64(fixed_frames[idx])
        ocr = ocr_map.get(idx)
        ocr_cell = ""
        if ocr:
            color = "#c0392b" if ocr["error"] else "#27ae60"
            ocr_cell = f'<td style="color:{color};font-weight:bold">{ocr["text"] or "(empty)"}</td>'
        else:
            ocr_cell = "<td>—</td>"

        rows_html.append(f"""
        <tr>
          <td class="idx">#{idx}</td>
          <td><img src="data:image/jpeg;base64,{raw_b64}"></td>
          <td><img src="data:image/jpeg;base64,{crop_b64}"></td>
          <td><img src="data:image/jpeg;base64,{fixed_b64}"></td>
          {ocr_cell}
        </tr>""")

    html = f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Water Heater Debug — {ts}</title>
<style>
  body {{ font-family: monospace; background: #111; color: #eee; margin: 0; padding: 16px; }}
  h1 {{ margin: 0 0 4px; }}
  .status {{ display: inline-block; padding: 4px 12px; border-radius: 4px;
             background: {status_color}; color: #fff; font-size: 1.1em; margin-bottom: 16px; }}
  table {{ border-collapse: collapse; }}
  th, td {{ padding: 4px 8px; border: 1px solid #333; vertical-align: middle; text-align: center; }}
  th {{ background: #222; }}
  td.idx {{ color: #888; font-size: 0.85em; }}
  img {{ max-height: 120px; display: block; }}
</style>
</head>
<body>
<h1>Water Heater Debug</h1>
<p>{ts} &nbsp;|&nbsp; {total} frames captured &nbsp;|&nbsp; showing {len(indices)}</p>
<div class="status">{status_label}</div>
<table>
  <thead>
    <tr><th>#</th><th>Raw</th><th>Cropped</th><th>Perspective fixed</th><th>OCR text</th></tr>
  </thead>
  <tbody>
    {"".join(rows_html)}
  </tbody>
</table>
</body>
</html>"""

    with open(path, "w") as f:
        f.write(html)
    print(f"[html] Saved {path} ({len(html) // 1024} KB)")


async def _reset_tapo_async():
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


def main():
    frames = capture_webcam(seconds=3)
    if not frames:
        print("No frames captured. Exiting.")
        sys.exit(1)

    cropped = crop_to_screen(frames)
    fixed = fix_perspective(cropped)

    is_error, ocr_results = error_showing_on_stream(fixed)
    save_debug_html(frames, cropped, fixed, ocr_results, is_error)

    if is_error:
        print("[main] Error detected → resetting P100")
        reset_tapo100()
    else:
        print("[main] No error → nothing to do")


if __name__ == "__main__":
    end_time = time.time() + 1.5 * 3600
    while time.time() < end_time:
        main()
        remaining = end_time - time.time()
        if remaining > 0:
            time.sleep(min(5, remaining))
