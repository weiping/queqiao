#!/bin/sh
# migrate-from-magpie — move a magpie installation over to queqiao.
#
#   migrate-from-magpie.sh                 backup, migrate the config, uninstall magpie
#   migrate-from-magpie.sh --migrate-only  migrate the config, keep magpie installed
#   migrate-from-magpie.sh --dry-run       print what would happen, change nothing
#   migrate-from-magpie.sh restore         roll back to before the last migration
#   migrate-from-magpie.sh --yes           no confirmation prompt
#
# Nothing is deleted: the config is copied into the backup, and everything
# uninstalled (binary, app, caches) is MOVED into the backup's removed/
# folder, from where `restore` puts it back. The backup lives in
# ~/.config/queqiao-migration/backup and stays until you delete it.
#
# queqiao must be installed first (install.sh or make cli).
set -eu

say()  { printf '  %s\n' "$*"; }
die()  { printf 'migrate: %s\n' "$*" >&2; exit 1; }

MODE=full; YES=0; DRY=0
for a in "$@"; do
  case "$a" in
    --migrate-only) MODE=migrate ;;
    --dry-run) DRY=1 ;;
    --yes) YES=1 ;;
    restore) MODE=restore ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option $a (try --help)" ;;
  esac
done

CFG="${XDG_CONFIG_HOME:-$HOME/.config}"
MAGPIE_CFG="$CFG/magpie"
QUEQIAO_CFG="$CFG/queqiao"
STATE="$CFG/queqiao-migration"
BACKUP="$STATE/backup"

# what gets moved out when magpie is uninstalled (recorded, so restore
# can put each thing back where it came from)
record() { printf '%s\t%s\n' "$1" "$2" >> "$BACKUP/paths.txt"; }

run() { # run a step unless --dry-run
  if [ "$DRY" = 1 ]; then say "DRY: $*"; else "$@"; fi
}
mv_out() { # mv_out <source> <backup-subdir>  — move a thing into the backup
  src=$1; sub=$2
  [ -e "$src" ] || return 0
  say "move away: $src"
  if [ "$DRY" != 1 ]; then
    mkdir -p "$BACKUP/removed/$sub"
    mv "$src" "$BACKUP/removed/$sub/$(basename "$src")"
    record "$src" "$sub/$(basename "$src")"
  fi
}

# ---------------------------------------------------------------- restore
if [ "$MODE" = restore ]; then
  [ -f "$BACKUP/paths.txt" ] || die "no migration backup in $BACKUP (nothing to restore)"
  say "restoring from $BACKUP"
  # 1) queqiao's config back to what it was before the migration; what the
  #    migration wrote stays beside the backup, nothing is lost
  if [ -d "$BACKUP/queqiao-config-pre" ]; then
    [ -d "$QUEQIAO_CFG" ] && { mkdir -p "$BACKUP/queqiao-config-post"; mv "$QUEQIAO_CFG" "$BACKUP/queqiao-config-post/$(date +%Y%m%d%H%M%S)"; }
    run mv "$BACKUP/queqiao-config-pre" "$QUEQIAO_CFG"
    say "queqiao config back to its pre-migration state"
  fi
  # 2) magpie's config: the loop below brings the original back from
  #    removed/. This copy from the backup is only for a restore whose
  #    uninstalled things are already gone (restore after restore), so it
  #    must not run when the loop will handle it — that would nest the two
  #    copies into one another.
  if [ -d "$BACKUP/magpie-config" ] && [ ! -d "$BACKUP/removed/config/magpie" ]; then
    if [ "$DRY" = 1 ]; then say "DRY: cp -a $BACKUP/magpie-config $MAGPIE_CFG"; else rm -rf "$MAGPIE_CFG"; cp -a "$BACKUP/magpie-config" "$MAGPIE_CFG"; fi
    say "magpie config restored to $MAGPIE_CFG"
  fi
  # 3) everything that was uninstalled, back where it was. -L besides -e:
  #    a moved symlink (magpie's installer links ~/.local/bin/magpie into
  #    the app bundle) dangles until its target is back, and -e alone
  #    would skip it
  while IFS="$(printf '\t')" read -r src sub; do
    { [ -e "$BACKUP/removed/$sub" ] || [ -L "$BACKUP/removed/$sub" ]; } || continue
    dir=$(dirname "$src")
    # whatever sits at the destination now (an earlier restore's copy, a
    # config the loop is about to replace) moves aside into the backup,
    # never deleted
    if [ "$DRY" = 1 ]; then
      say "DRY: mv $BACKUP/removed/$sub $src"
    else
      if [ -e "$src" ] || [ -L "$src" ]; then
        mkdir -p "$BACKUP/replaced"
        mv "$src" "$BACKUP/replaced/$(printf '%s' "$src" | tr '/%' '__')"
      fi
      mkdir -p "$dir"
      mv "$BACKUP/removed/$sub" "$src"
    fi
    say "back: $src"
  done < "$BACKUP/paths.txt"
  [ "$DRY" = 1 ] || rm -f "$BACKUP/paths.txt" "$STATE/migrated" "$STATE/uninstalled"
  say "restore done — magpie is as it was; the backup stays in $BACKUP"
  exit 0
