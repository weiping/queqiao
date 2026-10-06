#!/bin/sh
# queqiao installer: curl -fsSL https://raw.githubusercontent.com/weiping/queqiao/queqiao/install.sh | sh
#
# Puts the terminal build of queqiao in ~/.local/bin ($PREFIX/bin on
# Termux), checked against the release's SHA-256. queqiao's gateway and
# magpie's both listen on 127.0.0.1:3425: quit magpie before
# `queqiao serve`; the two never run at once.
#
# Options (… | sh -s -- --version qq-v0.1.0):
#   --version <tag>    install this release instead of the latest
#                      (QUEQIAO_VERSION= does the same)
#   --bin-dir <dir>    install somewhere else (QUEQIAO_BIN_DIR= does the same)
set -eu

repo=weiping/queqiao
tag="${QUEQIAO_VERSION:-}"
bin="${QUEQIAO_BIN_DIR:-}"

say() { printf '  %s\n' "$*"; }
die() { printf 'queqiao: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value"; tag=$2; shift 2 ;;
    --version=*) tag=${1#--version=}; shift ;;
    --bin-dir) [ $# -ge 2 ] || die "--bin-dir needs a value"; bin=$2; shift 2 ;;
    --bin-dir=*) bin=${1#--bin-dir=}; shift ;;
    -h|--help)
      printf '%s\n' "usage: curl -fsSL https://raw.githubusercontent.com/$repo/queqiao/install.sh | sh -s -- [--version <tag>] [--bin-dir <dir>]"
      exit 0 ;;
    *) die "unknown option $1" ;;
  esac
done

# Platform: darwin/linux (amd64/arm64), or Termux's android/arm64.
if [ -n "${PREFIX:-}" ] && [ -x "$PREFIX/bin/pkg" ] && uname -o 2>/dev/null | grep -qi android; then
  os=android; arch=arm64
  bin="${bin:-$PREFIX/bin}"
else
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) die "no prebuilt queqiao for $(uname -s); build from source: make cli" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "no prebuilt queqiao for $(uname -m); build from source: make cli" ;;
  esac
  bin="${bin:-$HOME/.local/bin}"
fi
[ "$os" = android ] && [ "$arch" != arm64 ] && die "the Android build is arm64 only"

command -v curl >/dev/null || die "curl is needed"

# The release to install: latest, or the one --version named.
if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$tag" ] || die "could not learn the latest release of $repo (rate limited? pass --version qq-v<x.y.z>)"
fi
say "queqiao $tag ($os/$arch)"

ext=""; [ "$os" = windows ] && ext=.exe
asset="queqiao-cli-$os-$arch$ext"
base="https://github.com/$repo/releases/download/$tag"

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

cat <<EOF

  next:
    queqiao router init --preset cn    # routing groups + router.json (frontier/anthropic also exist)
    queqiao serve                      # the gateway on 127.0.0.1:3425
    queqiao router status              # config, mapping, recent decisions

  queqiao's gateway and magpie's both use 127.0.0.1:3425: quit magpie
  first — they never run at once. Plugins for Claude Code / Codex / Pi:
  https://github.com/$repo#快速开始
EOF
