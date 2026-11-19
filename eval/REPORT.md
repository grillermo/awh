# AWH Go rewrite: before/after report

Date: 2026-11-19. "Before" is the unmodified Python server, "after" is the Go binary (`-dry-run`, port 3061, own db copy and samples dir). Production on port 3060 was not touched.

## 1. Summary

| Measure | Python | Go |
|---|---|---|
| Footprint, active median (30 min, ~1 s poller) | 116 MB | 70 MB |
| Footprint, active p95 | 116 MB | 72 MB |
| Footprint, active max | 125 MB | 73 MB |
| Footprint, idle (10 min) | 125 MB (median, max 125) | 49 MB |
| 30-min slope (rough, active) | -0.82 MB/h | +0.25 MB/h |
| Peak since process start | 189.3 MB (`before/vmmap.txt`, 11-day-old process) | not applicable (fresh process, max 73 MB in the samples) |
| Cold start to first `/last_frame` 200 | 3.09 s (`before/startup.txt`) | 6.18 s (`eval/startup.sh 3061`, one run, measured 2026-11-19; the log shows the camera loop was already erroring at kill time, run once only) |
| OCR median / p95 per crop | 9.3 ms / 10.8 ms | 28.3 ms / 28.8 ms (1 ORT thread) |

Go OCR latency by onnxruntime thread count (Task 8): 1 thread 28 ms, 2 threads 16.2 ms, 4 threads 9.9 ms, 8 threads 13.6 ms. The shipped setting is 1 thread, to keep memory and CPU low.

Note: Python idle (125 MB) is higher than its active median (116 MB). Both are copied as measured; the cause was not investigated.

## 2. Leak note

- 30-minute slopes: Python -0.82 MB/h, Go +0.25 MB/h. Both are within noise for 0.5 h of samples.
- Python lifetime peak is 189 MB against 116 MB at the time of measurement.
- A slow leak was NOT tested. No day-long polling was run, by choice. These numbers neither prove nor rule out a leak in either server.

## 3. OCR parity

### E1 (`eval/results/after/e1.md`, pasted whole)

## E1 offline OCR parity

279 images. Truth set: before 33/33, after 33/33.

| Gate | Result |
|---|---|
| kind agrees on every image | PASS |
| no Go-only 'error' | PASS |
| text agrees on >= 99% | PASS |
| |Δscore| p99 <= 0.01 | PASS |
| truth set unchanged | PASS |
| same images on both sides | PASS |

|Δscore|: median 0.00000, p99 0.00001, max 0.00005. Images crossing the 0.7 threshold on one side only: 0.

OCR latency: before median 9.3 ms / p95 10.8; after median 28.3 ms / p95 28.8.



### E3 (live-crop parity): not run

There is no `e3.md`. The 1-hour shadow run (Task 16) was abandoned by the user's decision after the memory samples were taken.

What was done:
- E1 offline parity (279 images), E6 OCR latency, E7 HTTP contract.
- E5 memory: idle 10 min and active 30 min, both servers.

What was NOT done:
- E3 live-crop parity (the ~360 crops the shadow server would have saved over an hour).
- The 1-hour shadow run itself.
- A measurement of Go cold start beyond the single run in the summary table (one sample, noisy).
- Any test for a slow leak.

## 4. HTTP contract (E7)

`eval/http_contract.sh 3061` against the Go server, diffed with `eval/results/before/http.txt`: identical, "E7 PASS" (status code and top-level JSON keys for 13 requests).

Two things worth knowing:
- Python returns 500 with `["error","ok"]` for `display_crop=1,2,3`; the Go port matches that on purpose.
- The first run differed on one line: Go answered `GET /ocr-samples/nope/x.png` with a plain-text 404 (Go's `http.NotFound`) where Flask sends an HTML page. Status was the same, but the contract script classifies the body differently. Fixed in `internal/server/server.go` (`notFoundHTML`, test `TestSampleImage404IsHTML`) and the re-run was identical.

## 5. Setup

| Item | Value |
|---|---|
| onnxruntime, Python | 1.31.1 |
| onnxruntime, Go (Homebrew) | 1.32.0 |
| Model | `PP-OCRv6_rec_small.onnx`, SHA-256 `6f327246b50388f3c176ae304bd95767ea6dc0c9ae92153ef8cbe210b3c14884` |
| Go | go1.27.4 darwin/arm64 |
| Frame size | 1440p camera stream (2560x1440 per the plan); not re-probed for this report because the camera was unreachable ("No route to host") in the shadow logs |
| Machine | macmini (Mac14,3, M2, 16 GB) |
| Date | 2026-11-19 |

## Gate results

| Gate | Threshold | Measured | Result |
|---|---|---|---|
| E1 OCR parity (all sub-gates) | see `e1.md` | all six PASS | PASS |
| E5 idle | <= 50 MB | 49 MB | PASS |
| E5 active median | <= 60 MB | 70 MB | FAIL |
| E5 active max | <= 80 MB | 73 MB | PASS |
| E6 Go OCR median | < 50 ms | 28.3 ms | PASS |
| E6 Go OCR median vs Python | <= 2x Python (9.3 ms, so <= 18.6 ms) | 28.3 ms (3.0x) | FAIL |
| E7 HTTP contract | identical | identical (after the 404 fix) | PASS |
| E3 live-crop parity | same as E1 | not run | NOT RUN |

Go active median is 70 MB, under the 80 MB stop-and-report line but over the 60 MB gate. Against Python the Go server uses about 46 MB less in the active median (116 to 70 MB) and 76 MB less at idle (125 to 49 MB).
