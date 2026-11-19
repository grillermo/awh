#!/usr/bin/env python3
"""
OCR model shoot-out on the hand-picked "45" samples (dataset.json).

    python bench.py <model> [<model> ...]     # one process per model is best
    python bench.py --list

Each model gets 2 warm-up calls, then every image is timed on its own. A hit is
an output whose digits are exactly "45" ("45°C" counts, "4C" does not).
Results land in results/<model>.json; summarize.py builds the table.
"""

import json
import os
import re
import statistics
import subprocess
import sys
import time

import cv2
import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
DATASET = json.load(open(os.environ.get("OCR_BENCH_DATASET", os.path.join(HERE, "dataset.json"))))
TRUTH = "45"


def digits(text):
    return re.sub(r"\D", "", text or "")


# --- preprocessing helpers ----------------------------------------------------

def gray_inverted(path):
    """Dark digits on a light background, which classic OCR engines expect."""
    g = cv2.cvtColor(cv2.imread(path), cv2.COLOR_BGR2GRAY)
    return 255 - g


def otsu_inverted(path):
    g = cv2.GaussianBlur(cv2.cvtColor(cv2.imread(path), cv2.COLOR_BGR2GRAY), (5, 5), 0)
    _, th = cv2.threshold(g, 0, 255, cv2.THRESH_BINARY + cv2.THRESH_OTSU)
    return 255 - th


# --- models -------------------------------------------------------------------
# Each factory returns predict(path) -> str. Imports live inside so one model's
# dependencies never load (or fail) for another.

def m_easyocr():
    import easyocr
    r = easyocr.Reader(["en"], gpu=False, verbose=False)
    return lambda p: " ".join(r.readtext(p, detail=0))


def m_easyocr_digits():
    import easyocr
    r = easyocr.Reader(["en"], gpu=False, verbose=False)
    return lambda p: " ".join(r.readtext(p, detail=0, allowlist="0123456789"))


def m_easyocr_recognize_digits():
    """Skip detection: the crop is already the display, so recognise it whole."""
    import easyocr
    r = easyocr.Reader(["en"], gpu=False, verbose=False)

    def predict(p):
        g = cv2.cvtColor(cv2.imread(p), cv2.COLOR_BGR2GRAY)
        h, w = g.shape
        out = r.recognize(g, horizontal_list=[[0, w, 0, h]], free_list=[], detail=0,
                          allowlist="0123456789")
        return " ".join(out)
    return predict


def _tesseract(psm, prep):
    import pytesseract
    cfg = f"--psm {psm} -c tessedit_char_whitelist=0123456789"
    return lambda p: pytesseract.image_to_string(prep(p), config=cfg).strip()


def m_tesseract_psm7_gray():
    return _tesseract(7, gray_inverted)


def m_tesseract_psm8_otsu():
    return _tesseract(8, otsu_inverted)


def m_ssocr():
    """Seven-segment specific. White digits on dark, 2 digits, Otsu threshold."""
    def predict(p):
        out = subprocess.run(
            ["ssocr", "--foreground=white", "--background=black", "-d", "-1",
             "-T", "remove_isolated", "grayscale", p],
            capture_output=True, text=True)
        return out.stdout.strip()
    return predict


def _rapid(**params):
    from rapidocr import RapidOCR
    engine = RapidOCR(params=params) if params else RapidOCR()

    def predict(p, **kw):
        res = engine(p, **kw)
        return " ".join(res.txts) if res.txts else ""
    return engine, predict


def m_rapidocr():
    _, predict = _rapid()
    return predict


def m_rapidocr_rec_only():
    _, predict = _rapid()
    return lambda p: predict(p, use_det=False, use_cls=False, use_rec=True)


def m_rapidocr_rec_only_raw():
    """Same, but on the raw camera crop instead of the enhanced 200x200 frame."""
    _, predict = _rapid()
    raw = {d["ocr"]: d["raw"] for d in DATASET}
    return lambda p: predict(raw[p], use_det=False, use_cls=False, use_rec=True)


def _trocr(name):
    import torch
    from transformers import TrOCRProcessor, VisionEncoderDecoderModel
    from PIL import Image
    proc = TrOCRProcessor.from_pretrained(name)
    model = VisionEncoderDecoderModel.from_pretrained(name).eval()

    def predict(p):
        px = proc(images=Image.open(p).convert("RGB"), return_tensors="pt").pixel_values
        with torch.inference_mode():
            ids = model.generate(px, max_new_tokens=8)
        return proc.batch_decode(ids, skip_special_tokens=True)[0]
    return predict


def m_trocr_small_printed():
    return _trocr("microsoft/trocr-small-printed")


def m_trocr_base_printed():
    return _trocr("microsoft/trocr-base-printed")


def _florence(name, device):
    import torch
    from transformers import AutoModelForImageTextToText, AutoProcessor
    from PIL import Image
    proc = AutoProcessor.from_pretrained(name)
    model = AutoModelForImageTextToText.from_pretrained(name, torch_dtype=torch.float32).to(device).eval()

    def predict(p):
        inputs = proc(text="<OCR>", images=Image.open(p).convert("RGB"), return_tensors="pt").to(device)
        with torch.inference_mode():
            ids = model.generate(**inputs, max_new_tokens=10, num_beams=1, do_sample=False)
        return proc.batch_decode(ids, skip_special_tokens=True)[0]
    return predict


