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
