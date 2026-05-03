#!/usr/bin/env python3

import base64
import os
import threading
import time

import cv2
from flask import Flask, jsonify, render_template, request

from auto_reset import DISPLAY_CROP, capture_frame, crop_frame, fix_perspective_frame, get_ocr_reader, get_recent_errors, load_display_crop, load_display_view, monitor_and_reset, reset_tapo100, save_display_crop, save_display_view

app = Flask(__name__)


class _FrameState:
    def __init__(self):
        self.frame_b64 = None
        self.ocr_text = ""
        self.captured_at = None
        self._lock = threading.Lock()

    def update(self, frame_b64, ocr_text):
        with self._lock:
            self.frame_b64 = frame_b64
            self.ocr_text = ocr_text
            self.captured_at = time.time()

    def snapshot(self):
        with self._lock:
            return {
                "frame_b64": self.frame_b64,
                "ocr_text": self.ocr_text,
                "captured_at": self.captured_at,
            }


_state = _FrameState()


def _capture_loop():
    reader = get_ocr_reader()
    crop = load_display_crop()
    while True:
        try:
            frame = capture_frame()
            if frame is not None:
                cropped = crop_frame(frame, coords=crop)
                fixed = fix_perspective_frame(cropped)
                detections = reader.readtext(fixed, detail=0)
                ocr_text = " ".join(detections).strip()
                ok, buf = cv2.imencode(".jpg", fixed, [cv2.IMWRITE_JPEG_QUALITY, 85])
                if ok:
                    b64 = base64.b64encode(buf).decode()
                    _state.update(b64, ocr_text)
        except Exception as exc:
            print(f"[live] capture error: {exc}")


_bg_thread = threading.Thread(target=_capture_loop, daemon=True)
_bg_thread.start()


def encode_jpg(frame, quality=85):
    ok, buf = cv2.imencode(".jpg", frame, [cv2.IMWRITE_JPEG_QUALITY, quality])
    if not ok:
        raise ValueError("Failed to encode frame as JPEG")
    return base64.b64encode(buf).decode()


def parse_display_crop_arg(value):
    if not value:
        return DISPLAY_CROP

    parts = [part.strip() for part in value.split(",")]
    if len(parts) != 4:
        raise ValueError("display_crop must have 4 comma-separated values")

    coords = tuple(float(part) for part in parts)
    if any(part < 0 or part > 1 for part in coords):
        raise ValueError("display_crop values must be between 0 and 1")
    if coords[0] >= coords[2] or coords[1] >= coords[3]:
        raise ValueError("display_crop must satisfy left < right and top < bottom")
    return coords


def load_fresh_frame():
    frame = capture_frame()
    if frame is None:
        raise RuntimeError("Failed to capture frame from RTSP stream")
    return frame


@app.get("/")
def index():
    error = None
    frame_b64 = None
    crop = load_display_crop()

    try:
        frame = load_fresh_frame()
        frame_b64 = encode_jpg(frame)
    except Exception as exc:
        error = str(exc)

    initial_view = load_display_view()
    return render_template("index.html", frame_b64=frame_b64, crop=crop, initial_view=initial_view, error=error)


@app.post("/api/save-crop")
def api_save_crop():
    data = request.get_json(force=True, silent=True) or {}
    crop = data.get("crop")
    if not isinstance(crop, list) or len(crop) != 4:
        return jsonify({"ok": False, "error": "crop must be array of 4 floats"}), 400
    try:
        coords = tuple(float(v) for v in crop)
    except (TypeError, ValueError) as exc:
        return jsonify({"ok": False, "error": str(exc)}), 400
    save_display_crop(coords)
    return jsonify({"ok": True, "crop": list(coords)})


@app.post("/api/save-view")
def api_save_view():
    data = request.get_json(force=True, silent=True) or {}
    view = data.get("view")
    if not isinstance(view, dict) or not all(k in view for k in ("scale", "offsetXFrac", "offsetYFrac")):
        return jsonify({"ok": False, "error": "view must have scale, offsetXFrac, offsetYFrac"}), 400
    try:
        safe = {k: float(view[k]) for k in ("scale", "offsetXFrac", "offsetYFrac")}
    except (TypeError, ValueError) as exc:
        return jsonify({"ok": False, "error": str(exc)}), 400
    save_display_view(safe)
    return jsonify({"ok": True, "view": safe})


@app.get("/api/display")
def display_frame():
    try:
        display_crop = parse_display_crop_arg(request.args.get("display_crop"))
        frame = load_fresh_frame()
        cropped = crop_frame(frame, coords=display_crop)
        fixed = fix_perspective_frame(cropped)
        return jsonify({"ok": True, "fixed_frame_b64": encode_jpg(fixed)})
    except Exception as exc:
        return jsonify({"ok": False, "error": str(exc)}), 500


@app.get("/monitor")
def monitor():
    crop = request.args.get("display_crop", ",".join(str(v) for v in DISPLAY_CROP))
    return render_template("monitor.html", display_crop=crop)


@app.get("/errors-history")
def errors_history():
    return render_template("errors_history.html", errors=get_recent_errors())


@app.get("/api/monitor")
def monitor_data():
    try:
        display_crop = parse_display_crop_arg(request.args.get("display_crop"))
        result = monitor_and_reset(display_crop=display_crop)
        return jsonify(
            {
                "ok": True,
                "display_crop": [round(v, 3) for v in display_crop],
                "cropped_frame_b64": encode_jpg(result["cropped_frame"]),
                "ocr_text": result["ocr_text"],
                "error_text": result["error_text"],
                "is_error": result["is_error"],
            }
        )
    except Exception as exc:
        return jsonify({"ok": False, "error": str(exc)}), 500


@app.get("/last_frame")
def last_frame():
    snap = _state.snapshot()
    if snap["frame_b64"] is None:
        return jsonify({"ok": False, "error": "no frame yet"}), 503
    return jsonify({"ok": True, "frame_b64": snap["frame_b64"], "ocr_text": snap["ocr_text"]})


@app.post("/reset")
def reset():
    threading.Thread(target=reset_tapo100, daemon=True).start()
    return jsonify({"ok": True, "message": "reset initiated"})


@app.get("/live")
def live():
    return render_template("live.html")


if __name__ == "__main__":
    host = os.environ.get("HOST", "0.0.0.0")
    port = int(os.environ.get("PORT", "5000"))
    app.run(host=host, port=port)
