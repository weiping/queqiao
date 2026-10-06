#!/usr/bin/env python3
"""Re-apply the queqiao brand to the CLI's user-facing strings.

Run this after merging upstream into queqiao: upstream's new help lines
and messages arrive spelled magpie; this makes them queqiao again. Safe
to run repeatedly (idempotent). Only string literals are touched —
comments and identifiers keep their spelling. Semantic magpie is
protected: the magpie:// scheme, /v1/magpie/* paths, the magpie/
provider prefix in model ids, magpie-models.json, model_providers.magpie,
remote-magpie, sk-magpie keys, usemagpie.ai, Magpie.app,
magpie-community and the internal magpie.* storage keys.

After running: go test -tags nogui . — the CLI tests assert the branded
spellings; a test failing there points at a string this script missed.
"""

import re
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent

# the CLI files the brand covers (usage consts, prompts, version, stderr)
FILES = [
    "main.go", "model_cli.go", "groups_cli.go", "groups_rules_cli.go",
    "library_cli.go", "plugins_cli.go", "providers_cli.go", "quota_cli.go",
    "search_cli.go", "visible_cli.go", "hook_cli.go",
]

PROTECTED = [
    "usemagpie.ai", "magpie-community", "magpie://", "/v1/magpie/",
    "magpie-models", "model_providers.magpie", "remote-magpie", "sk-magpie",
    "Magpie.app", "magpie.app", "magpie-releases", "/magpie", "magpie/",
    "MAGPIE_", "magpie.",
]

SUBCOMMANDS = (
    "model|models|group|groups|provider|providers|plugin|plugins|quota|quotas|"
    "search|library|visible|usage|hook|router|backup|restore|web|tui|tray|panel|"
    "autostart|accounts?|gateway-key|presets|import|claude|codex|pi|save|use|"
    "profiles|rm|ls|version|help|dev|healthcheck|serve|webdav|s3|omp|update"
)

STR_DQ = re.compile(r'"((?:[^"\\\n]|\\.)*)"')
STR_BT = re.compile(r"`([^`]*)`")


def brand_literal(s: str) -> str:
    for i, a in enumerate(PROTECTED):
        s = s.replace(a, f"\x00P{i}\x00")
    s = re.sub(rf"\bmagpie(?= (?:{SUBCOMMANDS})\b)", "queqiao", s)
    s = re.sub(r"\bmagpie\b", "queqiao", s)
    for i, a in enumerate(PROTECTED):
        s = s.replace(f"\x00P{i}\x00", a)
    return s


def brand(src: str) -> str:
    src = STR_DQ.sub(lambda m: '"' + brand_literal(m.group(1)) + '"', src)
    src = STR_BT.sub(lambda m: "`" + brand_literal(m.group(1)) + "`", src)
    return src


def main() -> int:
    changed = 0
    for name in FILES:
        p = ROOT / name
        if not p.exists():
            continue
        src = p.read_text()
        out = brand(src)
        if out != src:
            p.write_text(out)
            print(f"branded: {name}")
            changed += 1
    print(f"{changed} file(s) changed; run: go test -tags nogui .")
    return 0


if __name__ == "__main__":
    sys.exit(main())
