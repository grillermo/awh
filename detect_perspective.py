#!/usr/bin/env python3
"""
Run once: detect quad corners on debug/frame.jpg, print hardcoded constants.
"""

import cv2
import numpy as np

DISPLAY_CROP = (0.637, 0.748, 0.682, 0.855)


def _order_points(pts):
    rect = np.zeros((4, 2), dtype=np.float32)
    s = pts.sum(axis=1)
    rect[0] = pts[np.argmin(s)]
    rect[2] = pts[np.argmax(s)]
    diff = np.diff(pts, axis=1)
    rect[1] = pts[np.argmin(diff)]
    rect[3] = pts[np.argmax(diff)]
    return rect


def main():
    img = cv2.imread("debug/frame.jpg")
    if img is None:
        print("ERROR: cannot read debug/frame.jpg")
        return

    h, w = img.shape[:2]
    print(f"Full image: {w}x{h}")

    left_f, top_f, right_f, bottom_f = DISPLAY_CROP
    l, t, r, b = int(w * left_f), int(h * top_f), int(w * right_f), int(h * bottom_f)
    cropped = img[t:b, l:r]
    print(f"Crop region px: l={l} t={t} r={r} b={b}  →  {cropped.shape[1]}x{cropped.shape[0]}")

    gray = cv2.cvtColor(cropped, cv2.COLOR_BGR2GRAY)
    blurred = cv2.GaussianBlur(gray, (5, 5), 0)
    edges = cv2.Canny(blurred, 30, 100)

    contours, _ = cv2.findContours(edges, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
    if not contours:
        print("ERROR: no contours found")
        return

    contours = sorted(contours, key=cv2.contourArea, reverse=True)
    quad = None
    for cnt in contours:
        peri = cv2.arcLength(cnt, True)
        approx = cv2.approxPolyDP(cnt, 0.02 * peri, True)
        if len(approx) == 4:
            quad = approx
            break

    if quad is None:
        print("ERROR: no quadrilateral contour found")
        return

    pts = quad.reshape(4, 2).astype(np.float32)
    rect = _order_points(pts)

    print("\n--- Detected quad (ordered: TL, TR, BR, BL) ---")
    labels = ["top-left", "top-right", "bottom-right", "bottom-left"]
    for label, pt in zip(labels, rect):
        print(f"  {label}: ({pt[0]:.1f}, {pt[1]:.1f})")

    ch, cw = cropped.shape[:2]
    rect_rel = rect / np.array([cw, ch], dtype=np.float32)

    print("\n--- Paste this into auto_reset.py ---")
    print("# Hardcoded quad corners relative to the cropped display region")
    print("# (top-left, top-right, bottom-right, bottom-left)")
    print(f"DISPLAY_QUAD_REL = np.array([")
    for pt in rect_rel:
        print(f"    [{pt[0]:.4f}, {pt[1]:.4f}],")
    print(f"], dtype=np.float32)")

    print("\n--- Absolute px version (for reference, valid only at this resolution) ---")
    print(f"# Crop px: l={l} t={t} r={r} b={b}")
    print(f"DISPLAY_QUAD_PX = np.array([")
    for pt in rect:
        print(f"    [{pt[0]:.1f}, {pt[1]:.1f}],")
    print(f"], dtype=np.float32)")

    # Save visualisation
    vis = cropped.copy()
    for pt in rect.astype(int):
        cv2.circle(vis, tuple(pt), 4, (0, 255, 0), -1)
    cv2.polylines(vis, [rect.astype(int).reshape(-1, 1, 2)], True, (0, 255, 0), 1)
    out_path = "debug/perspective_detection.jpg"
    cv2.imwrite(out_path, vis)
    print(f"\nVisualization saved → {out_path}")


if __name__ == "__main__":
    main()
