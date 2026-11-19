#!/usr/bin/env python3
"""Writes internal/testdata/<name>{.png,_resize.png,_tensor.f32,_fixed.png} for Go tests."""
import os
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import cv2  # noqa: E402
import numpy as np  # noqa: E402

from auto_reset import enhance_frame, fix_perspective_frame, get_ocr_reader  # noqa: E402

OUT = "internal/testdata"
os.makedirs(OUT, exist_ok=True)
reader = get_ocr_reader()
reader(cv2.imread(sys.argv[1]), use_det=False, use_cls=False, use_rec=True)  # text_rec loads lazily
rec = reader.text_rec  # rapidocr TextRecognizer
for name, src in (("digit45", sys.argv[1]), ("noise", sys.argv[2])):
    img = cv2.imread(src)
    shutil.copy(src, f"{OUT}/{name}.png")
    h, w = img.shape[:2]
    max_wh = max(320 / 48, w / h)
    tensor = rec.resize_norm_img(img, max_wh).astype(np.float32)  # (3, 48, W)
    tensor.tofile(f"{OUT}/{name}_tensor.f32")
    rw = min(int(48 * max_wh), int(np.ceil(48 * w / h)))
    cv2.imwrite(f"{OUT}/{name}_resize.png", cv2.resize(img, (rw, 48)))
    cv2.imwrite(f"{OUT}/{name}_fixed.png", enhance_frame(fix_perspective_frame(img)))
    print(name, img.shape, "->", tensor.shape)