def m_florence2_base_cpu():
    return _florence("florence-community/Florence-2-base", "cpu")


def m_florence2_base_mps():
    return _florence("florence-community/Florence-2-base", "mps")


def m_florence2_large_mps():
    return _florence("florence-community/Florence-2-large", "mps")


# --- GPU models (run on the PC: OCR_BENCH_DEVICE=cuda) -------------------------

DEVICE = os.environ.get("OCR_BENCH_DEVICE", "cpu")


def m_trocr_large_printed():
    import torch
    from transformers import TrOCRProcessor, VisionEncoderDecoderModel
    from PIL import Image
    name = "microsoft/trocr-large-printed"
    proc = TrOCRProcessor.from_pretrained(name)
    model = VisionEncoderDecoderModel.from_pretrained(name, device_map=DEVICE).eval()

    def predict(p):
        px = proc(images=Image.open(p).convert("RGB"), return_tensors="pt").pixel_values.to(DEVICE)
        with torch.inference_mode():
            ids = model.generate(px, max_new_tokens=8)
        return proc.batch_decode(ids, skip_special_tokens=True)[0]
    return predict


def m_florence2_large():
    return _florence("florence-community/Florence-2-large", DEVICE)


def m_got_ocr2():
    import torch
    from transformers import AutoModelForImageTextToText, AutoProcessor
    from PIL import Image
    name = "stepfun-ai/GOT-OCR-2.0-hf"
    proc = AutoProcessor.from_pretrained(name)
    model = AutoModelForImageTextToText.from_pretrained(name, dtype=torch.bfloat16).to(DEVICE).eval()

    def predict(p):
        inputs = proc(Image.open(p).convert("RGB"), return_tensors="pt").to(DEVICE, dtype=torch.bfloat16)
        with torch.inference_mode():
            ids = model.generate(**inputs, do_sample=False, tokenizer=proc.tokenizer,
                                 stop_strings="<|im_end|>", max_new_tokens=10)
        return proc.decode(ids[0, inputs["input_ids"].shape[1]:], skip_special_tokens=True)
    return predict


VLM_PROMPT = "What number does this display show? Reply with the digits only."


def _vlm(name):
    import torch
    from transformers import AutoModelForImageTextToText, AutoProcessor
    from PIL import Image
    proc = AutoProcessor.from_pretrained(name)
    model = AutoModelForImageTextToText.from_pretrained(name, dtype=torch.bfloat16).to(DEVICE).eval()

    def predict(p):
        msgs = [{"role": "user", "content": [
            {"type": "image", "image": Image.open(p).convert("RGB")},
            {"type": "text", "text": VLM_PROMPT},
        ]}]
        inputs = proc.apply_chat_template(msgs, add_generation_prompt=True, tokenize=True,
                                          return_dict=True, return_tensors="pt").to(DEVICE)
        with torch.inference_mode():
            ids = model.generate(**inputs, max_new_tokens=6, do_sample=False)
        return proc.batch_decode(ids[:, inputs["input_ids"].shape[1]:], skip_special_tokens=True)[0]
    return predict


def m_qwen3_vl_2b():
    return _vlm("Qwen/Qwen3-VL-2B-Instruct")


def m_qwen25_vl_3b():
    return _vlm("Qwen/Qwen2.5-VL-3B-Instruct")


def _vision(level):
    binary = os.path.join(HERE, "vision_ocr")

    def predict(p):
        out = subprocess.run([binary, level, p], capture_output=True, text=True)
        return out.stdout.strip()
    return predict


def m_apple_vision_accurate():
    return _vision("accurate")


def m_apple_vision_fast():
    return _vision("fast")


MODELS = {name[2:]: fn for name, fn in globals().items() if name.startswith("m_")}


def run(name):
    t0 = time.perf_counter()
    predict = MODELS[name]()
    load_s = time.perf_counter() - t0
    for d in DATASET[:2]:
        predict(d["ocr"])  # warm-up

    rows = []
    for d in DATASET:
        t = time.perf_counter()
        text = predict(d["ocr"])
        ms = (time.perf_counter() - t) * 1000
        rows.append({"id": d["id"], "text": text, "ms": round(ms, 1), "hit": digits(text) == TRUTH})

    times = [r["ms"] for r in rows]
    summary = {
        "model": name,
        "hits": sum(r["hit"] for r in rows),
        "total": len(rows),
        "median_ms": round(statistics.median(times), 1),
        "p95_ms": round(sorted(times)[int(0.95 * (len(times) - 1))], 1),
        "max_ms": round(max(times), 1),
        "load_s": round(load_s, 1),
        "rows": rows,
    }
    os.makedirs(os.path.join(HERE, "results"), exist_ok=True)
    json.dump(summary, open(os.path.join(HERE, "results", f"{name}.json"), "w"), indent=1)
    print(f"{name:28s} {summary['hits']:2d}/{summary['total']}  median {summary['median_ms']:7.1f} ms  "
          f"max {summary['max_ms']:7.1f} ms  load {summary['load_s']} s")


if __name__ == "__main__":
    if sys.argv[1:] == ["--list"]:
        print("\n".join(MODELS))
        sys.exit()
    for name in sys.argv[1:]:
        run(name)
