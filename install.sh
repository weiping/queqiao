#!/bin/sh
# queqiao installer: curl -fsSL https://raw.githubusercontent.com/weiping/queqiao/main/install.sh | sh
#
# queqiao runs beside official magpie (https://github.com/yetone/magpie):
# install magpie first. This puts queqiao in ~/.local/bin, checked against
# the release's SHA-256, and runs queqiaod at login (queqiao service
# install). queqiaod listens on 127.0.0.1:3426; magpie keeps 3425.
#
# Options (… | sh -s -- --version qq-v0.2.0):
#   --version <tag>    install this release instead of the latest
#                      (QUEQIAO_VERSION= does the same)
#   --bin-dir <dir>    install somewhere else (QUEQIAO_BIN_DIR= does the same)
#   --no-service       don't run queqiaod at login
set -eu

repo=weiping/queqiao
tag="${QUEQIAO_VERSION:-}"
bin="${QUEQIAO_BIN_DIR:-}"
service=1

say() { printf '  %s\n' "$*"; }
die() { printf 'queqiao: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value"; tag=$2; shift 2 ;;
    --version=*) tag=${1#--version=}; shift ;;
    --bin-dir) [ $# -ge 2 ] || die "--bin-dir needs a value"; bin=$2; shift 2 ;;
    --bin-dir=*) bin=${1#--bin-dir=}; shift ;;
    --no-service) service=0; shift ;;
    -h|--help)
      printf '%s\n' "usage: curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | sh -s -- [--version <tag>] [--bin-dir <dir>] [--no-service]"
      exit 0 ;;
    *) die "unknown option $1" ;;
  esac
done

command -v magpie >/dev/null 2>&1 || die "queqiao needs official magpie: install it first from https://github.com/yetone/magpie, then run this again"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "no prebuilt queqiao for $(uname -s); build from source: make build" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "no prebuilt queqiao for $(uname -m); build from source: make build" ;;
esac
bin="${bin:-$HOME/.local/bin}"
command -v curl >/dev/null || die "curl is needed"

if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$tag" ] || die "could not learn the latest release of $repo (rate limited? pass --version qq-v<x.y.z>)"
fi
say "queqiao $tag ($os/$arch)"

asset="queqiao-$os-$arch"
base="${QUEQIAO_DOWNLOAD_BASE:-https://github.com/$repo/releases/download/$tag}"

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "no $asset in release $tag"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "no checksums.txt in release $tag"

# The download is only trusted when its SHA-256 is the release's.
want=$(sed -n "s/^\\([0-9a-f]*\\)  $asset\$/\\1/p" "$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt in $tag has no line for $asset"
if command -v shasum >/dev/null; then
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
elif command -v sha256sum >/dev/null; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  die "shasum or sha256sum is needed to check the download"
fi
[ "$want" = "$got" ] || die "SHA-256 mismatch for $asset (want $want, got $got): the download is refused"

mkdir -p "$bin"
mv "$tmp/$asset" "$bin/queqiao"
chmod +x "$bin/queqiao"
say "installed to $bin/queqiao"

case ":$PATH:" in
  *":$bin:"*) ;;
  *) say "note: $bin is not on PATH — add it, e.g. export PATH=\"$bin:\$PATH\"" ;;
esac

if [ "$service" = 1 ]; then
  "$bin/queqiao" service install || say "queqiao service install failed; run queqiaod yourself: queqiao serve"
fi

# qq-v0.1.x kept magpie's data in queqiao's folder
cfg="${XDG_CONFIG_HOME:-$HOME/.config}"
if [ -f "$cfg/queqiao/providers.json" ]; then
  cat <<EOF

  qq-v0.1.x data found in $cfg/queqiao: hand it to official magpie with
    queqiao migrate --dry-run     # see what moves
    queqiao migrate               # do it (queqiao migrate restore undoes it)
EOF
fi

cat <<EOF

  next:
    queqiao router init --preset cn    # tier groups in magpie + router.json (frontier/anthropic also exist)
    queqiao status                     # magpie, queqiaod, groups, recent decisions
    codex -p queqiao                   # Codex through queqiaod; Claude Code and Pi: their plugins
EOF
