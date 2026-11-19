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