fi

# ---------------------------------------------------------------- checks
[ -d "$MAGPIE_CFG" ] || die "no magpie config at $MAGPIE_CFG; nothing to migrate"
command -v queqiao >/dev/null 2>&1 || die "queqiao is not on PATH; install it first (README → 快速开始)"
[ -e "$BACKUP/paths.txt" ] && die "a previous migration's backup is still in $BACKUP; run '$0 restore' first, or delete the backup if you no longer need it"

if [ "$YES" != 1 ] && [ "$DRY" != 1 ]; then
  printf 'this migrates %s into queqiao and uninstalls magpie (recoverable: %s restore). continue? [y/N] ' "$MAGPIE_CFG" "$0"
  read -r ans
  case "$ans" in y|Y|yes|YES) ;; *) die "aborted" ;; esac
fi

# ---------------------------------------------------------------- migrate
say "1/3  backing up and migrating the config"
mkdir -p "$STATE"
if [ "$DRY" = 1 ]; then
  say "DRY: cp -a $MAGPIE_CFG $BACKUP/magpie-config"
  [ -d "$QUEQIAO_CFG" ] && say "DRY: mv $QUEQIAO_CFG $BACKUP/queqiao-config-pre"
  say "DRY: cp -a $MAGPIE_CFG $QUEQIAO_CFG"
else
  mkdir -p "$BACKUP"
  rm -rf "$BACKUP/magpie-config"
  cp -a "$MAGPIE_CFG" "$BACKUP/magpie-config"
  say "backup: $BACKUP/magpie-config"
  if [ -d "$QUEQIAO_CFG" ]; then
    rm -rf "$BACKUP/queqiao-config-pre"
    mv "$QUEQIAO_CFG" "$BACKUP/queqiao-config-pre"
    say "existing queqiao config set aside: $BACKUP/queqiao-config-pre"
  fi
  cp -a "$MAGPIE_CFG" "$QUEQIAO_CFG"
  say "migrated: $MAGPIE_CFG → $QUEQIAO_CFG"
fi
if [ "$DRY" != 1 ]; then : > "$BACKUP/paths.txt"; touch "$STATE/migrated"; fi

# a queqiao that was already routed: its routing groups lived in the old
# providers.json, which the copy just replaced — they have to be made again
if [ -f "$BACKUP/queqiao-config-pre/router.json" ]; then
  say "note: router.json was set aside; after migrating, run: queqiao router init --preset <cn|frontier|anthropic> --groups-only --force"
fi

if [ "$MODE" = migrate ]; then
  say "magpie left installed (--migrate-only). Both gateways use 127.0.0.1:3425: stop magpie before 'queqiao serve'."
  exit 0
fi

# ---------------------------------------------------------------- uninstall
say "2/3  stopping magpie"
# graceful first, then whatever is left
if command -v magpie >/dev/null 2>&1; then
  run magpie autostart off || true
fi
if [ "$DRY" != 1 ]; then
  command -v osascript >/dev/null 2>&1 && osascript -e 'tell application id "com.yetone.magpie" to quit' 2>/dev/null || true
  sleep 1
  pkill -x magpie 2>/dev/null || true
  pkill -f '/Magpie.app/|/magpie.app/' 2>/dev/null || true
  pkill -f "$HOME/.cache/magpie/" 2>/dev/null || true   # the plugin host's bun
  sleep 1
fi

say "3/3  moving magpie aside (nothing deleted)"
mv_out "$HOME/.local/bin/magpie"        local-bin
for app in /Applications/Magpie.app /Applications/magpie.app "$HOME/Applications/Magpie.app" "$HOME/Applications/magpie.app"; do
  mv_out "$app"                          applications
done
mv_out "$MAGPIE_CFG"                     config
mv_out "$HOME/.cache/magpie"             cache
mv_out "$HOME/Library/Caches/magpie"     system-cache
for plist in "$HOME/Library/LaunchAgents/"*magpie*.plist; do
  [ -e "$plist" ] && mv_out "$plist"     launch-agents
done
[ "$DRY" = 1 ] || touch "$STATE/uninstalled"

say ""
say "done. next:"
say "  queqiao router init --preset cn   # routing groups + router.json (frontier/anthropic also exist)"
say "  queqiao serve                    # the gateway on 127.0.0.1:3425"
if command -v magpie >/dev/null 2>&1 && [ "$DRY" != 1 ]; then
  hash -r 2>/dev/null || true   # this shell may still hold the path from 'autostart off'
  command -v magpie >/dev/null 2>&1 && say "note: a 'magpie' is still on PATH ($(command -v magpie)) — remove it by hand if that is another install"
fi
say "roll back any time with: $0 restore  (backup: $BACKUP)"
