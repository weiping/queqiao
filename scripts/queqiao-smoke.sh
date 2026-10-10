#!/usr/bin/env bash
set -euo pipefail
# Smoke test for the queqiao binary: it serves, and its files go into the
# queqiao config/cache folders, never the magpie ones.
# Usage: bash build/queqiao-smoke.sh <path-to-binary>

bin="${1:?usage: queqiao-smoke.sh <binary>}"
T="$(mktemp -d)"
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  rm -rf "$T"
}
trap cleanup EXIT

HOME="$T" XDG_CONFIG_HOME="$T/config" XDG_CACHE_HOME="$T/cache" \
  MAGPIE_ADDR=127.0.0.1:3525 "$bin" serve &
pid=$!

up=0
for _ in $(seq 1 40); do
  if curl -fsS -H 'Authorization: Bearer magpie' \
      http://127.0.0.1:3525/v1/models >/dev/null 2>&1; then
    up=1
    break
  fi
  sleep 0.5
done
if [ "$up" != 1 ]; then
  echo "smoke: gateway did not answer /v1/models within 20s"
  exit 1
fi

kill "$pid" 2>/dev/null || true
wait "$pid" 2>/dev/null || true
pid=""

[ -d "$T/config/queqiao" ] || { echo "smoke: $T/config/queqiao is not a directory"; exit 1; }
[ ! -e "$T/config/magpie" ] || { echo "smoke: $T/config/magpie exists"; exit 1; }
[ ! -e "$T/cache/magpie" ]   || { echo "smoke: $T/cache/magpie exists"; exit 1; }
echo "smoke: ok"
