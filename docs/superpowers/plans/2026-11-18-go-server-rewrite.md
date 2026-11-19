# AWH Go Server Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Flask server (`app.py`, `auto_reset.py`, `display_reading.py`, `ocr_samples.py`) with one Go binary that serves the same HTTP API with the same OCR model, to cut the server's RAM. Before/after evidence covers OCR results, memory and latency.

**Why (motivation):** RAM. On 2026-11-18 the running server (pid under `./serve` on the macmini) showed:

| Measure | Value |
|---|---|
| Physical footprint | 116 MB (flat over 1 min, idle) |
| Physical footprint, peak | 189 MB |
| Live malloc | 56 MB, 11% fragmentation |
| Uptime | 11 days |

Floor measurements in the same `python_env`:

| Loaded | Footprint |
|---|---|
| bare python | 8.5 MB |
| + numpy | 18 MB |
| + cv2 | 26 MB |
| + flask | 42 MB |
| numpy + onnxruntime, before session | 25 MB |
| + `PP-OCRv6_rec_small.onnx` session | 57 MB (onnxruntime + model ≈ 32 MB) |
| + 20 inferences | 63 MB default, 57 MB with arena and mem pattern off, 1 thread |

**Expected after:** Go runtime + HTTP + SQLite ≈ 10–15 MB, plus onnxruntime + model ≈ 32 MB, so about **45–55 MB steady**, against 116 MB. Camera decoding moves into a short-lived `ffmpeg` child that exits after each frame, so its memory is returned every time and never piles up in the server.

**Leak suspicion:** 116 MB is about 40 MB above the sum of the parts, and the peak was 189 MB. Task 1 records a 48-hour footprint time series of the Python server under real polling. If it climbs steadily, that is a leak. Task 1 ends with a decision point: a Python fix might be enough, and that call is the user's.

**Architecture:** A single Go module at the repo root. `cmd/awh` is the server, and `internal/*` packages port each Python module one-to-one: `reading`, `vision`, `ocr`, `capture`, `store`, `samples`, `tapo`, `server`. OCR runs the **same ONNX file** RapidOCR uses (`PP-OCRv6_rec_small.onnx`) through onnxruntime's C library via `github.com/yalue/onnxruntime_go`. Preprocessing and CTC decoding are ported line for line from `rapidocr/ch_ppocr_rec`. Frames come from `ffmpeg`/`ffprobe` subprocesses; the live loop asks ffmpeg for only the display crop (`format=bgr24,crop=…`), so the Go heap never holds a full 1440p frame. Python stays in the repo, and runnable, until a week after cutover.

**Tech Stack:** Go ≥ 1.25 (stdlib `net/http` with method patterns, `html/template`, `embed`), `github.com/yalue/onnxruntime_go`, Homebrew `onnxruntime`, `modernc.org/sqlite` (pure Go), Homebrew `ffmpeg`. The eval harness is Python in `python_env`.

**Where to work:** everything runs **on the macmini** (`command ssh macmini`, then `cd ~/c/awh`). The camera (192.168.1.21), the plug (192.168.1.86), `ocr_samples/`, `errors.sqlite3`, `python_env` and the running server are all there. On 2026-11-18 both checkouts (immunemac and macmini) were leveled to `4a71822` on `github/main`. git-sync was not carrying this repo, so after each commit run `git push github main` on the machine you committed on, and `git fetch github && git merge --ff-only github/main` on the other.

**Never** toggle the Tapo plug outside Task 11's explicit, user-approved step. Turning it off cuts power to the water heater. Every Go run before cutover uses `-dry-run`.

---

## Evaluation design (read before starting)

"Before" is always the **current Python code, unmodified**. "After" is the Go binary. Every comparison feeds both the **same input**.

