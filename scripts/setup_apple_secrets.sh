#!/bin/sh
# setup_apple_secrets — walk the one-time Apple signing setup and set the
# GitHub secrets the release workflow needs (docs/signing.md in prose).
#
#   scripts/setup_apple_secrets.sh            # interactive
#   scripts/setup_apple_secrets.sh --dry-run  # check state, set nothing
#
# Two web steps remain yours (Apple requires them): issuing the Developer
# ID Application certificate, and creating the App Store Connect API key.
# The script tells you exactly when to do them and waits for a re-run.
set -eu

DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1
say()  { printf '  %s\n' "$*"; }
die()  { printf 'setup: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is needed ($2)"; }

need gh "GitHub CLI, logged into weiping/queqiao"
gh repo view weiping/queqiao >/dev/null 2>&1 || die "gh cannot see weiping/queqiao (gh auth login)"
need security "macOS Keychain"

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------- 1. the certificate
IDENT=$(security find-identity -v -p codesigning 2>/dev/null | awk '/Developer ID Application/ {print $0; exit}')
CERT_OK=0
if [ -n "$IDENT" ]; then
  say "found: ${IDENT%%(*}(${IDENT#*(}"
  CERT_OK=1
else
  say "no Developer ID Application certificate in the login keychain."
  say "generating a key pair and CSR for you:"
  KEY="$TMP/signing.key"; CSR="$TMP/certificate.csr"
  openssl genrsa -out "$KEY" 2048 2>/dev/null
  openssl req -new -key "$KEY" -out "$CSR" -subj "/CN=queqiao CI/O=queqiao/C=CN" 2>/dev/null
  [ -s "$CSR" ] || die "openssl could not make the CSR"
  cp "$CSR" "$HOME/Desktop/queqiao-signing.csr" 2>/dev/null || cp "$CSR" "$PWD/queqiao-signing.csr"
  say ""
  say "WEB STEP 1 of 2 — issue the certificate:"
  say "  1. developer.apple.com → Account → Certificates → ➕ (or /account/resources/certificates/add)"
  say "  2. choose Developer ID Application, continue"
  say "  3. upload $(ls "$HOME/Desktop/queqiao-signing.csr" 2>/dev/null || echo queqiao-signing.csr)"
  say "  4. download the .cer, double-click it (imports to Keychain Access)"
  say ""
  say "then re-run this script — it will pick the certificate up."
  [ "$DRY" = 1 ] || exit 0
  exit 0
fi

# the team id rides in the identity's parentheses: ... (ABCDEF1234)
TEAM=$(printf '%s' "$IDENT" | sed -n 's/.*(\([A-Z0-9]\{10\}\)).*/\1/p')
[ -n "$TEAM" ] || die "could not read the Team ID from the identity: $IDENT"

# ---------------------------------------------------------------- 2. the API key
P8="${APPLE_API_KEY_FILE:-}"
if [ -z "$P8" ]; then
  for f in "$HOME/Desktop"/AuthKey_*.p8 "$HOME/Downloads"/AuthKey_*.p8 "$PWD"/AuthKey_*.p8; do
    [ -f "$f" ] && P8="$f" && break
  done
fi
if [ -z "$P8" ]; then
  say ""
  say "WEB STEP 2 of 2 — the notarisation key:"
  say "  1. appstoreconnect.apple.com → Users and Access → Integrations → App Store Connect API"
  say "  2. Generate a key, access Developer, download the .p8 (once only!)"
  say "  3. note the Key ID and the Issuer ID on that page"
  say ""
  say "then re-run with:"
  say "  APPLE_API_KEY_FILE=/path/to/AuthKey_XXXX.p8 scripts/setup_apple_secrets.sh"
  [ "$DRY" = 1 ] || exit 0
  exit 0
fi
say "API key: $P8"

printf '%s\n' "" "  everything is here. next prompts: the .p12 export password," "  the Key ID and the Issuer ID (from the App Store Connect page)."

# ---------------------------------------------------------------- 3. export and set
if [ "$DRY" = 1 ]; then
  say "dry run: would export the .p12, base64 it and set 6 secrets (team $TEAM)."
  exit 0
fi

P12="$TMP/queqiao-signing.p12"
NAME=$(printf '%s' "$IDENT" | sed 's/^ *[0-9]* //; s/ *([^)]*) *$//')
printf 'p12 export password: '; stty -echo; read -r P12PASS; stty echo; printf '\n'
security export -k login.keychain-db -P "$P12PASS" -t identities -f pkcs12 -o "$P12" 2>/dev/null \
  || security export -k login.keychain-db -P "$P12PASS" -t identities -f pkcs12 -o "$P12"
[ -s "$P12" ] || die "could not export the identity as .p12 (Keychain Access → export works too)"
printf 'Key ID: '; read -r KEYID
printf 'Issuer ID: '; read -r ISSUER

set_secret() { # set_secret <name> <file|literal> [file]
  if [ "$3" = file ]; then base64 -i "$2" | gh secret set "$1" -R weiping/queqiao; else printf '%s' "$2" | gh secret set "$1" -R weiping/queqiao; fi
}
set_secret APPLE_CERT_P12 "$P12" file
printf '%s' "$P12PASS" | gh secret set APPLE_CERT_PASSWORD -R weiping/queqiao
set_secret APPLE_API_KEY "$P8" file
printf '%s' "$KEYID"  | gh secret set APPLE_API_KEY_ID -R weiping/queqiao
printf '%s' "$ISSUER" | gh secret set APPLE_API_ISSUER -R weiping/queqiao
printf '%s' "$TEAM"   | gh secret set APPLE_TEAM_ID -R weiping/queqiao

say ""
gh secret list -R weiping/queqiao
say "all set. next qq-v* tag builds a signed, notarised, stapled app."
