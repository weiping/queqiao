#!/bin/sh
# Tests install.sh against a fake release (file:// URLs) and fake commands.
#   sh scripts/install_test.sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
fail=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

# a release whose binary is a script that logs how it was called
setup() {
  t=$(mktemp -d)
  mkdir -p "$t/rel" "$t/home" "$t/fakebin"
  case "$(uname -s)" in Darwin) os=darwin ;; *) os=linux ;; esac
  case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; *) arch=amd64 ;; esac
  asset="queqiao-$os-$arch"
  printf '#!/bin/sh\necho "$@" >> "%s/calls"\n' "$t" > "$t/rel/$asset"
  (cd "$t/rel" && { shasum -a 256 "$asset" 2>/dev/null || sha256sum "$asset"; } > checksums.txt)
  printf '#!/bin/sh\necho magpie v0.1.1000\n' > "$t/fakebin/magpie"
  chmod +x "$t/fakebin/magpie"
}
run() { # run install.sh with PATH=$1
  HOME="$t/home" PATH="$1" QUEQIAO_VERSION=qq-v0.2.0 QUEQIAO_DOWNLOAD_BASE="file://$t/rel" \
    QUEQIAO_BIN_DIR="$t/home/.local/bin" sh "$root/install.sh" > "$t/out" 2>&1
}
sys=/usr/bin:/bin

setup
if run "$sys"; then bad "no magpie: install went ahead"; else
  grep -q 'https://github.com/yetone/magpie' "$t/out" && ok "no magpie: refuses and names magpie" || bad "no magpie: message lacks magpie's address"
fi
[ -e "$t/home/.local/bin/queqiao" ] && bad "no magpie: binary was installed" || ok "no magpie: nothing installed"
rm -rf "$t"

setup
if run "$t/fakebin:$sys"; then
  [ -x "$t/home/.local/bin/queqiao" ] && ok "with magpie: installed" || bad "with magpie: no binary"
  grep -qx 'service install' "$t/calls" 2>/dev/null && ok "with magpie: service installed" || bad "with magpie: service not installed"
  grep -q 'migrate' "$t/out" && bad "fresh install mentions migrate" || ok "fresh install: no migrate hint"
else bad "with magpie: failed: $(cat "$t/out")"; fi
rm -rf "$t"

setup
mkdir -p "$t/home/.config/queqiao"; echo '{}' > "$t/home/.config/queqiao/providers.json"
if run "$t/fakebin:$sys"; then
  grep -q 'queqiao migrate --dry-run' "$t/out" && ok "qq-v0.1.x data: migrate hint" || bad "qq-v0.1.x data: no migrate hint"
else bad "qq-v0.1.x data: failed: $(cat "$t/out")"; fi
rm -rf "$t"

setup
echo "0000000000000000000000000000000000000000000000000000000000000000  $asset" > "$t/rel/checksums.txt"
if run "$t/fakebin:$sys"; then bad "bad checksum: accepted"; else
  grep -q 'SHA-256 mismatch' "$t/out" && ok "bad checksum: refused" || bad "bad checksum: other error: $(cat "$t/out")"
fi
rm -rf "$t"

exit $fail