| Layer | Input | Compared | Gate (must hold to cut over) |
|---|---|---|---|
| E1 OCR offline parity | ~274 `ocr_samples/*/*_raw.png` + 5 error crops from `errors.sqlite3` + 33 truth-labelled frames (`ocr_bench/dataset.json`) | text, score and `classify()` kind per image | kind agrees on **100%**; **0** images where Go says `error` and Python doesn't; text agrees ≥ 99% with every mismatch listed; \|Δscore\| p99 ≤ 0.01; truth set 33/33 on both |
| E2 tensor parity | 2 fixture crops | preprocessed `1×3×48×W` tensor | max \|Δ\| ≤ 2/255·2 (≈ 1 grey level after normalisation) |
| E3 OCR live parity | crops the Go shadow server saves every 30 s over 24 h, including night | same as E1, run offline through both | same as E1 |
| E4 live A/B | Python `:3060` and Go `:3061` polled together every 2 s for 24 h | `ocr_kind` per poll | ≥ 98% agreement; every Go-only `error` reviewed by hand |
| E5 memory | each server, idle 10 min and active (poller at live.html's ~1 s cadence) 30 min, then 24 h active | footprint of the process **plus its children** | Go active median ≤ 60 MB, idle ≤ 50 MB, 24 h slope ≈ 0. **Stop and report** if the Go active median exceeds 80 MB |
| E6 latency | E1 set | OCR ms per image (after 2 warm-ups); cold start to first `/last_frame` 200 | Go OCR median ≤ 2× Python's and < 50 ms |
| E7 HTTP contract | every read-only endpoint on both servers | status codes and JSON key sets | identical |

Results go in `eval/results/{before,after}/`. `eval/REPORT.md` is the before/after write-up, built in Task 18.

## File structure

```
go.mod, go.sum
cmd/awh/main.go                    # flags, env, wiring, signal handling
cmd/awh-ocr-eval/main.go           # E1/E3 runner: manifest.jsonl -> results.jsonl
internal/reading/reading.go        # port of display_reading.py
internal/vision/image.go           # BGR image type, crop, PNG/JPEG
internal/vision/resize.go          # cv2.resize INTER_LINEAR (8-bit fixed point)
internal/vision/enhance.go         # port of enhance_frame
internal/vision/perspective.go     # port of fix_perspective_frame
internal/ocr/preprocess.go         # rapidocr resize_norm_img
internal/ocr/decode.go             # rapidocr CTCLabelDecode
internal/ocr/recognizer.go         # onnxruntime session
internal/capture/ffmpeg.go         # ffprobe size, ffmpeg crop / full JPEG
internal/store/store.go            # errors.sqlite3: settings + selection
internal/samples/samples.go        # port of ocr_samples.py
internal/tapo/klap.go              # Tapo P100 KLAP client (off / wait / on)
internal/server/server.go          # handlers, frame state, capture loop
internal/server/shadow.go          # E3 crop recorder
web/web.go                         # embed templates
web/templates/index.html           # Go-template copy of templates/index.html
web/templates/live.html            # verbatim copy of templates/live.html
models/ppocrv6_rec_keys.json       # character list, extracted from the ONNX metadata
models/PP-OCRv6_rec_small.onnx     # gitignored; copied by scripts/fetch-model
scripts/fetch-model
serve-go, serve-python
eval/memwatch.sh, eval/mem_summary.py, eval/poll.sh, eval/startup.sh
eval/build_manifest.py, eval/ocr_reference.py, eval/compare_ocr.py
eval/ab_poll.py, eval/http_contract.sh, eval/dump_fixtures.py
eval/REPORT.md
```

---

### Task 0: Prepare the macmini

**Files:**
- Modify: `.gitignore`
- Add: `ocr_bench/` sources (currently untracked on the macmini)

- [ ] **Step 1: Install toolchains**

```bash
brew install go onnxruntime ffmpeg
go version && ffmpeg -version | head -1 && ls /opt/homebrew/lib/libonnxruntime.dylib
python_env/bin/python -c "import onnxruntime; print(onnxruntime.__version__)"
brew list --versions onnxruntime
```

Expected: all four print. Record both onnxruntime versions (Python was 1.31.1) in `eval/REPORT.md` later. A version mismatch can shift scores in the 4th–5th decimal, and E1 measures exactly that.

- [ ] **Step 2: Ignore the big and generated things**

Append to `.gitignore`:

```
ocr_bench/python_env/
ocr_bench/__pycache__/
models/*.onnx
eval/shadow/
eval/images/
/awh
/awh-ocr-eval
```

- [ ] **Step 3: Commit the existing bench (its dataset is E1's truth set)**

```bash
git add .gitignore ocr_bench
git status --short ocr_bench | head
git commit -m "chore: track ocr_bench sources and results, ignore its venv"
```

Expected: `ocr_bench/python_env` is not listed.

---

### Task 1: "Before" memory baseline and leak check (Python, untouched)

**Files:**
- Create: `eval/memwatch.sh`, `eval/mem_summary.py`, `eval/poll.sh`, `eval/startup.sh`

- [ ] **Step 1: Write `eval/memwatch.sh`**

```bash
#!/usr/bin/env bash
# Samples the macOS physical footprint of a process plus its direct children.
# usage: eval/memwatch.sh <pid> <seconds> <interval_s> <out.csv>
set -euo pipefail
pid=$1; secs=$2; every=$3; out=$4

footprint_mb() {
  footprint "$1" 2>/dev/null | awk '{
    for (i = 1; i < NF; i++) if ($i == "Footprint:") {
      v = $(i + 1); u = $(i + 2)
      if (u == "KB") v /= 1024; else if (u == "GB") v *= 1024
      print v; exit
    }}'
}

echo "epoch,self_mb,children_mb,total_mb" > "$out"
end=$((SECONDS + secs))
while [ "$SECONDS" -lt "$end" ]; do
  kill -0 "$pid" 2>/dev/null || { echo "pid $pid gone" >&2; exit 1; }
  self=$(footprint_mb "$pid"); self=${self:-0}
  kids=0
  for c in $(pgrep -P "$pid" || true); do
    k=$(footprint_mb "$c"); kids=$(echo "$kids + ${k:-0}" | bc)
  done
  echo "$(date +%s),$self,$kids,$(echo "$self + $kids" | bc)" >> "$out"
  sleep "$every"
done
```

- [ ] **Step 2: Write `eval/mem_summary.py`**

```python
#!/usr/bin/env python3
"""usage: mem_summary.py <memwatch.csv> [...]  -> median/p95/max and slope (MB per hour)"""
import csv
import statistics
import sys

for path in sys.argv[1:]:
    rows = list(csv.DictReader(open(path)))
    t = [float(r["epoch"]) for r in rows]
    y = [float(r["total_mb"]) for r in rows]
    if len(y) < 2:
        print(f"{path}: not enough samples")
        continue
    ys = sorted(y)
    p95 = ys[int(0.95 * (len(ys) - 1))]
    tm, ym = statistics.fmean(t), statistics.fmean(y)
    den = sum((ti - tm) ** 2 for ti in t)
    slope_h = (sum((ti - tm) * (yi - ym) for ti, yi in zip(t, y)) / den * 3600) if den else 0.0
    hours = (t[-1] - t[0]) / 3600
    print(f"{path}: n={len(y)} over {hours:.1f} h  median={statistics.median(y):.1f} MB  "
          f"p95={p95:.1f}  max={max(y):.1f}  slope={slope_h:+.2f} MB/h")
```

- [ ] **Step 3: Write `eval/poll.sh`** (stands in for the kiosk: live.html fetches `/last_frame`, then waits 1 s, or 0.5 s after an error)

```bash
#!/usr/bin/env bash
# usage: eval/poll.sh <port> <seconds>
set -euo pipefail
end=$((SECONDS + $2))
while [ "$SECONDS" -lt "$end" ]; do
  curl -s -o /dev/null --max-time 20 "http://127.0.0.1:$1/last_frame" || true
  sleep 1
done
```

- [ ] **Step 4: Write `eval/startup.sh`**

```bash
#!/usr/bin/env bash
# Time from launch to the first 200 on /last_frame. The server is killed afterwards.
# usage: eval/startup.sh <port> -- <command...>
set -euo pipefail
port=$1; shift 2
start=$(python3 -c 'import time; print(time.time())')
"$@" & pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
until [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "http://127.0.0.1:$port/last_frame")" = 200 ]; do
  sleep 0.2
done
python3 -c "import time; print(f'first frame after {time.time() - $start:.2f} s')"
```

- [ ] **Step 5: Make them executable, check them, commit**

```bash
chmod +x eval/*.sh && shellcheck eval/*.sh
git add eval && git commit -m "eval: memory sampler, poller, startup timer"
```

- [ ] **Step 6: Start the 48 h leak watch on the production server** (runs in the background, does not touch the server)

```bash
mkdir -p eval/results/before
PID=$(pgrep -f '^awh')   # Python's setproctitle name; the Go binary runs as ./awh
nohup eval/memwatch.sh "$PID" 172800 60 eval/results/before/mem-48h.csv >/dev/null 2>&1 &
```

- [ ] **Step 7: Idle and active samples** (the active one drives the live loop, the same thing the kiosk does)

```bash
eval/memwatch.sh "$PID" 600 5 eval/results/before/mem-idle.csv
eval/poll.sh 3060 1800 & eval/memwatch.sh "$PID" 1800 5 eval/results/before/mem-active.csv; wait
python_env/bin/python eval/mem_summary.py eval/results/before/mem-*.csv | tee eval/results/before/mem-summary.txt
```

Skip the idle sample if someone has the kiosk open; `/last_frame` requests in the server log show that.

- [ ] **Step 8: Cold start (a second Python instance on a spare port, killed at the end)**

```bash
eval/startup.sh 3062 -- env HOST=127.0.0.1 PORT=3062 python_env/bin/python app.py | tee eval/results/before/startup.txt
```

- [ ] **Step 9: Leak decision point, after 48 h**

```bash
python_env/bin/python eval/mem_summary.py eval/results/before/mem-48h.csv
```

If `slope` is above **+0.5 MB/h** with steady polling, it is a leak. Report the number to the user and ask whether to (a) keep going with Go, which removes the suspects (cv2's per-frame `VideoCapture`, numpy temporaries), or (b) first try a Python fix. Tasks 2–8 do not depend on this answer and can run while waiting.

- [ ] **Step 10: Commit the results**

```bash
git add eval/results/before && git commit -m "eval: before memory baseline (Python server)"
```

---

### Task 2: "Before" OCR outputs (Python reference)

**Files:**
- Create: `eval/build_manifest.py`, `eval/ocr_reference.py`

- [ ] **Step 1: Write `eval/build_manifest.py`**

```python
#!/usr/bin/env python3
"""
Builds eval/manifest.jsonl: one {"id", "path", "truth"?} per crop.

Sources: every ocr_samples/*/*_raw.png (the raw crops OCR reads in production),
the cropped images saved with past errors in errors.sqlite3 (written out as PNG
so both sides decode identical pixels), and truth labels from ocr_bench/dataset.json.
"""
import glob
import json
import os
import sqlite3

import cv2
import numpy as np

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)

truth = {}
for rec in json.load(open("ocr_bench/dataset.json")):
    truth[os.path.abspath(rec["raw"])] = rec["truth"]

rows = []
for path in sorted(glob.glob("ocr_samples/*/*_raw.png")):
    kind, name = path.split(os.sep)[-2:]
    row = {"id": f"{kind}/{name[:-len('_raw.png')]}", "path": os.path.abspath(path)}
    if os.path.abspath(path) in truth:
        row["truth"] = truth[os.path.abspath(path)]
    rows.append(row)

os.makedirs("eval/images/errors", exist_ok=True)
with sqlite3.connect("errors.sqlite3") as conn:
    for err_id, blob in conn.execute("SELECT id, cropped_image FROM errors ORDER BY id"):
        img = cv2.imdecode(np.frombuffer(blob, np.uint8), cv2.IMREAD_COLOR)
        out = os.path.abspath(f"eval/images/errors/{err_id}.png")
        cv2.imwrite(out, img)
        rows.append({"id": f"errors/{err_id}", "path": out})

with open("eval/manifest.jsonl", "w") as fh:
    for row in rows:
        fh.write(json.dumps(row) + "\n")
print(f"{len(rows)} images, {sum('truth' in r for r in rows)} with truth")
```

- [ ] **Step 2: Run it**

```bash
python_env/bin/python eval/build_manifest.py
```

Expected: about `280 images, 33 with truth`. If the truth count is 0, `dataset.json`'s `raw` paths and the glob disagree; print one of each and fix the key.

- [ ] **Step 3: Write `eval/ocr_reference.py`** (calls the production functions themselves, so this *is* the before)

```python
#!/usr/bin/env python3
"""usage: ocr_reference.py <manifest.jsonl> <out.jsonl>  (run from the repo root)"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import cv2  # noqa: E402

from auto_reset import get_ocr_reader, read_display  # noqa: E402
from display_reading import classify  # noqa: E402

manifest, out_path = sys.argv[1], sys.argv[2]
rows = [json.loads(line) for line in open(manifest)]
reader = get_ocr_reader()
first = cv2.imread(rows[0]["path"])
for _ in range(2):
    read_display(reader, first)

with open(out_path, "w") as out:
    for row in rows:
        img = cv2.imread(row["path"])  # BGR uint8, the same as the live crop
        t0 = time.perf_counter()
        text, score = read_display(reader, img)
        ms = (time.perf_counter() - t0) * 1000
        kind, value = classify(text, score)
        out.write(json.dumps({"id": row["id"], "text": text, "score": round(score, 5),
                              "kind": kind, "value": value, "ms": round(ms, 2)},
                             ensure_ascii=False) + "\n")
print(f"wrote {len(rows)} results to {out_path}")
```

- [ ] **Step 4: Run it and commit**

```bash
python_env/bin/python eval/ocr_reference.py eval/manifest.jsonl eval/results/before/ocr.jsonl
git add eval/build_manifest.py eval/ocr_reference.py eval/manifest.jsonl eval/results/before/ocr.jsonl
git commit -m "eval: Python OCR reference outputs (before)"
```

---

### Task 3: "Before" HTTP contract snapshot

**Files:**
- Create: `eval/http_contract.sh`

- [ ] **Step 1: Write `eval/http_contract.sh`** (read-only endpoints only; `/reset` is never called)

```bash
#!/usr/bin/env bash
# Prints "<endpoint> <status> <sorted top-level JSON keys>" for each read-only endpoint.
# usage: eval/http_contract.sh <port>
set -euo pipefail
base="http://127.0.0.1:$1"
check() {  # method path [json-body]
  local body; body=$(mktemp)
  local code
  if [ "$1" = GET ]; then
    code=$(curl -s -o "$body" -w '%{http_code}' --max-time 30 "$base$2")
  else
    code=$(curl -s -o "$body" -w '%{http_code}' --max-time 30 -X "$1" -H 'Content-Type: application/json' -d "$3" "$base$2")
  fi
  local keys; keys=$(jq -c 'if type=="object" then keys else "non-object" end' < "$body" 2>/dev/null || echo html)
  echo "$1 $2 $code $keys"
  rm -f "$body"
}
curl -s -o /dev/null "$base/last_frame"; sleep 8   # wake the live loop
check GET /last_frame
check GET /api/display
check GET "/api/display?display_crop=0.6,0.5,0.7,0.6"
check GET "/api/display?display_crop=1,2,3"
check GET /api/ocr-samples
check POST /api/update-crop '{"edge":"nope","dir":1}'
check POST /api/update-crop '{"edge":"left","dir":5}'
check POST /api/save-view '{"view":{"scale":1}}'
check POST /api/save-crop '{"crop":[1,2]}'
check POST /api/ocr-samples/selection '{"select":"x"}'
check GET /ocr-samples/nope/x.png
check GET /live
check GET /
```

- [ ] **Step 2: Snapshot Python and commit**

```bash
chmod +x eval/http_contract.sh && shellcheck eval/http_contract.sh
eval/http_contract.sh 3060 | tee eval/results/before/http.txt
git add eval/http_contract.sh eval/results/before/http.txt && git commit -m "eval: HTTP contract snapshot (before)"
```

Expected: the 4xx cases are 400 with `["error","ok"]`, `/live` and `/` are `html`, and `/last_frame` is 200 with `["frame_b64","ocr_kind","ocr_text","ok"]`.

---

### Task 4: Go module and `reading` (port of `display_reading.py`)

**Files:**
- Create: `go.mod`, `internal/reading/reading.go`, `internal/reading/reading_test.go`

- [ ] **Step 1: Init the module**

```bash
go mod init github.com/grillermo/awh
```

- [ ] **Step 2: Write the failing test** `internal/reading/reading_test.go` (mirrors `display_reading_test.py`)

```go
package reading

import (
	"strings"
	"testing"
)

const sure = 0.95

func check(t *testing.T, text string, score float64, kind, value string) {
	t.Helper()
	k, v := Classify(text, score)
	if k != kind || v != value {
		t.Errorf("Classify(%q, %v) = (%q, %q), want (%q, %q)", text, score, k, v, kind, value)
	}
}

func TestTemperatures(t *testing.T) {
	check(t, "45", sure, "temperature", "45")
	check(t, "45°C", sure, "temperature", "45")
	check(t, "45C", sure, "temperature", "45")
	check(t, " 60 ", sure, "temperature", "60")
}

func TestEveryManualErrorCodeIsAnError(t *testing.T) {
	for code := range ErrorCodes {
		check(t, code, sure, "error", code)
	}
	check(t, "e1", sure, "error", "E1")
}

func TestMenuCodesAreNotErrors(t *testing.T) {
	for _, code := range []string{"P4", "SA", "oF", "on", "10F", "3D", "C1", "00"} {
		check(t, code, sure, "menu", strings.ToUpper(code))
	}
}

func TestLowConfidenceIsNothing(t *testing.T) {
	check(t, "45", MinScore-0.01, "none", "")
	check(t, "E1", 0.3, "none", "")
	check(t, "45", 0, "none", "")
}

func TestAnythingNotInTheManualIsNothing(t *testing.T) {
	for _, text := range []string{"恒", "福", "鸭", "C", "1", "8", "188", "451", "4", "UG", "W", ""} {
		check(t, text, sure, "none", "")
	}
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `go test ./internal/reading/`
Expected: FAIL, `undefined: Classify`.

- [ ] **Step 4: Write `internal/reading/reading.go`**

```go
// Package reading turns an OCR result into something the Bosch Therm 5600 F
// display can show. Port of display_reading.py; the lists come from the
// heater's manual (table 17 for errors, Fig. 7 and sections 4.6/8 for menus).
package reading

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MinScore is RapidOCR's confidence below which a reading is noise.
const MinScore = 0.7

var ErrorCodes = setOf("A0", "A7", "C7", "CA", "CF", "E1", "E2", "E9",
	"EA", "EC", "EE", "EF", "F7", "FA")

var MenuCodes = func() map[string]bool {
	m := setOf("P4", "P9", "SA", "PP", "DH", "OD", "FH", "LP", "HP",
		"H0", "H1", "H2", "C0", "C1", "C2",
		"P8", "P6", "P5", "P3", "AA", "AP", "P0", "L0", "ON", "OF", "00")
	for n := 1; n <= 10; n++ {
		m[strconv.Itoa(n)+"F"] = true // 1F..10F: the last 10 errors
	}
	for n := 1; n <= 5; n++ {
		m[strconv.Itoa(n)+"D"] = true // 1D..5D: operating data
	}
	return m
}()

// \p{Nd}, not \d: Python's \d matches every Unicode decimal digit.
var temperature = regexp.MustCompile(`^(\p{Nd}{2})(?:°?[CF])?$`)

func setOf(codes ...string) map[string]bool {
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// Classify returns kind ("temperature", "error", "menu" or "none") and the
// cleaned value ("45", "E1", "P4", or "" for none).
func Classify(text string, score float64) (kind, value string) {
	if score < MinScore {
		return "none", ""
	}
	compact := strings.ToUpper(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text))
	if compact == "" {
		return "none", ""
	}
	// Menus first: "00" (P5's ignition delay) would otherwise pass as a temperature.
	if MenuCodes[compact] {
		return "menu", compact
	}
	if ErrorCodes[compact] {
		return "error", compact
	}
	if m := temperature.FindStringSubmatch(compact); m != nil {
		return "temperature", m[1]
	}
	return "none", ""
}
```

- [ ] **Step 5: Run it and watch it pass**

Run: `go test ./internal/reading/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/reading && git commit -m "feat(go): reading.Classify, port of display_reading"
```

---

### Task 5: `vision` image type, crop and encoders

**Files:**
- Create: `internal/vision/image.go`, `internal/vision/image_test.go`

- [ ] **Step 1: Write the failing test**

```go
package vision

import (
	"strings"
	"testing"
)

func TestCropRectMatchesPythonIntTruncation(t *testing.T) {
	// Python: l, t, r, b = int(w*0.6035), int(h*0.5494), int(w*0.6426), int(h*0.6233)
	r, err := CropRect(2560, 1440, [4]float64{0.6035, 0.5494, 0.6426, 0.6233})
	if err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 1544, Y: 791, W: 1645 - 1544, H: 897 - 791}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
}

func TestCropRectEmptyIsAnError(t *testing.T) {
	_, err := CropRect(640, 480, [4]float64{0.5, 0.5, 0.5, 0.6})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-crop error, got %v", err)
	}
}

func TestCropCopiesPixels(t *testing.T) {
	m := New(4, 3)
	for i := range m.Pix {
		m.Pix[i] = uint8(i)
	}
	c := m.Crop(Rect{X: 1, Y: 1, W: 2, H: 2})
	// pixel (1,1) of the source starts at (1*4+1)*3 = 15
	if c.W != 2 || c.H != 2 || c.Pix[0] != 15 || c.Pix[3] != 18 || c.Pix[6] != 27 {
		t.Fatalf("unexpected crop %+v", c)
	}
}

func TestJPEGBase64RoundTrips(t *testing.T) {
	s, err := EncodeJPEGBase64(New(8, 8), 85)
	if err != nil || !strings.HasPrefix(s, "/9j/") { // base64 of FF D8 FF
		t.Fatalf("got %q, %v", s[:min(8, len(s))], err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/vision/`
Expected: FAIL, `undefined: CropRect`.

- [ ] **Step 3: Write `internal/vision/image.go`**

```go
// Package vision holds the few image operations the server needs, on 8-bit
// BGR images laid out like OpenCV's (so ports from auto_reset.py read the same).
package vision

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
)

// Image is 8-bit BGR, row-major, 3 bytes per pixel, no padding.
type Image struct {
	W, H int
	Pix  []uint8
}

type Rect struct{ X, Y, W, H int }

func New(w, h int) Image { return Image{W: w, H: h, Pix: make([]uint8, w*h*3)} }

// CropRect turns crop fractions (left, top, right, bottom) into pixels exactly
// like auto_reset.crop_frame: int() truncation, numpy-style clamping at the edge.
func CropRect(w, h int, c [4]float64) (Rect, error) {
	l, t := int(float64(w)*c[0]), int(float64(h)*c[1])
	r, b := min(int(float64(w)*c[2]), w), min(int(float64(h)*c[3]), h)
	if r <= l || b <= t {
		return Rect{}, fmt.Errorf("crop produced an empty image: frame=%dx%d, crop=l%d:r%d,t%d:b%d from coords=%v",
			w, h, l, r, t, b, c)
	}
	return Rect{X: l, Y: t, W: r - l, H: b - t}, nil
}

func (m Image) Crop(r Rect) Image {
	out := New(r.W, r.H)
	for y := 0; y < r.H; y++ {
		src := ((r.Y+y)*m.W + r.X) * 3
		copy(out.Pix[y*r.W*3:(y+1)*r.W*3], m.Pix[src:src+r.W*3])
	}
	return out
}

// FromImage converts any decoded image (PNG from cv2.imwrite, etc.) to BGR.
func FromImage(src image.Image) Image {
	b := src.Bounds()
	out := New(b.Dx(), b.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*out.W + x) * 3
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = uint8(bl>>8), uint8(g>>8), uint8(r>>8)
		}
	}
	return out
}

func LoadPNG(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", path, err)
	}
	return FromImage(img), nil
}

func (m Image) rgba() *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, m.W, m.H))
	for i, j := 0, 0; i < len(m.Pix); i, j = i+3, j+4 {
		out.Pix[j], out.Pix[j+1], out.Pix[j+2], out.Pix[j+3] = m.Pix[i+2], m.Pix[i+1], m.Pix[i], 255
	}
	return out
}

