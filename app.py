#!/usr/bin/env python3

import base64
import os

import cv2
from flask import Flask, jsonify, render_template, request

from auto_reset import DISPLAY_CROP, capture_frame, get_recent_errors, load_display_crop, load_display_view, monitor_and_reset, save_display_crop, save_display_view

app = Flask(__name__)


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


if __name__ == "__main__":
    host = os.environ.get("HOST", "0.0.0.0")
    port = int(os.environ.get("PORT", "5000"))
    app.run(host=host, port=port, debug=True)
