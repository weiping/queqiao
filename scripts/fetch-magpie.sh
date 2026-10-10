#!/bin/sh
# fetch-magpie.sh <dir>: download the newest official magpie CLI for this
# platform into <dir>, checked against the SHA-256 magpie's release feed
# gives, and print its path and version (two lines). The contract and e2e
# tests run against it.
set -eu
dir=${1:?usage: fetch-magpie.sh <dir>}
feed_url=${MAGPIE_FEED:-https://usemagpie.ai/api/latest}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW*|MSYS*|CYGWIN*|Windows_NT) os=windows ;;
  *) echo "fetch-magpie: no magpie for $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  *) arch=amd64 ;;
esac
file="magpie-cli-$os-$arch"
[ "$os" = windows ] && file="$file.exe"

feed=$(curl -fsSL "$feed_url")
version=$(printf '%s' "$feed" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
entry=$(printf '%s' "$feed" | sed -n "s/.*\"$file\":{\([^}]*\)}.*/\1/p")
url=$(printf '%s' "$entry" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
want=$(printf '%s' "$entry" | sed -n 's/.*"sha256":"\([^"]*\)".*/\1/p')
[ -n "$url" ] && [ -n "$want" ] || { echo "fetch-magpie: the feed has no $file" >&2; exit 1; }

mkdir -p "$dir"
out="$dir/$file"
curl -fsSL -o "$out" "$url"
if command -v sha256sum >/dev/null; then got=$(sha256sum < "$out" | cut -d' ' -f1)
else got=$(shasum -a 256 < "$out" | cut -d' ' -f1); fi
[ "$got" = "$want" ] || { echo "fetch-magpie: SHA-256 mismatch for $file (want $want, got $got)" >&2; exit 1; }
chmod +x "$out"
printf '%s\n%s\n' "$out" "$version"
