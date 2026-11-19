#!/usr/bin/env bash
# Prints "<method> <endpoint> <status> <sorted top-level JSON keys>" for each read-only endpoint.
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
