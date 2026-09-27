#!/bin/sh
set -eu
base=${BASE_URL:-http://localhost:8080}
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
curl -fsS -c "$jar" -H 'Content-Type: application/json' -d '{"login":"demo","password":"demo"}' "$base/api/login"
for path in /appointments /appointments/1 /booking /booking?service_id=1 /dashboard '/api/appointments?page=1&size=20' /api/appointments/1 /api/services /api/specialists '/api/slots?service_id=1&date_from=2026-10-01&date_to=2026-10-08' '/api/dashboard?date_from=2026-10-01&date_to=2026-10-08'; do
  curl -fsS -b "$jar" -o /dev/null "$base$path"
  printf 'OK %s\n' "$path"
done
curl -fsS -b "$jar" -X POST "$base/api/logout"
