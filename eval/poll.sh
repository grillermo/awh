#!/usr/bin/env bash
# usage: eval/poll.sh <port> <seconds>
set -euo pipefail
end=$((SECONDS + $2))
while [ "$SECONDS" -lt "$end" ]; do
  curl -s -o /dev/null --max-time 20 "http://127.0.0.1:$1/last_frame" || true
  sleep 1
done