func EncodeJPEGBase64(m Image, quality int) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m.rgba(), &jpeg.Options{Quality: quality}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func SavePNG(m Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, m.rgba()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/vision/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/vision && git commit -m "feat(go): vision image type, crop, PNG/JPEG"
```

---

### Task 6: `vision` resize, enhance, perspective (checked against Python fixtures)

**Files:**
- Create: `eval/dump_fixtures.py`, `internal/vision/resize.go`, `internal/vision/enhance.go`, `internal/vision/perspective.go`, `internal/vision/fixtures_test.go`, `internal/testdata/` (generated)

- [ ] **Step 1: Write `eval/dump_fixtures.py`** (Python outputs for 2 real crops: the cv2 resize RapidOCR would do, the full rec tensor, and the display image)

```python
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
rec = get_ocr_reader().text_rec  # rapidocr TextRecognizer
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
```

- [ ] **Step 2: Generate them from one true "45" crop and one noise crop**

```bash
D=$(python_env/bin/python -c "import json; print(next(r['raw'] for r in json.load(open('ocr_bench/dataset.json'))))")
N=$(ls ocr_samples/text/*_raw.png | head -1)
python_env/bin/python eval/dump_fixtures.py "$D" "$N"
```

If `get_ocr_reader().text_rec` raises `AttributeError`, find the attribute with `python_env/bin/python -c "from auto_reset import get_ocr_reader; print(vars(get_ocr_reader()).keys())"` and use the one holding the `TextRecognizer`.

- [ ] **Step 3: Write the failing test** `internal/vision/fixtures_test.go`

```go
package vision

import (
	"path/filepath"
	"testing"
)

const td = "../testdata"

func load(t *testing.T, name string) Image {
	t.Helper()
	m, err := LoadPNG(filepath.Join(td, name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func diff(a, b Image) (maxAbs int, meanAbs float64) {
	sum := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		maxAbs = max(maxAbs, d)
	}
	return maxAbs, float64(sum) / float64(len(a.Pix))
}

func TestResizeMatchesCv2(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		src, want := load(t, n+".png"), load(t, n+"_resize.png")
		got := ResizeLinear(src, want.W, want.H)
		if mx, _ := diff(got, want); mx > 1 {
			t.Errorf("%s: max |diff| %d grey levels, want <= 1", n, mx)
		}
	}
}

// Display-only output; it never reaches OCR, so the bar is looser.
func TestFixedFrameCloseToPython(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		src, want := load(t, n+".png"), load(t, n+"_fixed.png")
		got := Enhance(FixPerspective(src))
		if _, mean := diff(got, want); mean > 2 {
			t.Errorf("%s: mean |diff| %.2f, want <= 2", n, mean)
		}
	}
}
```

- [ ] **Step 4: Run it and watch it fail**

Run: `go test ./internal/vision/ -run 'Resize|Fixed'`
Expected: FAIL, `undefined: ResizeLinear`.

- [ ] **Step 5: Write `internal/vision/resize.go`**

```go
package vision

import "math"

// OpenCV's INTER_LINEAR for 8-bit images: half-pixel centres, 11-bit fixed
// point weights (INTER_RESIZE_COEF_BITS), edge clamping. RapidOCR resizes the
// display crop with this before recognition, so it decides what the model sees.
const (
	coefBits  = 11
	coefScale = 1 << coefBits
)

type tap struct {
	i0, i1 int
	a0, a1 int64
}

func linearTaps(dst, src int) []tap {
	scale := float64(src) / float64(dst)
	taps := make([]tap, dst)
	for d := range taps {
		f := float32((float64(d)+0.5)*scale - 0.5)
		s := int(math.Floor(float64(f)))
		f -= float32(s)
		if s < 0 {
			s, f = 0, 0
		}
		if s >= src-1 {
			s, f = src-1, 0
		}
		a0 := int64(math.RoundToEven(float64((1 - f) * coefScale))) // cvRound
		taps[d] = tap{i0: s, i1: min(s+1, src-1), a0: a0, a1: coefScale - a0}
	}
	return taps
}

func ResizeLinear(m Image, w, h int) Image {
	xs, ys := linearTaps(w, m.W), linearTaps(h, m.H)
	out := New(w, h)
	r0, r1 := make([]int64, w*3), make([]int64, w*3)
	row := func(y int, buf []int64) {
		base := y * m.W * 3
		for x, t := range xs {
			for c := 0; c < 3; c++ {
				buf[x*3+c] = int64(m.Pix[base+t.i0*3+c])*t.a0 + int64(m.Pix[base+t.i1*3+c])*t.a1
			}
		}
	}
	for y, t := range ys {
		row(t.i0, r0)
		row(t.i1, r1)
		for i := range r0 {
			v := (r0[i]*t.a0 + r1[i]*t.a1 + 1<<(2*coefBits-1)) >> (2 * coefBits)
			out.Pix[y*w*3+i] = uint8(min(max(v, 0), 255))
		}
	}
	return out
}
```

- [ ] **Step 6: Write `internal/vision/enhance.go`** (port of `enhance_frame`)

```go
package vision

import (
	"log"
	"math"
)

const (
	enhanceGammaMin   = 0.35
	enhanceGammaMax   = 3.0
	enhanceTargetMean = 0.5
	enhanceMinSpread  = 24
)

// gray is cv2.COLOR_BGR2GRAY for 8-bit: fixed point with 14 fractional bits.
func gray(p []uint8, i int) uint8 {
	return uint8((int(p[i])*1868 + int(p[i+1])*9617 + int(p[i+2])*4899 + 8192) >> 14)
}

// Enhance stretches p1..p99 of the luma to full range, then applies a gamma
// that pulls mean luminance toward mid-grey, as one LUT on every channel.
func Enhance(m Image) Image {
	var hist [256]int
	n := m.W * m.H
	for i := 0; i < len(m.Pix); i += 3 {
		hist[gray(m.Pix, i)]++
	}
	lo, hi := percentile(&hist, n, 1), percentile(&hist, n, 99)

	var ramp [256]float64
	for v := range ramp {
		ramp[v] = float64(v)
	}
	if hi-lo >= enhanceMinSpread {
		for v := range ramp {
			ramp[v] = clamp((float64(v)-lo)*(255/(hi-lo)), 0, 255)
		}
	}
	var dot float64
	for v := range ramp {
		dot += ramp[v] * float64(hist[v])
	}
	mean := clamp(dot/float64(n)/255, 0.02, 0.98)
	gamma := clamp(math.Log(enhanceTargetMean)/math.Log(mean), enhanceGammaMin, enhanceGammaMax)

	var lut [256]uint8
	for v := range lut {
		lut[v] = uint8(math.RoundToEven(clamp(255*math.Pow(ramp[v]/255, gamma), 0, 255)))
	}
	out := New(m.W, m.H)
	for i, p := range m.Pix {
		out.Pix[i] = lut[p]
	}
	if debugEnhance {
		log.Printf("[enhance] p1=%.0f p99=%.0f mean=%.2f gamma=%.2f", lo, hi, mean, gamma)
	}
	return out
}

var debugEnhance = false

// percentile is numpy's default ("linear") method, read off a histogram.
func percentile(hist *[256]int, n int, p float64) float64 {
	pos := p / 100 * float64(n-1)
	k := int(math.Floor(pos))
	a, b := float64(valueAtRank(hist, k)), float64(valueAtRank(hist, min(k+1, n-1)))
	return a + (pos-float64(k))*(b-a)
}

func valueAtRank(hist *[256]int, k int) int {
	seen := 0
	for v, c := range hist {
		seen += c
		if k < seen {
			return v
		}
	}
	return 255
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }
```

- [ ] **Step 7: Write `internal/vision/perspective.go`** (port of `fix_perspective_frame`)

```go
package vision

import "math"

// displayQuadRel: the display's corners inside the crop (TL, TR, BR, BL),
// copied from DISPLAY_QUAD_REL in auto_reset.py.
var displayQuadRel = [4][2]float64{
	{0.2759, 0.2895}, {0.7931, 0.1316}, {0.7586, 0.6579}, {0.2069, 0.8421},
}

const fixedSize = 200

// FixPerspective warps the display quad to a 200x200 square (bilinear,
// black outside the source), like cv2.warpPerspective in auto_reset.py.
func FixPerspective(m Image) Image {
	last := float64(fixedSize - 1)
	dst := [4][2]float64{{0, 0}, {last, 0}, {last, last}, {0, last}}
	var src [4][2]float64
	for i, q := range displayQuadRel {
		src[i] = [2]float64{q[0] * float64(m.W), q[1] * float64(m.H)}
	}
	h := homography(dst, src) // output pixel -> source pixel
	out := New(fixedSize, fixedSize)
	for y := 0; y < fixedSize; y++ {
		for x := 0; x < fixedSize; x++ {
			fx, fy := float64(x), float64(y)
			d := h[6]*fx + h[7]*fy + 1
			sx := (h[0]*fx + h[1]*fy + h[2]) / d
			sy := (h[3]*fx + h[4]*fy + h[5]) / d
			sample(m, sx, sy, out.Pix[(y*fixedSize+x)*3:])
		}
	}
	return out
}

// homography solves the 8 unknowns mapping from[i] -> to[i] (h33 = 1).
func homography(from, to [4][2]float64) [8]float64 {
	var a [8][9]float64
	for i := 0; i < 4; i++ {
		x, y, u, v := from[i][0], from[i][1], to[i][0], to[i][1]
		a[2*i] = [9]float64{x, y, 1, 0, 0, 0, -u * x, -u * y, u}
		a[2*i+1] = [9]float64{0, 0, 0, x, y, 1, -v * x, -v * y, v}
	}
	for c := 0; c < 8; c++ { // Gauss-Jordan, partial pivoting
		p := c
		for r := c + 1; r < 8; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		for r := 0; r < 8; r++ {
			if r != c {
				f := a[r][c] / a[c][c]
				for k := c; k < 9; k++ {
					a[r][k] -= f * a[c][k]
				}
			}
		}
	}
	var h [8]float64
	for i := range h {
		h[i] = a[i][8] / a[i][i]
	}
	return h
}

func sample(m Image, x, y float64, out []uint8) {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	for c := 0; c < 3; c++ {
		v := (1-fx)*(1-fy)*px(m, x0, y0, c) + fx*(1-fy)*px(m, x0+1, y0, c) +
			(1-fx)*fy*px(m, x0, y0+1, c) + fx*fy*px(m, x0+1, y0+1, c)
		out[c] = uint8(math.Round(clamp(v, 0, 255)))
	}
}

func px(m Image, x, y, c int) float64 {
	if x < 0 || y < 0 || x >= m.W || y >= m.H {
		return 0
	}
	return float64(m.Pix[(y*m.W+x)*3+c])
}
```

- [ ] **Step 8: Run the tests**

Run: `go test ./internal/vision/`
Expected: `ok`. If `TestResizeMatchesCv2` fails with max diff 2–3, the vertical rounding differs from OpenCV's NEON path. Try `v := ((r0[i]>>4)*t.a0>>16 + (r1[i]>>4)*t.a1>>16 + 2) >> 2` and keep whichever matches. E2 and E1 then confirm it doesn't matter to OCR.

- [ ] **Step 9: Commit**

```bash
git add eval/dump_fixtures.py internal/testdata internal/vision
git commit -m "feat(go): cv2-compatible resize, enhance and perspective, checked against Python"
```

---

### Task 7: `ocr` (model, preprocess, CTC decode, recognizer)

**Files:**
- Create: `scripts/fetch-model`, `models/ppocrv6_rec_keys.json`, `internal/ocr/preprocess.go`, `internal/ocr/decode.go`, `internal/ocr/recognizer.go`, `internal/ocr/ocr_test.go`

- [ ] **Step 1: Write `scripts/fetch-model`** (copies the exact ONNX file RapidOCR uses and extracts its character list as JSON. JSON, not lines, because Python's `splitlines()` also splits on `\x0b`, `\x1c`–`\x1e`, `\x85` and ` `, so a line file can't round-trip)

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
src=$(python_env/bin/python -c "import rapidocr, os; print(os.path.join(os.path.dirname(rapidocr.__file__), 'models', 'PP-OCRv6_rec_small.onnx'))")
mkdir -p models
cp "$src" models/PP-OCRv6_rec_small.onnx
python_env/bin/python - <<'EOF'
import json, onnxruntime as ort
s = ort.InferenceSession("models/PP-OCRv6_rec_small.onnx", providers=["CPUExecutionProvider"])
chars = s.get_modelmeta().custom_metadata_map["character"].splitlines()
json.dump(chars, open("models/ppocrv6_rec_keys.json", "w"), ensure_ascii=False)
print(len(chars), "characters")
EOF
shasum -a 256 models/PP-OCRv6_rec_small.onnx
```

- [ ] **Step 2: Run it**

```bash
chmod +x scripts/fetch-model && shellcheck scripts/fetch-model && scripts/fetch-model
```

Expected: `18708 characters`. With blank and space added, that matches the model's 18710 output classes.

- [ ] **Step 3: Add the Go dependency**

```bash
go get github.com/yalue/onnxruntime_go@latest
go doc github.com/yalue/onnxruntime_go NewDynamicAdvancedSession
go doc github.com/yalue/onnxruntime_go SessionOptions | grep -E 'IntraOp|InterOp|CpuMemArena|MemPattern'
```

Expected: the session constructor and the thread setters exist. If `SetCpuMemArena`/`SetMemPattern` are missing in this version, drop those two lines in Step 6. The floor test showed they save only ~6 MB.

- [ ] **Step 4: Write the failing test** `internal/ocr/ocr_test.go`

```go
package ocr

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/grillermo/awh/internal/vision"
)

const td = "../testdata"

func TestPreprocessMatchesRapidOCRTensor(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		img, err := vision.LoadPNG(filepath.Join(td, n+".png"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(td, n+"_tensor.f32"))
		if err != nil {
			t.Fatal(err)
		}
		got, width := Preprocess(img)
		if len(raw)/4 != len(got) || len(got) != 3*recH*width {
			t.Fatalf("%s: len python %d, go %d (width %d)", n, len(raw)/4, len(got), width)
		}
		worst := 0.0
		for i := range got {
			want := math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			worst = math.Max(worst, math.Abs(float64(got[i]-want)))
		}
		if worst > 2.0/255+1e-6 { // one grey level, after (x/255-0.5)/0.5
			t.Errorf("%s: max |diff| %.5f", n, worst)
		}
	}
}

func TestDecodeCollapsesRepeatsAndBlanks(t *testing.T) {
	keys := []string{"blank", "4", "5", " "}
	// timesteps: 4 4 blank 5 5 -> "45"; confs 0.9, 0.8 (first of each run)
	probs := []float32{
		0.05, 0.9, 0.03, 0.02,
		0.1, 0.7, 0.1, 0.1,
		0.9, 0.05, 0.03, 0.02,
		0.1, 0.05, 0.8, 0.05,
		0.1, 0.05, 0.6, 0.25,
	}
	text, score := Decode(probs, 5, 4, keys)
	if text != "45" || math.Abs(score-0.85) > 1e-9 {
		t.Fatalf("got %q %.5f", text, score)
	}
}

func TestDecodeAllBlankIsEmptyZero(t *testing.T) {
	text, score := Decode([]float32{1, 0, 1, 0}, 2, 2, []string{"blank", "x"})
	if text != "" || score != 0 {
		t.Fatalf("got %q %v", text, score)
	}
}

func TestRecognizerReadsTheFixture45(t *testing.T) {
	lib := "/opt/homebrew/lib/libonnxruntime.dylib"
	if _, err := os.Stat(lib); err != nil {
		t.Skip("onnxruntime not installed")
	}
	if err := Init(lib); err != nil {
		t.Fatal(err)
	}
	r, err := NewRecognizer("../../models/PP-OCRv6_rec_small.onnx", "../../models/ppocrv6_rec_keys.json", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	img, _ := vision.LoadPNG(filepath.Join(td, "digit45.png"))
	text, score, err := r.Read(img)
	if err != nil || text == "" || score < 0.7 {
		t.Fatalf("got %q %.3f %v", text, score, err)
	}
	t.Logf("read %q at %.5f", text, score)
}
```

In `TestDecodeCollapsesRepeatsAndBlanks`, the second "4" step (0.7) is a repeat, so RapidOCR's `remove_duplicate` drops it. Its conf doesn't count either; only selected positions contribute, and that matches `decode()`.

- [ ] **Step 5: Run it and watch it fail**

Run: `go test ./internal/ocr/`
Expected: FAIL, `undefined: Preprocess`.

- [ ] **Step 6: Write the three files**

`internal/ocr/preprocess.go`:

```go
// Package ocr runs RapidOCR's PP-OCRv6 recogniser (no detection, no
// classifier) the way rapidocr/ch_ppocr_rec does, on onnxruntime.
package ocr

import (
	"math"

	"github.com/grillermo/awh/internal/vision"
)

const (
	recH = 48  // rec_img_shape [3, 48, 320]
	recW = 320
)

// Preprocess mirrors TextRecognizer.resize_norm_img for a batch of one:
// resize to height 48 keeping aspect, scale to [-1, 1], pad right with 0.
// The channel order stays BGR, exactly what RapidOCR feeds the model from an ndarray.
func Preprocess(m vision.Image) ([]float32, int) {
	maxWH := math.Max(float64(recW)/float64(recH), float64(m.W)*1.0/float64(m.H))
	width := int(float64(recH) * maxWH)
	resizedW := int(math.Ceil(float64(recH) * (float64(m.W) / float64(m.H))))
	if resizedW > width {
		resizedW = width
	}
	r := vision.ResizeLinear(m, resizedW, recH)
	data := make([]float32, 3*recH*width)
	for y := 0; y < recH; y++ {
		for x := 0; x < resizedW; x++ {
			for c := 0; c < 3; c++ {
				v := float32(r.Pix[(y*resizedW+x)*3+c]) / 255
				data[c*recH*width+y*width+x] = (v - 0.5) / 0.5
			}
		}
	}
	return data, width
}
```

`internal/ocr/decode.go`:

```go
package ocr

import (
	"math"
	"strings"
)

// Decode is CTCLabelDecode with remove_duplicate=True: argmax per step, drop
// repeats and blanks (index 0); the score is the mean of the kept steps' max
// probabilities, each rounded to 5 decimals, the mean rounded again.
func Decode(probs []float32, steps, classes int, keys []string) (string, float64) {
	var sb strings.Builder
	var sum float64
	kept, prev := 0, -1
	for t := 0; t < steps; t++ {
		row := probs[t*classes : (t+1)*classes]
		best := 0
		for c := 1; c < classes; c++ {
			if row[c] > row[best] { // strict: first max wins, like numpy argmax
				best = c
			}
		}
		if best != prev && best != 0 {
			sb.WriteString(keys[best])
			sum += round5(float64(row[best]))
			kept++
		}
		prev = best
	}
	if kept == 0 {
		return "", 0
	}
	return sb.String(), round5(sum / float64(kept))
}

func round5(x float64) float64 { return math.RoundToEven(x*1e5) / 1e5 }
```

`internal/ocr/recognizer.go`:

```go
package ocr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/grillermo/awh/internal/vision"
)

// Init loads the onnxruntime shared library once per process.
func Init(libPath string) error {
	ort.SetSharedLibraryPath(libPath)
	return ort.InitializeEnvironment()
}

type Recognizer struct {
	mu   sync.Mutex
	sess *ort.DynamicAdvancedSession
	keys []string
}

func NewRecognizer(modelPath, keysPath string, threads int) (*Recognizer, error) {
	raw, err := os.ReadFile(keysPath)
	if err != nil {
		return nil, err
	}
	var chars []string
	if err := json.Unmarshal(raw, &chars); err != nil {
		return nil, fmt.Errorf("%s: %w", keysPath, err)
	}
	// rapidocr: insert " " at the end, then "blank" at index 0.
	keys := append(append([]string{"blank"}, chars...), " ")

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	for _, set := range []func() error{
		func() error { return opts.SetIntraOpNumThreads(threads) },
		func() error { return opts.SetInterOpNumThreads(1) },
		func() error { return opts.SetCpuMemArena(false) },
		func() error { return opts.SetMemPattern(false) },
	} {
		if err := set(); err != nil {
			return nil, err
		}
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath, []string{"x"}, []string{"fetch_name_0"}, opts)
	if err != nil {
		return nil, err
	}
	return &Recognizer{sess: sess, keys: keys}, nil
}

// Read returns RapidOCR's (text, score) for the whole image, or ("", 0) when
// nothing was read, like auto_reset.read_display.
func (r *Recognizer) Read(m vision.Image) (string, float64, error) {
	data, width := Preprocess(m)
	in, err := ort.NewTensor(ort.NewShape(1, 3, recH, int64(width)), data)
	if err != nil {
		return "", 0, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	r.mu.Lock()
	err = r.sess.Run([]ort.Value{in}, outs)
	r.mu.Unlock()
	if err != nil {
		return "", 0, err
	}
	defer outs[0].Destroy()
	t, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return "", 0, fmt.Errorf("unexpected output type %T", outs[0])
	}
	shape := t.GetShape()
	if len(shape) != 3 || int(shape[2]) != len(r.keys) {
		return "", 0, fmt.Errorf("output shape %v does not match %d keys", shape, len(r.keys))
	}
	text, score := Decode(t.GetData(), int(shape[1]), int(shape[2]), r.keys)
	if strings.TrimSpace(text) == "" {
		return "", 0, nil
	}
	return text, score, nil
}

func (r *Recognizer) Close() { r.sess.Destroy() }
```

- [ ] **Step 7: Run the tests**

```bash
CGO_ENABLED=1 go test ./internal/ocr/ -v
```

Expected: all pass, and the log shows `read "45" at 0.9…` (or `45°C`).

- [ ] **Step 8: Commit** (the `.onnx` file is gitignored; the keys JSON is committed)

```bash
git add scripts/fetch-model models/ppocrv6_rec_keys.json internal/ocr go.mod go.sum
git commit -m "feat(go): PP-OCRv6 recogniser on onnxruntime, ported preprocess and CTC decode"
```

---

### Task 8: OCR eval runner, E1 parity gate and early memory check

**Files:**
- Create: `cmd/awh-ocr-eval/main.go`, `eval/compare_ocr.py`

- [ ] **Step 1: Write `cmd/awh-ocr-eval/main.go`**

```go
// awh-ocr-eval runs the Go OCR over eval/manifest.jsonl and writes one JSON
// line per image with the same keys as eval/ocr_reference.py.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"time"

	"github.com/grillermo/awh/internal/ocr"
	"github.com/grillermo/awh/internal/reading"
	"github.com/grillermo/awh/internal/vision"
)

func main() {
	manifest := flag.String("manifest", "eval/manifest.jsonl", "")
	out := flag.String("out", "eval/results/after/ocr.jsonl", "")
	model := flag.String("model", "models/PP-OCRv6_rec_small.onnx", "")
	keys := flag.String("keys", "models/ppocrv6_rec_keys.json", "")
	lib := flag.String("ort-lib", "/opt/homebrew/lib/libonnxruntime.dylib", "")
	threads := flag.Int("threads", 1, "onnxruntime intra-op threads")
	hold := flag.Duration("hold", 0, "after the run, sleep this long (to measure footprint)")
	flag.Parse()

	if err := ocr.Init(*lib); err != nil {
		log.Fatal(err)
	}
	rec, err := ocr.NewRecognizer(*model, *keys, *threads)
	if err != nil {
		log.Fatal(err)
	}
	type row struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	var rows []row
	in, err := os.Open(*manifest)
	if err != nil {
		log.Fatal(err)
	}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			log.Fatal(err)
		}
		rows = append(rows, r)
	}
	first, err := vision.LoadPNG(rows[0].Path)
	if err != nil {
		log.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rec.Read(first)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		img, err := vision.LoadPNG(r.Path)
		if err != nil {
			log.Fatal(err)
		}
		t0 := time.Now()
		text, score, err := rec.Read(img)
		ms := float64(time.Since(t0).Microseconds()) / 1000
		if err != nil {
			log.Fatalf("%s: %v", r.ID, err)
		}
		kind, value := reading.Classify(text, score)
		enc.Encode(map[string]any{"id": r.ID, "text": text, "score": score,
			"kind": kind, "value": value, "ms": math.Round(ms*100) / 100})
	}
	f.Close()
	log.Printf("wrote %d results to %s", len(rows), *out)
	if *hold > 0 {
		log.Printf("pid %d holding for %s", os.Getpid(), *hold)
		time.Sleep(*hold)
	}
}
```

- [ ] **Step 2: Write `eval/compare_ocr.py`** (gate checker; exits 1 when a hard gate fails)

```python
#!/usr/bin/env python3
"""usage: compare_ocr.py <manifest> <before.jsonl> <after.jsonl> <title>  -> markdown on stdout"""
import json
import statistics
import sys

manifest, before_p, after_p, title = sys.argv[1:5]
truth = {r["id"]: r.get("truth") for r in map(json.loads, open(manifest))}
before = {r["id"]: r for r in map(json.loads, open(before_p))}
after = {r["id"]: r for r in map(json.loads, open(after_p))}
ids = [i for i in before if i in after]
missing = set(before) ^ set(after)

kind_diff = [i for i in ids if before[i]["kind"] != after[i]["kind"]]
text_diff = [i for i in ids if before[i]["text"] != after[i]["text"]]
go_only_errors = [i for i in kind_diff if after[i]["kind"] == "error"]
deltas = sorted(abs(before[i]["score"] - after[i]["score"]) for i in ids)
p99 = deltas[int(0.99 * (len(deltas) - 1))] if deltas else 0.0
cross = [i for i in ids if (before[i]["score"] >= 0.7) != (after[i]["score"] >= 0.7)]


def truth_hits(side):
    labelled = [i for i in ids if truth.get(i)]
    hits = sum(1 for i in labelled if side[i]["value"] == truth[i])
    return hits, len(labelled)


def ms(side):
    v = sorted(side[i]["ms"] for i in ids)
    return statistics.median(v), v[int(0.95 * (len(v) - 1))]


bh, bn = truth_hits(before)
ah, an = truth_hits(after)
gates = {
    "kind agrees on every image": not kind_diff,
    "no Go-only 'error'": not go_only_errors,
    "text agrees on >= 99%": len(text_diff) <= 0.01 * len(ids),
    "|Δscore| p99 <= 0.01": p99 <= 0.01,
    "truth set unchanged": ah == bh,
    "same images on both sides": not missing,
}
print(f"## {title}\n")
print(f"{len(ids)} images. Truth set: before {bh}/{bn}, after {ah}/{an}.\n")
print("| Gate | Result |\n|---|---|")
for name, ok in gates.items():
    print(f"| {name} | {'PASS' if ok else 'FAIL'} |")
print(f"\n|Δscore|: median {statistics.median(deltas):.5f}, p99 {p99:.5f}, max {deltas[-1]:.5f}. "
      f"Images crossing the 0.7 threshold on one side only: {len(cross)}.\n")
bm, bp = ms(before)
am, ap = ms(after)
print(f"OCR latency: before median {bm:.1f} ms / p95 {bp:.1f}; after median {am:.1f} ms / p95 {ap:.1f}.\n")
if text_diff or cross:
    print("| id | before | after |\n|---|---|---|")
    for i in sorted(set(text_diff) | set(cross)):
        b, a = before[i], after[i]
        print(f"| {i} | {b['text']!r} {b['score']:.3f} {b['kind']} | {a['text']!r} {a['score']:.3f} {a['kind']} |")
sys.exit(0 if all(gates.values()) else 1)
```

- [ ] **Step 3: Run E1**

```bash
mkdir -p eval/results/after
CGO_ENABLED=1 go run ./cmd/awh-ocr-eval
python_env/bin/python eval/compare_ocr.py eval/manifest.jsonl eval/results/before/ocr.jsonl eval/results/after/ocr.jsonl "E1 offline OCR parity" | tee eval/results/after/e1.md
```

Expected: every gate PASS. If one fails, look at the listed images: run `dump_fixtures.py` on a failing crop and compare tensors (E2) before touching anything else. Do not continue to Task 9 with a failing E1.

- [ ] **Step 4: Early memory check** (the recogniser is the bulk of the Go server's memory)

```bash
go build -o awh-ocr-eval ./cmd/awh-ocr-eval
./awh-ocr-eval -hold 30s & sleep 25; footprint $! | grep Footprint; wait
```

Expected: about 35–45 MB. **If it is above 70 MB, stop and report.** The Go server can't hit its target, and the user should decide before more work happens.

- [ ] **Step 5: Commit**

```bash
git add cmd/awh-ocr-eval eval/compare_ocr.py eval/results/after
git commit -m "eval: Go OCR runner and E1 parity check"
```

---

### Task 9: `store` (errors.sqlite3)

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`

- [ ] **Step 1: Add the driver**

```bash
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Write the failing test**

```go
package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "awh.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCropDefaultsThenRoundTrips(t *testing.T) {
	s := open(t)
	if got := s.Crop(); got != DefaultCrop {
		t.Fatalf("default %v", got)
	}
	want := [4]float64{0.6035, 0.5494, 0.6426, 0.6233}
	if err := s.SaveCrop(want); err != nil {
		t.Fatal(err)
	}
	if got := s.Crop(); got != want {
		t.Fatalf("got %v", got)
	}
}

func TestReadsWhatPythonWrote(t *testing.T) {
	s := open(t)
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('display_crop', '[0.6035, 0.5494, 0.6426, 0.6233]')`); err != nil {
		t.Fatal(err)
	}
	if got := s.Crop(); got[3] != 0.6233 {
		t.Fatalf("got %v", got)
	}
}

func TestViewNilThenRoundTrips(t *testing.T) {
	s := open(t)
	if s.View() != nil {
		t.Fatal("want nil view")
	}
	v := map[string]float64{"scale": 2.1, "offsetXFrac": -0.9, "offsetYFrac": -1}
	if err := s.SaveView(v); err != nil {
		t.Fatal(err)
	}
	if got := s.View(); got["scale"] != 2.1 {
		t.Fatalf("got %v", got)
	}
}

func TestSelection(t *testing.T) {
	s := open(t)
	if err := s.UpdateSelection([]string{"text/a", "digit/b"}, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSelection([]string{"text/a"}, []string{"digit/b"}, 2); err != nil {
		t.Fatal(err)
	}
	sel, err := s.Selection()
	if err != nil || len(sel) != 1 || !sel["text/a"] {
		t.Fatalf("got %v %v", sel, err)
	}
}
```

- [ ] **Step 3: Run it and watch it fail**

Run: `go test ./internal/store/`
Expected: FAIL, `undefined: Open`.

- [ ] **Step 4: Write `internal/store/store.go`**

```go
// Package store reads and writes errors.sqlite3 with the same tables and
// value formats as auto_reset.py and ocr_samples.py, so either server can run on it.
package store

import (
	"database/sql"
	"encoding/json"

	_ "modernc.org/sqlite"
)

// DefaultCrop is DISPLAY_CROP in auto_reset.py: left, top, right, bottom.
var DefaultCrop = [4]float64{0.631, 0.585, 0.676, 0.691}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS errors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		ocr_text TEXT NOT NULL,
		raw_image BLOB NOT NULL,
		cropped_image BLOB NOT NULL,
		fixed_image BLOB NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ocr_sample_selection (sample_id TEXT PRIMARY KEY, selected_at REAL NOT NULL)`,
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) setting(key string) (string, bool) {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) put(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, string(b))
	return err
}

func (s *Store) Crop() [4]float64 {
	v, ok := s.setting("display_crop")
	var c []float64
	if !ok || json.Unmarshal([]byte(v), &c) != nil || len(c) != 4 {
		return DefaultCrop
	}
	return [4]float64(c)
}

func (s *Store) SaveCrop(c [4]float64) error { return s.put("display_crop", c[:]) }

// View is nil when none was saved (the template renders it as null).
func (s *Store) View() map[string]float64 {
	v, ok := s.setting("display_view")
	var m map[string]float64
	if !ok || json.Unmarshal([]byte(v), &m) != nil {
		return nil
	}
	return m
}

func (s *Store) SaveView(v map[string]float64) error { return s.put("display_view", v) }

func (s *Store) Selection() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT sample_id FROM ocr_sample_selection`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) UpdateSelection(sel, desel []string, now float64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range sel {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO ocr_sample_selection (sample_id, selected_at) VALUES (?, ?)`, id, now); err != nil {
			return err
		}
	}
	for _, id := range desel {
		if _, err := tx.Exec(`DELETE FROM ocr_sample_selection WHERE sample_id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 5: Run it and watch it pass, then commit**

```bash
go test ./internal/store/
git add internal/store go.mod go.sum && git commit -m "feat(go): store for errors.sqlite3 settings and sample selection"
```

---

### Task 10: `samples` (port of `ocr_samples.py`)

**Files:**
- Create: `internal/samples/samples.go`, `internal/samples/samples_test.go`

- [ ] **Step 1: Write the failing test**

```go
package samples

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grillermo/awh/internal/vision"
)

func TestKind(t *testing.T) {
	for text, want := range map[string]string{"": "empty", "  ": "empty", "45": "number", "5": "digit", "4C": "text", "E1": "text"} {
		if got := Kind(text); got != want {
			t.Errorf("Kind(%q) = %q, want %q", text, got, want)
		}
	}
}

func newCollector(t *testing.T, root string, now *float64, rnd float64) *Collector {
	t.Helper()
	c, err := NewCollector(root, MaxBytes, func() float64 { return rnd }, func() float64 { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTextIsSavedAtMostThreeTimesSpacedApart(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	c := newCollector(t, root, &now, 0.5)
	img := vision.New(4, 4)
	saved := 0
	for i := 0; i < 10; i++ {
		rec, err := c.MaybeSave("4C", img, img, 0.8)
		if err != nil {
			t.Fatal(err)
		}
		if rec != nil {
			saved++
		}
		now += 30 // every other attempt is inside the 60 s gap
	}
	if saved != 3 {
		t.Fatalf("saved %d, want 3", saved)
	}
	// A restart reloads the counters from index.jsonl.
	now += 1000
	if rec, _ := newCollector(t, root, &now, 0.5).MaybeSave("4C", img, img, 0.8); rec != nil {
		t.Fatal("saved a 4th copy after restart")
	}
}

func TestNumbersAreNeverSavedAndEmptiesAreSampled(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	img := vision.New(4, 4)
	if rec, _ := newCollector(t, root, &now, 0.0).MaybeSave("45", img, img, 0.9); rec != nil {
		t.Fatal("saved a number")
	}
	if rec, _ := newCollector(t, root, &now, 0.5).MaybeSave("", img, img, 0); rec != nil {
		t.Fatal("saved an empty above the 2% draw")
	}
	rec, err := newCollector(t, root, &now, 0.01).MaybeSave("", img, img, 0)
	if err != nil || rec == nil || rec.Kind != "empty" {
		t.Fatalf("want an empty sample, got %+v %v", rec, err)
	}
	if _, err := os.Stat(rec.OCR); err != nil {
		t.Fatal(err)
	}
}

func TestListNewestFirstSkipsMissingFiles(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	c := newCollector(t, root, &now, 0.5)
	img := vision.New(4, 4)
	a, _ := c.MaybeSave("AB", img, img, 0.8)
	now += 100
	b, _ := c.MaybeSave("CD", img, img, 0.8)
	now += 100
	gone, _ := c.MaybeSave("EF", img, img, 0.8)
	os.Remove(gone.OCR)
	list := List(root)
	if len(list) != 2 || list[0].ID != ID(*b) || list[1].ID != ID(*a) {
		t.Fatalf("got %+v", list)
	}
	if list[0].OCRImage != filepath.Join("text", filepath.Base(b.OCR)) {
		t.Fatalf("ocr_image %q", list[0].OCRImage)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/samples/`
Expected: FAIL, `undefined: Kind`.

- [ ] **Step 3: Write `internal/samples/samples.go`**

```go
// Package samples keeps example frames of OCR misses for later tuning, and
// lists them for the iOS sample picker. Port of ocr_samples.py, same files.
package samples

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/grillermo/awh/internal/vision"
)

const (
	textMaxPerText   = 3
	textMinInterval  = 60.0
	emptyProbability = 0.02
	emptyMinInterval = 600.0
	emptyMaxTotal    = 40
	MaxBytes         = 50 * 1024 * 1024
)

var (
	number  = regexp.MustCompile(`^\p{Nd}+$`)
	slugBad = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// Kind is "number", "digit" (a lone digit), "empty" or "text".
func Kind(text string) string {
	c := compact(text)
	switch {
	case c == "":
		return "empty"
	case number.MatchString(c) && utf8.RuneCountInString(c) == 1:
		return "digit"
	case number.MatchString(c):
		return "number"
	}
	return "text"
}

type Record struct {
	At      float64  `json:"at"`
	Kind    string   `json:"kind"`
	OCRText string   `json:"ocr_text"`
	Raw     string   `json:"raw"`
	OCR     string   `json:"ocr"`
	Score   *float64 `json:"score,omitempty"`
}

type seen struct {
	count int
	last  float64
}

type Collector struct {
	root       string
	maxBytes   int64
	rnd, clock func() float64
	mu         sync.Mutex
	used       int64
	warned     bool
	text       map[string]seen
	emptyCount int
	emptyLast  float64
}

func NewCollector(root string, maxBytes int64, rnd, clock func() float64) (*Collector, error) {
	c := &Collector{root: root, maxBytes: maxBytes, rnd: rnd, clock: clock, text: map[string]seen{}}
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				c.used += info.Size()
			}
		}
		return nil
	})
	for _, rec := range readIndex(root) {
		switch rec.Kind {
		case "text", "digit":
			s := c.text[rec.OCRText]
			c.text[rec.OCRText] = seen{s.count + 1, math.Max(s.last, rec.At)}
		case "empty":
			c.emptyCount++
			c.emptyLast = math.Max(c.emptyLast, rec.At)
		}
	}
	return c, nil
}

func readIndex(root string) []Record {
	f, err := os.Open(filepath.Join(root, "index.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (c *Collector) shouldSave(text string) string {
	kind := Kind(text)
	if kind == "number" {
		return ""
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used >= c.maxBytes {
		if !c.warned {
			log.Printf("[samples] disk budget of %d MB used up; not saving more samples", c.maxBytes>>20)
			c.warned = true
		}
		return ""
	}
	if kind == "text" || kind == "digit" {
		key := strings.TrimSpace(text)
		s := c.text[key]
		if s.count >= textMaxPerText || now-s.last < textMinInterval {
			return ""
		}
		c.text[key] = seen{s.count + 1, now}
		return kind
	}
	if c.emptyCount >= emptyMaxTotal || now-c.emptyLast < emptyMinInterval || c.rnd() >= emptyProbability {
		return ""
	}
	c.emptyCount++
	c.emptyLast = now
	return kind
}

func slug(text string) string {
	s := strings.Trim(slugBad.ReplaceAllString(text, "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "x"
	}
	return s
}

// MaybeSave stores raw (the crop OCR read) and processed (the enhanced frame)
// when this reading is worth keeping. It returns nil when it is not.
func (c *Collector) MaybeSave(text string, raw, processed vision.Image, score float64) (*Record, error) {
	kind := c.shouldSave(text)
	if kind == "" {
		return nil, nil
	}
	now := c.clock()
	name := "empty"
	if kind != "empty" {
		name = slug(text)
	}
	base := time.Unix(0, int64(now*1e9)).Local().Format("20060102-150405") + "_" + name
	dir := filepath.Join(c.root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rawPath, ocrPath := filepath.Join(dir, base+"_raw.png"), filepath.Join(dir, base+"_ocr.png")
	if err := vision.SavePNG(raw, rawPath); err != nil {
		return nil, err
	}
	if err := vision.SavePNG(processed, ocrPath); err != nil {
		return nil, err
	}
	sc := math.Round(score*1000) / 1000
	rec := &Record{At: now, Kind: kind, OCRText: strings.TrimSpace(text), Raw: rawPath, OCR: ocrPath, Score: &sc}
	line, _ := json.Marshal(rec)
	line = append(line, '\n')
	written := int64(len(line))
	for _, p := range []string{rawPath, ocrPath} {
		if info, err := os.Stat(p); err == nil {
			written += info.Size()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(c.root, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return nil, err
	}
	c.used += written
	log.Printf("[samples] saved %s sample %q -> %s", kind, rec.OCRText, ocrPath)
	return rec, nil
}

// ID is "<kind>/<file stem>", e.g. "text/20261106-182945_4C".
func ID(r Record) string {
	return fmt.Sprintf("%s/%s", r.Kind, strings.TrimSuffix(filepath.Base(r.OCR), "_ocr.png"))
}

type Listed struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	OCRText  string  `json:"ocr_text"`
	At       float64 `json:"at"`
	OCRImage string  `json:"ocr_image"`
	RawImage string  `json:"raw_image"`
	Selected bool    `json:"selected"`
}

func rel(root, p string) string {
	ar, _ := filepath.Abs(root)
	ap, _ := filepath.Abs(p)
	r, err := filepath.Rel(ar, ap)
	if err != nil {
		return p
	}
	return r
}

// List returns every sample whose image still exists, newest first.
func List(root string) []Listed {
	out := []Listed{}
	for _, r := range readIndex(root) {
		if _, err := os.Stat(r.OCR); err != nil {
			continue
		}
		out = append(out, Listed{ID: ID(r), Kind: r.Kind, OCRText: r.OCRText, At: r.At,
			OCRImage: rel(root, r.OCR), RawImage: rel(root, r.Raw)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}
```

Paths in `index.jsonl` are relative to the server's working directory (`ocr_samples/text/…`), as Python writes them. The server runs from the repo root and passes `-samples ocr_samples`.

- [ ] **Step 4: Run it and watch it pass, then commit**

```bash
go test ./internal/samples/
git add internal/samples && git commit -m "feat(go): OCR sample collector and listing, port of ocr_samples"
```

---

### Task 11: `tapo` (P100 KLAP client)

**Files:**
- Create: `internal/tapo/klap.go`, `internal/tapo/klap_test.go`, `cmd/tapo-check/main.go`

The Python `tapo` package speaks KLAP to the plug. This is the same protocol, written from its published handshake and tested against a fake device.

- [ ] **Step 1: Write the failing test** (a fake device that runs the device side of the protocol)

```go
package tapo

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type fakeDevice struct {
	auth           []byte
	local, remote  []byte
	on             bool
	calls          []string
}

func (d *fakeDevice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/app/handshake1":
		d.local, d.remote = body, bytes.Repeat([]byte{7}, 16)
		http.SetCookie(w, &http.Cookie{Name: "TP_SESSIONID", Value: "abc"})
		h := sha256.Sum256(cat(d.local, d.remote, d.auth))
		w.Write(cat(d.remote, h[:]))
	case "/app/handshake2":
		want := sha256.Sum256(cat(d.remote, d.local, d.auth))
		if !bytes.Equal(body, want[:]) {
			w.WriteHeader(403)
		}
	case "/app/request":
		seq, _ := strconv.Atoi(r.URL.Query().Get("seq"))
		c := newCipher(d.local, d.remote, d.auth)
		var seqb [4]byte
		binary.BigEndian.PutUint32(seqb[:], uint32(int32(seq)))
		plain, err := c.decrypt(body[32:], int32(seq))
		sig := sha256.Sum256(cat(c.sig, seqb[:], body[32:]))
		if err != nil || !bytes.Equal(sig[:], body[:32]) {
			w.WriteHeader(400)
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				DeviceOn bool `json:"device_on"`
			} `json:"params"`
		}
		json.Unmarshal(plain, &req)
		d.calls = append(d.calls, req.Method+":"+strconv.FormatBool(req.Params.DeviceOn))
		if req.Method == "set_device_info" {
			d.on = req.Params.DeviceOn
		}
		resp, _ := json.Marshal(map[string]any{"error_code": 0, "result": map[string]any{"device_on": d.on, "model": "P100"}})
		w.Write(c.encrypt(resp, int32(seq)))
	}
}

func TestHandshakeInfoAndPowerCycle(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw"), on: true}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "pw", Wait: 0}

	info, err := c.Info(t.Context())
	if err != nil || info["model"] != "P100" {
		t.Fatalf("info %v %v", info, err)
	}
	if err := c.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(dev.calls, ",")
	if got != "get_device_info:false,set_device_info:false,set_device_info:true" || !dev.on {
		t.Fatalf("calls %s, on=%v", got, dev.on)
	}
}

func TestWrongPasswordFailsTheHandshake(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw")}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "nope"}
	if _, err := c.Info(t.Context()); err == nil {
		t.Fatal("want an auth error")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/tapo/`
Expected: FAIL, `undefined: authHash`.

- [ ] **Step 3: Write `internal/tapo/klap.go`**

```go
// Package tapo power-cycles a Tapo P100 plug over its local KLAP protocol,
// replacing reset_tapo100() and the Python tapo package.
package tapo

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	Base            string        // "http://192.168.1.86"
	Email, Password string
	Wait            time.Duration // off -> on gap; 10 s in production
	HTTP            *http.Client
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func authHash(email, password string) []byte {
	e, p := sha1.Sum([]byte(email)), sha1.Sum([]byte(password))
	h := sha256.Sum256(cat(e[:], p[:]))
	return h[:]
}

type klapCipher struct {
	key, iv, sig []byte
	seq          int32
}

func newCipher(local, remote, auth []byte) *klapCipher {
	k := sha256.Sum256(cat([]byte("lsk"), local, remote, auth))
	iv := sha256.Sum256(cat([]byte("iv"), local, remote, auth))
	sig := sha256.Sum256(cat([]byte("ldk"), local, remote, auth))
	return &klapCipher{key: k[:16], iv: iv[:12], sig: sig[:28], seq: int32(binary.BigEndian.Uint32(iv[28:]))}
}

func (c *klapCipher) ivFor(seq int32) []byte {
	out := make([]byte, 16)
	copy(out, c.iv)
	binary.BigEndian.PutUint32(out[12:], uint32(seq))
	return out
}

func (c *klapCipher) encrypt(plain []byte, seq int32) []byte {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(c.key)
	cipher.NewCBCEncrypter(block, c.ivFor(seq)).CryptBlocks(data, data)
	return data
}

func (c *klapCipher) decrypt(data []byte, seq int32) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("tapo: bad ciphertext length")
	}
	out := append([]byte{}, data...)
	block, _ := aes.NewCipher(c.key)
	cipher.NewCBCDecrypter(block, c.ivFor(seq)).CryptBlocks(out, out)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("tapo: bad padding")
	}
	return out[:len(out)-pad], nil
}

type session struct {
	c      *Client
	cookie string
	ci     *klapCipher
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Client) post(ctx context.Context, path, cookie string, body []byte) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("tapo: %s returned %s", path, resp.Status)
	}
	return b, resp, err
}

func (c *Client) handshake(ctx context.Context) (*session, error) {
	auth := authHash(c.Email, c.Password)
	local := make([]byte, 16)
	rand.Read(local)
	body, resp, err := c.post(ctx, "/app/handshake1", "", local)
	if err != nil {
		return nil, err
	}
	if len(body) < 48 {
		return nil, fmt.Errorf("tapo: handshake1 answered %d bytes", len(body))
	}
	remote := body[:16]
	want := sha256.Sum256(cat(local, remote, auth))
	if !bytes.Equal(body[16:48], want[:]) {
		return nil, errors.New("tapo: handshake1 hash mismatch (wrong email or password?)")
	}
	cookie := ""
	for _, ck := range resp.Cookies() {
		if ck.Name == "TP_SESSIONID" {
			cookie = "TP_SESSIONID=" + ck.Value
		}
	}
	h2 := sha256.Sum256(cat(remote, local, auth))
	if _, _, err := c.post(ctx, "/app/handshake2", cookie, h2[:]); err != nil {
		return nil, err
	}
	return &session{c: c, cookie: cookie, ci: newCipher(local, remote, auth)}, nil
}

func (s *session) call(ctx context.Context, payload any) (map[string]any, error) {
	plain, _ := json.Marshal(payload)
	s.ci.seq++
	seq := s.ci.seq
	ct := s.ci.encrypt(plain, seq)
	var seqb [4]byte
	binary.BigEndian.PutUint32(seqb[:], uint32(seq))
	sig := sha256.Sum256(cat(s.ci.sig, seqb[:], ct))
	body, _, err := s.c.post(ctx, "/app/request?seq="+strconv.Itoa(int(seq)), s.cookie, cat(sig[:], ct))
	if err != nil {
		return nil, err
	}
	if len(body) < 32 {
		return nil, errors.New("tapo: short response")
	}
	dec, err := s.ci.decrypt(body[32:], seq)
	if err != nil {
		return nil, err
	}
	var out struct {
		ErrorCode int            `json:"error_code"`
		Result    map[string]any `json:"result"`
	}
	if err := json.Unmarshal(dec, &out); err != nil {
		return nil, err
	}
	if out.ErrorCode != 0 {
		return nil, fmt.Errorf("tapo: error_code %d", out.ErrorCode)
	}
	return out.Result, nil
}

func setOn(on bool) map[string]any {
	return map[string]any{"method": "set_device_info", "params": map[string]any{"device_on": on}}
}

// Info is read-only: it never changes the plug.
func (c *Client) Info(ctx context.Context) (map[string]any, error) {
	s, err := c.handshake(ctx)
	if err != nil {
		return nil, err
	}
	return s.call(ctx, map[string]any{"method": "get_device_info"})
}

// Reset turns the plug off, waits, and turns it back on, like reset_tapo100().
func (c *Client) Reset(ctx context.Context) error {
	s, err := c.handshake(ctx)
	if err != nil {
		return err
	}
	log.Printf("[tapo] Turning off %s", c.Base)
	if _, err := s.call(ctx, setOn(false)); err != nil {
		return err
	}
	log.Printf("[tapo] Waiting %s", c.Wait)
	select {
	case <-time.After(c.Wait):
	case <-ctx.Done():
	}
	// Always try to turn it back on, even if ctx was cancelled during the wait.
	onCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log.Printf("[tapo] Turning on %s", c.Base)
	if _, err := s.call(onCtx, setOn(true)); err != nil {
		// The session may have expired: retry once with a fresh handshake.
		s2, err2 := c.handshake(onCtx)
		if err2 != nil {
			return fmt.Errorf("turn on: %v; re-handshake: %w", err, err2)
		}
		if _, err := s2.call(onCtx, setOn(true)); err != nil {
			return err
		}
	}
	log.Printf("[tapo] Reset complete")
	return nil
}
```

The fake device in the test records `get_device_info:false` for the read-only call, because its `params` decode to the zero value. The test's expected string accounts for that.

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/tapo/`
Expected: `ok`.

- [ ] **Step 5: Write `cmd/tapo-check/main.go`** (read-only probe of the real plug)

```go
// tapo-check prints the plug's device info. It never switches the plug.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/grillermo/awh/internal/tapo"
)

func main() {
	c := &tapo.Client{Base: "http://" + os.Getenv("TAPO_IP"), Email: os.Getenv("TAPO_EMAIL"), Password: os.Getenv("TAPO_PASSWORD")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("model:", info["model"], "device_on:", info["device_on"])
}
```

- [ ] **Step 6: Create the secrets file for the Go server** with the editor (not the shell). It holds the values from `auto_reset.py` (`RTSP_URL`, `TAPO_IP`, `TAPO_EMAIL`, `TAPO_PASSWORD`). The file is the repo's gitignored `.env`; its variables are `AWH_RTSP_URL`, `TAPO_IP`, `TAPO_EMAIL`, `TAPO_PASSWORD`, one `KEY=value` per line.

- [ ] **Step 7: Probe the real plug (read-only)**

```bash
set -a; . ./.env; set +a; go run ./cmd/tapo-check
```

Expected: `model: P100 device_on: true`. If the handshake fails with a hash mismatch on correct credentials, the plug firmware may want KLAP v1 (`md5(md5(email) + md5(password))`). Add that as a fallback in `authHash` and re-run. **Do not test `Reset` on the real plug here.** That happens once, with the user's go-ahead, in Task 19.

- [ ] **Step 8: Commit**

```bash
git add internal/tapo cmd/tapo-check && git commit -m "feat(go): Tapo P100 KLAP client with a fake-device test"
```

---

### Task 12: `capture` (ffmpeg / ffprobe)

**Files:**
- Create: `internal/capture/ffmpeg.go`, `internal/capture/ffmpeg_test.go`

- [ ] **Step 1: Write the test** (an integration test, skipped unless the camera URL is set)

```go
package capture

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

func cam(t *testing.T) Camera {
	url := os.Getenv("AWH_RTSP_URL")
	if url == "" {
		t.Skip("AWH_RTSP_URL not set")
	}
	return Camera{URL: url, Timeout: 20 * time.Second}
}

func TestSizeCropAndJPEG(t *testing.T) {
	c := cam(t)
	w, h, err := c.Size(t.Context())
	if err != nil || w < 320 || h < 240 {
		t.Fatalf("size %dx%d %v", w, h, err)
	}
	r, err := vision.CropRect(w, h, [4]float64{0.6035, 0.5494, 0.6426, 0.6233})
	if err != nil {
		t.Fatal(err)
	}
	img, err := c.Crop(t.Context(), r)
	if err != nil || img.W != r.W || img.H != r.H {
		t.Fatalf("crop %dx%d %v", img.W, img.H, err)
	}
	jpg, err := c.FullJPEG(t.Context())
	if err != nil || !bytes.HasPrefix(jpg, []byte{0xFF, 0xD8}) {
		t.Fatalf("jpeg %d bytes %v", len(jpg), err)
	}
	t.Logf("frame %dx%d, crop %+v", w, h, r)
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `set -a; . ./.env; set +a; go test ./internal/capture/`
Expected: FAIL, `undefined: Camera`.

- [ ] **Step 3: Write `internal/capture/ffmpeg.go`**

```go
// Package capture grabs single frames from the camera's RTSP stream with
// ffmpeg subprocesses. Each call is a short-lived child process, so decoding a
// full frame never grows the server's own heap; the live loop asks ffmpeg for
// the display crop only.
package capture

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

type Camera struct {
	URL     string
	Timeout time.Duration
}

func (c Camera) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Size probes the stream's frame size.
func (c Camera) Size(ctx context.Context) (int, int, error) {
	out, err := c.run(ctx, "ffprobe", "-v", "error", "-rtsp_transport", "tcp",
		"-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", c.URL)
	if err != nil {
		return 0, 0, err
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d", &w, &h); err != nil || w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("ffprobe: unexpected size %q", out)
	}
	return w, h, nil
}

// Crop returns one frame's rect as BGR. It converts to bgr24 before cropping,
// so the pixels equal a crop of the fully converted frame.
func (c Camera) Crop(ctx context.Context, r vision.Rect) (vision.Image, error) {
	out, err := c.run(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-rtsp_transport", "tcp",
		"-i", c.URL, "-frames:v", "1",
		"-vf", fmt.Sprintf("format=bgr24,crop=%d:%d:%d:%d", r.W, r.H, r.X, r.Y),
		"-f", "rawvideo", "-pix_fmt", "bgr24", "-")
	if err != nil {
		return vision.Image{}, err
	}
	if len(out) != r.W*r.H*3 {
		return vision.Image{}, fmt.Errorf("ffmpeg: got %d bytes, want %d", len(out), r.W*r.H*3)
	}
	return vision.Image{W: r.W, H: r.H, Pix: out}, nil
}

// FullJPEG returns one full frame as JPEG, encoded by ffmpeg (for the crop editor).
func (c Camera) FullJPEG(ctx context.Context) ([]byte, error) {
	return c.run(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-rtsp_transport", "tcp",
		"-i", c.URL, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-")
}
```

- [ ] **Step 4: Run it and watch it pass, then commit**

```bash
set -a; . ./.env; set +a; go test ./internal/capture/ -v
git add internal/capture && git commit -m "feat(go): ffmpeg frame capture (crop and full JPEG)"
```

---

### Task 13: Templates

**Files:**
- Create: `web/web.go`, `web/templates/index.html` (Go-template version), `web/templates/live.html` (verbatim copy)

The Python templates stay untouched; the Python server keeps reading them until cutover.

- [ ] **Step 1: Copy and convert**

```bash
mkdir -p web/templates
cp templates/live.html web/templates/live.html
cp templates/index.html web/templates/index.html
sd -F '{% if error %}' '{{if .Error}}' web/templates/index.html
sd -F '{{ error }}' '{{.Error}}' web/templates/index.html
sd -F '{% else %}' '{{else}}' web/templates/index.html
sd -F '{% endif %}' '{{end}}' web/templates/index.html
sd -F '{% if frame_b64 %}' '{{if .FrameB64}}' web/templates/index.html
sd -F '{{ frame_b64 }}' '{{.FrameB64}}' web/templates/index.html
sd -F '{{ crop|tojson }}' '{{.Crop}}' web/templates/index.html
sd -F '{{ initial_view|tojson }}' '{{.InitialView}}' web/templates/index.html
grep -n '{%\|{{' web/templates/index.html
grep -c '{%\|{{' web/templates/live.html || true
```

Expected: only `{{if .Error}}`, `{{.Error}}`, `{{else}}`, `{{end}}` (×2), `{{if .FrameB64}}`, `{{.FrameB64}}`, `{{.Crop}}`, `{{.InitialView}}` remain in index.html, and live.html has 0. `html/template` JSON-encodes values used inside `<script>`, so `{{.Crop}}` renders `[0.6035,0.5494,0.6426,0.6233]` and a nil `InitialView` renders `null`, the same values `tojson` produced.

- [ ] **Step 2: Write `web/web.go`**

```go
// Package web embeds the HTML pages so the binary is self-contained.
package web

import "embed"

//go:embed templates/*.html
var Templates embed.FS
```

- [ ] **Step 3: Commit**

```bash
git add web && git commit -m "feat(go): embedded index and live pages"
```

---

### Task 14: `server` (handlers, frame state, capture loop, shadow recorder)

**Files:**
- Create: `internal/server/server.go`, `internal/server/shadow.go`, `internal/server/server_test.go`

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/vision"
)

type fakeCam struct{}

func (fakeCam) Size(context.Context) (int, int, error) { return 640, 480, nil }
func (fakeCam) Crop(_ context.Context, r vision.Rect) (vision.Image, error) {
	return vision.New(r.W, r.H), nil
}
func (fakeCam) FullJPEG(context.Context) ([]byte, error) { return []byte{0xFF, 0xD8, 0xFF}, nil }

type fakeOCR struct{ text string }

func (f fakeOCR) Read(vision.Image) (string, float64, error) { return f.text, 0.95, nil }

type fakeReset struct{ n int }

func (f *fakeReset) Reset(context.Context) error { f.n++; return nil }

func newTest(t *testing.T, dry bool) (*Server, *fakeReset, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	col, _ := samples.NewCollector(filepath.Join(dir, "s"), samples.MaxBytes, func() float64 { return 1 }, func() float64 { return 0 })
	rs := &fakeReset{}
	s, err := New(fakeCam{}, fakeOCR{"E1"}, rs, st, col, Options{DryRun: dry, SamplesDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	return s, rs, s.Handler()
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestLastFrameBeforeAndAfterATick(t *testing.T) {
	s, _, h := newTest(t, true)
	if code, out := do(t, h, "GET", "/last_frame", ""); code != 503 || out["error"] != "no frame yet" {
		t.Fatalf("%d %v", code, out)
	}
	if err := s.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	code, out := do(t, h, "GET", "/last_frame", "")
	if code != 200 || out["ocr_kind"] != "error" || out["ocr_text"] != "E1" || out["frame_b64"] == "" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestUpdateCropMovesOneEdgePersistsAndClearsTheFrame(t *testing.T) {
	s, _, h := newTest(t, true)
	s.tick(t.Context())
	before := s.store.Crop()
	code, out := do(t, h, "POST", "/api/update-crop", `{"edge":"left","dir":1}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if got := s.store.Crop()[0]; got != before[0]+0.001 {
		t.Fatalf("left %v -> %v", before[0], got)
	}
	if code, _ := do(t, h, "GET", "/last_frame", ""); code != 503 {
		t.Fatal("frame not cleared")
	}
	if code, _ := do(t, h, "POST", "/api/update-crop", `{"edge":"nope","dir":1}`); code != 400 {
		t.Fatal("bad edge accepted")
	}
	if code, _ := do(t, h, "POST", "/api/update-crop", `{"edge":"left","dir":5}`); code != 400 {
		t.Fatal("bad dir accepted")
	}
}

func TestValidation(t *testing.T) {
	_, _, h := newTest(t, true)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/save-view", `{"view":{"scale":1}}`},
		{"POST", "/api/save-crop", `{"crop":[1,2]}`},
		{"POST", "/api/ocr-samples/selection", `{"select":"x"}`},
		{"GET", "/api/display?display_crop=1,2,3", ""},
		{"GET", "/api/display?display_crop=0.7,0.5,0.6,0.6", ""},
	} {
		if code, out := do(t, h, c.method, c.path, c.body); code != 400 && code != 500 || out["ok"] != false {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}
}

func TestResetHonoursDryRun(t *testing.T) {
	_, rs, h := newTest(t, true)
	do(t, h, "POST", "/reset", "")
	if rs.n != 0 {
		t.Fatal("dry run reset the plug")
	}
	_, rs, h = newTest(t, false)
	if code, out := do(t, h, "POST", "/reset", ""); code != 200 || out["message"] != "reset initiated" || rs.n != 1 {
		t.Fatalf("%d %v n=%d", code, out, rs.n)
	}
}

func TestSampleImagesRejectTraversal(t *testing.T) {
	_, _, h := newTest(t, true)
	for _, p := range []string{"/ocr-samples/nope/a.png", "/ocr-samples/text/a.txt", "/ocr-samples/text/../../db.sqlite3"} {
		if code, _ := do(t, h, "GET", p, ""); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestIndexRendersCropAsJSON(t *testing.T) {
	_, _, h := newTest(t, true)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "const INITIAL_RECT = [0.631,0.585,0.676,0.691]") ||
		!strings.Contains(body, "const INITIAL_VIEW = null") {
		t.Fatalf("%d\n%s", w.Code, body[:min(400, len(body))])
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/server/`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Write `internal/server/server.go`**

```go
// Package server is the HTTP API and live capture loop, a port of app.py.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grillermo/awh/internal/reading"
	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/vision"
	"github.com/grillermo/awh/web"
)

const (
	cropStep    = 0.001
	idleAfter   = 10 * time.Second // the loop sleeps when nobody polled /last_frame for this long
	jpegQuality = 85
)

type Camera interface {
	Size(ctx context.Context) (int, int, error)
	Crop(ctx context.Context, r vision.Rect) (vision.Image, error)
	FullJPEG(ctx context.Context) ([]byte, error)
}

type Reader interface {
	Read(vision.Image) (string, float64, error)
}

type Resetter interface {
	Reset(ctx context.Context) error
}

type Options struct {
	DryRun     bool   // never touch the plug
	SamplesDir string // where /ocr-samples/ images are served from
	ShadowDir  string // when set, record a crop + reading every 30 s (eval E3)
}

type frame struct{ b64, text, kind string }

type Server struct {
	cam     Camera
	ocr     Reader
	reset   Resetter
	store   *store.Store
	samples *samples.Collector
	opts    Options
	pages   *template.Template

	cropMu sync.Mutex
	crop   [4]float64

	sizeMu sync.Mutex
	w, h   int

	frameMu sync.Mutex
	frame   frame

	lastReq atomic.Int64 // unix nanos of the last /last_frame
	shadow  *shadowRecorder
}

func New(cam Camera, ocr Reader, reset Resetter, st *store.Store, col *samples.Collector, opts Options) (*Server, error) {
	pages, err := template.ParseFS(web.Templates, "templates/index.html")
	if err != nil {
		return nil, err
	}
	s := &Server{cam: cam, ocr: ocr, reset: reset, store: st, samples: col, opts: opts, pages: pages, crop: st.Crop()}
	if opts.ShadowDir != "" {
		s.shadow = newShadowRecorder(opts.ShadowDir, 30*time.Second)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /live", s.live)
	mux.HandleFunc("GET /last_frame", s.lastFrame)
	mux.HandleFunc("GET /api/display", s.display)
	mux.HandleFunc("POST /api/save-crop", s.saveCrop)
	mux.HandleFunc("POST /api/save-view", s.saveView)
	mux.HandleFunc("POST /api/update-crop", s.updateCrop)
	mux.HandleFunc("POST /reset", s.resetPlug)
	mux.HandleFunc("GET /api/ocr-samples", s.listSamples)
	mux.HandleFunc("POST /api/ocr-samples/selection", s.selectSamples)
	mux.HandleFunc("GET /ocr-samples/{kind}/{file...}", s.sampleImage)
	mux.HandleFunc("POST /report-error", s.reportError)
	return recoverJSON(mux)
}

// --- JSON helpers -----------------------------------------------------------

type obj = map[string]any

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, obj{"ok": false, "error": msg})
}

func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("[http] panic on %s %s: %v", r.Method, r.URL.Path, v)
				fail(w, 500, fmt.Sprint(v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- state ------------------------------------------------------------------

func (s *Server) currentCrop() [4]float64 {
	s.cropMu.Lock()
	defer s.cropMu.Unlock()
	return s.crop
}

func (s *Server) setFrame(f frame) { s.frameMu.Lock(); s.frame = f; s.frameMu.Unlock() }
func (s *Server) clearFrame()      { s.setFrame(frame{}) }

func (s *Server) frameSize(ctx context.Context) (int, int, error) {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	if s.w == 0 {
		w, h, err := s.cam.Size(ctx)
		if err != nil {
			return 0, 0, err
		}
		s.w, s.h = w, h
	}
	return s.w, s.h, nil
}

func (s *Server) forgetSize() { s.sizeMu.Lock(); s.w, s.h = 0, 0; s.sizeMu.Unlock() }

func (s *Server) grab(ctx context.Context, c [4]float64) (vision.Image, error) {
	w, h, err := s.frameSize(ctx)
	if err != nil {
		return vision.Image{}, err
	}
	r, err := vision.CropRect(w, h, c)
	if err != nil {
		return vision.Image{}, err
	}
	img, err := s.cam.Crop(ctx, r)
	if err != nil {
		s.forgetSize() // the stream may have changed resolution
	}
	return img, err
}

// --- live loop ----------------------------------------------------------------

// Loop runs the capture loop until ctx ends, like app._capture_loop.
func (s *Server) Loop(ctx context.Context) {
	for ctx.Err() == nil {
		if time.Since(time.Unix(0, s.lastReq.Load())) > idleAfter {
			sleep(ctx, time.Second)
			continue
		}
		if err := s.tick(ctx); err != nil {
			log.Printf("[live] capture error: %v", err)
			sleep(ctx, time.Second)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (s *Server) tick(ctx context.Context) error {
	cropped, err := s.grab(ctx, s.currentCrop())
	if err != nil {
		return err
	}
	// The enhanced, perspective-fixed frame is for people; OCR reads the raw crop.
	fixed := vision.Enhance(vision.FixPerspective(cropped))
	text, score, err := s.ocr.Read(cropped)
	if err != nil {
		return err
	}
	kind, value := reading.Classify(text, score)
	if s.samples != nil {
		if _, err := s.samples.MaybeSave(text, cropped, fixed, score); err != nil {
			log.Printf("[samples] save failed: %v", err)
		}
	}
	if s.shadow != nil {
		s.shadow.maybeRecord(cropped, text, score, kind)
	}
	b64, err := vision.EncodeJPEGBase64(fixed, jpegQuality)
	if err != nil {
		return err
	}
	s.setFrame(frame{b64: b64, text: value, kind: kind})
	return nil
}

// --- handlers -----------------------------------------------------------------

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data := struct {
		FrameB64    string
		Crop        [4]float64
		InitialView map[string]float64
		Error       string
	}{Crop: s.store.Crop(), InitialView: s.store.View()}
	if jpg, err := s.cam.FullJPEG(r.Context()); err != nil {
		log.Printf("[index] %v", err)
		data.Error = err.Error()
	} else {
		data.FrameB64 = base64.StdEncoding.EncodeToString(jpg)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("[index] template: %v", err)
	}
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	page, err := web.Templates.ReadFile("templates/live.html")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

func (s *Server) lastFrame(w http.ResponseWriter, r *http.Request) {
	s.lastReq.Store(time.Now().UnixNano())
	s.frameMu.Lock()
	f := s.frame
	s.frameMu.Unlock()
	if f.b64 == "" {
		fail(w, 503, "no frame yet")
		return
	}
	writeJSON(w, 200, obj{"ok": true, "frame_b64": f.b64, "ocr_text": f.text, "ocr_kind": f.kind})
}

// parseCropArg mirrors app.parse_display_crop_arg (empty means DISPLAY_CROP).
func parseCropArg(v string) ([4]float64, error) {
	if v == "" {
		return store.DefaultCrop, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return [4]float64{}, errors.New("display_crop must have 4 comma-separated values")
	}
	var c [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return c, err
		}
		if f < 0 || f > 1 {
			return c, errors.New("display_crop values must be between 0 and 1")
		}
		c[i] = f
	}
	if c[0] >= c[2] || c[1] >= c[3] {
		return c, errors.New("display_crop must satisfy left < right and top < bottom")
	}
	return c, nil
}

func (s *Server) display(w http.ResponseWriter, r *http.Request) {
	c, err := parseCropArg(r.URL.Query().Get("display_crop"))
	if err != nil {
		fail(w, 500, err.Error()) // app.py answers 500 here too
		return
	}
	img, err := s.grab(r.Context(), c)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	b64, err := vision.EncodeJPEGBase64(vision.Enhance(vision.FixPerspective(img)), jpegQuality)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "fixed_frame_b64": b64})
}

func (s *Server) saveCrop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Crop []float64 `json:"crop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Crop) != 4 {
		fail(w, 400, "crop must be array of 4 floats")
		return
	}
	c := [4]float64(body.Crop)
	if err := s.store.SaveCrop(c); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.cropMu.Lock()
	s.crop = c
	s.cropMu.Unlock()
	s.clearFrame()
	writeJSON(w, 200, obj{"ok": true, "crop": body.Crop})
}

func (s *Server) saveView(w http.ResponseWriter, r *http.Request) {
	var body struct {
		View map[string]float64 `json:"view"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	safe := map[string]float64{}
	for _, k := range []string{"scale", "offsetXFrac", "offsetYFrac"} {
		v, ok := body.View[k]
		if !ok {
			fail(w, 400, "view must have scale, offsetXFrac, offsetYFrac")
			return
		}
		safe[k] = v
	}
	if err := s.store.SaveView(safe); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "view": safe})
}

func (s *Server) updateCrop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Edge string  `json:"edge"`
		Dir  float64 `json:"dir"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	idx, ok := map[string]int{"left": 0, "top": 1, "right": 2, "bottom": 3}[body.Edge]
	if !ok {
		fail(w, 400, "edge must be left/top/right/bottom")
		return
	}
	if body.Dir != 1 && body.Dir != -1 {
		fail(w, 400, "dir must be -1 or 1")
		return
	}
	s.cropMu.Lock()
	c := s.crop
	c[idx] = math.Min(1, math.Max(0, math.Round((c[idx]+body.Dir*cropStep)*1e4)/1e4))
	if c[0] >= c[2] || c[1] >= c[3] {
		s.cropMu.Unlock()
		fail(w, 400, "invalid crop bounds")
		return
	}
	s.crop = c
	s.cropMu.Unlock()
	if err := s.store.SaveCrop(c); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.clearFrame()
	writeJSON(w, 200, obj{"ok": true, "crop": c[:]})
}

func (s *Server) resetPlug(w http.ResponseWriter, r *http.Request) {
	if s.opts.DryRun {
		log.Printf("[tapo] dry run: reset requested, plug left alone")
	} else if err := s.reset.Reset(r.Context()); err != nil {
		log.Printf("[tapo] ERROR: %v", err) // app.py logs and still answers ok
	}
	writeJSON(w, 200, obj{"ok": true, "message": "reset initiated"})
}

func (s *Server) listSamples(w http.ResponseWriter, r *http.Request) {
	sel, err := s.store.Selection()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := samples.List(s.opts.SamplesDir)
	for i := range list {
		list[i].Selected = sel[list[i].ID]
	}
	writeJSON(w, 200, obj{"ok": true, "samples": list})
}

func (s *Server) selectSamples(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Select   []string `json:"select"`
		Deselect []string `json:"deselect"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "select and deselect must be arrays of sample ids")
		return
	}
	if err := s.store.UpdateSelection(body.Select, body.Deselect, float64(time.Now().UnixNano())/1e9); err != nil {
		fail(w, 500, err.Error())
		return
	}
	sel, err := s.store.Selection()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "selected_count": len(sel)})
}

func (s *Server) sampleImage(w http.ResponseWriter, r *http.Request) {
	kind, file := r.PathValue("kind"), r.PathValue("file")
	if (kind != "text" && kind != "digit" && kind != "empty") || !strings.HasSuffix(file, ".png") ||
		strings.Contains(file, "..") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.opts.SamplesDir, kind, filepath.Clean("/"+file)))
}

func (s *Server) reportError(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	log.Printf("[report-error] %s", body)
	writeJSON(w, 200, obj{"ok": true})
}
```

- [ ] **Step 4: Write `internal/server/shadow.go`**

```go
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

// shadowRecorder saves one crop and its Go reading every interval, as
// manifest lines ({"id", "path", ...}) that eval/ocr_reference.py and
// awh-ocr-eval can both replay (eval E3).
type shadowRecorder struct {
	dir   string
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

func newShadowRecorder(dir string, every time.Duration) *shadowRecorder {
	return &shadowRecorder{dir: dir, every: every}
}

func (r *shadowRecorder) maybeRecord(crop vision.Image, text string, score float64, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.last) < r.every {
		return
	}
	r.last = now
	stamp := now.Format("20060102-150405")
	crops := filepath.Join(r.dir, "crops")
	if err := os.MkdirAll(crops, 0o755); err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	path, _ := filepath.Abs(filepath.Join(crops, stamp+".png"))
	if err := vision.SavePNG(crop, path); err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	line, _ := json.Marshal(map[string]any{"id": "shadow/" + stamp, "path": path,
		"go_text": text, "go_score": score, "go_kind": kind, "at": now.Unix()})
	f, err := os.OpenFile(filepath.Join(r.dir, "manifest.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", line)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/server/`
Expected: `ok`. If `TestIndexRendersCropAsJSON` fails on spacing, print the rendered line and adjust the test's expected substring. The JS value is what matters, not the whitespace.

- [ ] **Step 6: Commit**

```bash
git add internal/server && git commit -m "feat(go): HTTP API, live loop and shadow recorder, port of app.py"
```

---

### Task 15: `cmd/awh` and run scripts

**Files:**
- Create: `cmd/awh/main.go`, `serve-go`, `serve-python`

- [ ] **Step 1: Write `cmd/awh/main.go`**

```go
// awh serves the water-heater monitor: live OCR of the heater display and a
// Tapo plug reset. Go port of app.py; see docs/superpowers/plans/2026-11-18-go-server-rewrite.md.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grillermo/awh/internal/capture"
	"github.com/grillermo/awh/internal/ocr"
	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/server"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/tapo"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", net.JoinHostPort(env("HOST", "0.0.0.0"), env("PORT", "5000")), "listen address")
	db := flag.String("db", "errors.sqlite3", "SQLite file shared with the Python server")
	samplesDir := flag.String("samples", "ocr_samples", "OCR sample folder")
	model := flag.String("model", "models/PP-OCRv6_rec_small.onnx", "")
	keys := flag.String("keys", "models/ppocrv6_rec_keys.json", "")
	ortLib := flag.String("ort-lib", "/opt/homebrew/lib/libonnxruntime.dylib", "")
	threads := flag.Int("ocr-threads", 1, "onnxruntime intra-op threads")
	dryRun := flag.Bool("dry-run", false, "never switch the Tapo plug")
	shadowDir := flag.String("shadow-dir", "", "record a crop + reading every 30 s here (eval)")
	flag.Parse()

	rtsp := os.Getenv("AWH_RTSP_URL")
	if rtsp == "" {
		log.Fatal("AWH_RTSP_URL is not set")
	}
	plug := &tapo.Client{Base: "http://" + os.Getenv("TAPO_IP"), Email: os.Getenv("TAPO_EMAIL"),
		Password: os.Getenv("TAPO_PASSWORD"), Wait: 10 * time.Second}
	if !*dryRun && (os.Getenv("TAPO_IP") == "" || plug.Email == "" || plug.Password == "") {
		log.Fatal("TAPO_IP, TAPO_EMAIL and TAPO_PASSWORD are required unless -dry-run")
	}

	if err := ocr.Init(*ortLib); err != nil {
		log.Fatal(err)
	}
	rec, err := ocr.NewRecognizer(*model, *keys, *threads)
	if err != nil {
		log.Fatal(err)
	}
	defer rec.Close()
	st, err := store.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	clock := func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	col, err := samples.NewCollector(*samplesDir, samples.MaxBytes, rand.Float64, clock)
	if err != nil {
		log.Fatal(err)
	}
	cam := capture.Camera{URL: rtsp, Timeout: 20 * time.Second}
	srv, err := server.New(cam, rec, plug, st, col, server.Options{DryRun: *dryRun, SamplesDir: *samplesDir, ShadowDir: *shadowDir})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Loop(ctx)
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Printf("awh listening on %s (dry-run=%v)", *addr, *dryRun)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
```

- [ ] **Step 2: Write `serve-python`** (an exact copy of today's `serve`, kept for rollback)

```bash
#!/usr/bin/env bash

source python_env/bin/activate
HOST=0.0.0.0 PORT=3060 python3 ./app.py
```

- [ ] **Step 3: Write `serve-go`** (`GOMEMLIMIT` makes the Go GC return memory early; the server's working set is a few MB)

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
set -a; . ./.env; set +a
go build -o awh ./cmd/awh
exec env GOMEMLIMIT=24MiB HOST="${HOST:-0.0.0.0}" PORT="${PORT:-3060}" ./awh "$@"
```

- [ ] **Step 4: Build, vet, smoke-test on a spare port (dry run, separate DB and samples so production is untouched)**

```bash
chmod +x serve-go serve-python && shellcheck serve-go serve-python
go vet ./... && go test ./...
mkdir -p eval/shadow && cp errors.sqlite3 eval/shadow/awh.sqlite3
PORT=3061 ./serve-go -dry-run -db eval/shadow/awh.sqlite3 -samples eval/shadow/ocr_samples &
sleep 3; eval/http_contract.sh 3061; kill %1
```

Expected: the same lines as `eval/results/before/http.txt`.

- [ ] **Step 5: Commit**

```bash
git add cmd/awh serve-go serve-python && git commit -m "feat(go): awh server binary and run scripts"
```

---

### Task 16: 24-hour shadow run (E3, E4, E5)

Python keeps serving production on `:3060`. Go runs beside it on `:3061`, dry run, with its own DB copy and samples folder.

**Files:**
- Create: `eval/ab_poll.py`

- [ ] **Step 1: Write `eval/ab_poll.py`**

```python
#!/usr/bin/env python3
"""usage: ab_poll.py <seconds> <out.csv> -- polls :3060 (Python) and :3060+1 (Go) every 2 s."""
import csv
import json
import sys
import time
import urllib.request

secs, out = int(sys.argv[1]), sys.argv[2]


def read(port):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/last_frame", timeout=20) as r:
            d = json.load(r)
        return d.get("ocr_kind", ""), d.get("ocr_text", "")
    except Exception as exc:  # 503 before the first frame, timeouts
        return "unavailable", type(exc).__name__


with open(out, "w", newline="") as fh:
    w = csv.writer(fh)
    w.writerow(["epoch", "py_kind", "py_text", "go_kind", "go_text"])
    end = time.time() + secs
    while time.time() < end:
        w.writerow([int(time.time()), *read(3060), *read(3061)])
        fh.flush()
        time.sleep(2)

rows = list(csv.DictReader(open(out)))
both = [r for r in rows if "unavailable" not in (r["py_kind"], r["go_kind"])]
agree = sum(r["py_kind"] == r["go_kind"] for r in both)
go_only = [r for r in both if r["go_kind"] == "error" and r["py_kind"] != "error"]
print(f"{len(both)} paired polls, kind agreement {agree / max(1, len(both)):.2%}, "
      f"Go-only errors {len(go_only)}")
for r in go_only:
    print("  go-only error:", r)
```

- [ ] **Step 2: Start the shadow server and the three watchers** (each in its own tmux window, so they survive the SSH session)

```bash
tmux new-session -d -s awh-shadow 'PORT=3061 ./serve-go -dry-run -db eval/shadow/awh.sqlite3 -samples eval/shadow/ocr_samples -shadow-dir eval/shadow 2>&1 | tee eval/shadow/server.log'
sleep 10
GOPID=$(pgrep -f '^\./awh')   # the Python server is "awh" without ./
tmux new-window -t awh-shadow "python_env/bin/python eval/ab_poll.py 86400 eval/results/after/ab.csv | tee eval/results/after/ab.txt"
tmux new-window -t awh-shadow "eval/memwatch.sh $GOPID 86400 60 eval/results/after/mem-24h.csv"
```

`ab_poll.py` polling both servers keeps both live loops running, so the 24 h memory series is under active load.

- [ ] **Step 3: After 24 h, idle and 30-min active samples for Go** (stop `ab_poll` first, or the idle sample isn't idle)

```bash
eval/memwatch.sh "$GOPID" 600 5 eval/results/after/mem-idle.csv
eval/poll.sh 3061 1800 & eval/memwatch.sh "$GOPID" 1800 5 eval/results/after/mem-active.csv; wait
python_env/bin/python eval/mem_summary.py eval/results/after/mem-*.csv | tee eval/results/after/mem-summary.txt
```

- [ ] **Step 4: E3, live crops through both OCRs**

```bash
python_env/bin/python eval/ocr_reference.py eval/shadow/manifest.jsonl eval/results/before/ocr-shadow.jsonl
./awh-ocr-eval -manifest eval/shadow/manifest.jsonl -out eval/results/after/ocr-shadow.jsonl
python_env/bin/python eval/compare_ocr.py eval/shadow/manifest.jsonl eval/results/before/ocr-shadow.jsonl eval/results/after/ocr-shadow.jsonl "E3 live OCR parity (24 h of shadow crops)" | tee eval/results/after/e3.md
```

Expected: about 2,880 crops, every gate PASS.

- [ ] **Step 5: Cold start**

```bash
tmux kill-session -t awh-shadow
eval/startup.sh 3061 -- env PORT=3061 ./serve-go -dry-run -db eval/shadow/awh.sqlite3 -samples eval/shadow/ocr_samples | tee eval/results/after/startup.txt
```

- [ ] **Step 6: Check the gates and commit the results**

Compare against the table in "Evaluation design". If a gate fails, stop and report to the user with the numbers.

```bash
git add eval/ab_poll.py eval/results/after && git commit -m "eval: 24 h shadow run results (after)"
```

---

### Task 17: HTTP contract check (E7)

- [ ] **Step 1: Compare**

```bash
PORT=3061 ./serve-go -dry-run -db eval/shadow/awh.sqlite3 -samples eval/shadow/ocr_samples & sleep 3
eval/http_contract.sh 3061 > eval/results/after/http.txt; kill %1
diff eval/results/before/http.txt eval/results/after/http.txt && echo "E7 PASS"
```

Expected: `E7 PASS`. A difference in a 4xx body's keys or status is a port bug: fix it in `internal/server`, add a test, then re-run.

- [ ] **Step 2: Commit**

```bash
git add eval/results/after/http.txt && git commit -m "eval: HTTP contract matches"
```

---

### Task 18: Before/after report

**Files:**
- Create: `eval/REPORT.md`

- [ ] **Step 1: Write `eval/REPORT.md`** from the result files, with these sections in this order, numbers copied from the files named:

1. **Summary**: one table, Python vs Go: active median and p95 footprint, idle footprint, 24 h slope, peak, cold start, OCR median/p95 ms (from `mem-summary.txt` ×2, `startup.txt` ×2, `e1.md`).
2. **Leak finding**: the 48 h Python slope (`before/mem-48h.csv`) and what it showed.
3. **OCR parity**: paste `e1.md` and `e3.md` whole, then the E4 line from `ab.txt`.
4. **HTTP contract**: E7 result.
5. **Setup**: onnxruntime versions (Python and Homebrew), model SHA-256 (from `scripts/fetch-model`), frame size, Go version, date.

- [ ] **Step 2: Commit**

```bash
git add eval/REPORT.md && git commit -m "eval: before/after report for the Go rewrite"
```

---

### Task 19: Cutover (needs the user's go-ahead)

Production runs `bash ./serve` (parent of the `awh` process) on the macmini, outside launchd and tmux. On 2026-11-18 that was pid 62566.

- [ ] **Step 1: Ask the user** before restarting anything. Show them `eval/REPORT.md`'s summary table. Separately, ask whether to test one real reset (it switches the heater off for 10 s) through the Go server.

- [ ] **Step 2: Point `serve` at Go**

```bash
cp serve-go serve
git add serve && git commit -m "Run the Go server in production"
```

- [ ] **Step 3: Restart** (after approval). Find the terminal or session that started `./serve` (`ps -o ppid= -p "$(pgrep -f '^bash ./serve')"`), stop it with Ctrl-C there, and run `./serve` again in the same place. Then:

```bash
curl -s http://127.0.0.1:3060/last_frame | jq '{ok, ocr_kind, ocr_text}'
```

- [ ] **Step 4: Optional real reset** (only if the user said yes in Step 1)

```bash
curl -s -X POST http://127.0.0.1:3060/reset
```

Expected: the server log shows `[tapo] Turning off` … `Reset complete` and the heater comes back.

- [ ] **Step 5: 24 h production memory after cutover**

```bash
nohup eval/memwatch.sh "$(pgrep -f '^\./awh')" 86400 60 eval/results/after/mem-prod-24h.csv >/dev/null 2>&1 &
```

Add its summary to `eval/REPORT.md` next to `before/mem-48h.csv`, then commit.

- [ ] **Step 6: Rollback, if needed**

```bash
cp serve-python serve
```

Then restart as in Step 3. Both servers use the same `errors.sqlite3` tables and formats, so no data step is needed.

- [ ] **Step 7: Clean up** after a week of stable running: offer the user a follow-up to delete the Python server files and `python_env` (keep `ocr_bench/` and the `eval/` Python scripts, which need `python_env` for future model comparisons, so ask which to keep).
