#!/usr/bin/env bash
# Create the SP0 spike tier groups on the upstream magpie (spec §4.4).
#
# Usage:
#   bash setup-groups.sh <fast-models> <balanced-models> <perf-models>
#
# Each argument is a comma-separated provider/model list as magpie's
# `models` lists them, e.g.
#   bash setup-groups.sh provider/a,provider/b provider/c provider/d
set -euo pipefail

fast=${1:?usage: setup-groups.sh <fast> <balanced> <perf>}
balanced=${2:?missing balanced models}
perf=${3:?missing perf models}

magpie group add qq-fast     "models=$fast"     routing=order stays=auto
magpie group add qq-balanced "models=$balanced" routing=order stays=auto
magpie group add qq-perf     "models=$perf"     routing=order stays=auto
magpie group add queqiao     models=group/qq-balanced,group/qq-perf,group/qq-fast routing=order stays=turn
