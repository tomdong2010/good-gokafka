#!/usr/bin/env bash
# Sends messages to the producer so the Grafana dashboard has something to show.
# Usage: scripts/load.sh [count] [url]    (roughly 1 in 20 requests is invalid on purpose)
set -euo pipefail

count=${1:-500}
url=${2:-http://localhost:3000/api/send}
senders=(alice bob carol dave erin frank grace heidi)

for ((i = 1; i <= count; i++)); do
  from=${senders[RANDOM % ${#senders[@]}]}
  if ((RANDOM % 20 == 0)); then
    body='{"from":"'"$from"'","content":{}}' # missing body -> 422
  else
    body='{"from":"'"$from"'","content":{"header":"load","body":"message '"$i"'"}}'
  fi
  curl -s -o /dev/null -X POST "$url" -H 'content-type: application/json' -d "$body" &
  ((i % 20 == 0)) && wait
done
wait
echo "sent $count requests to $url"
