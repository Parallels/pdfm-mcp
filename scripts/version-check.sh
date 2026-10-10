#!/usr/bin/env bash
# Release guard (specs 013/023): strict tag matching VERSION, clean tree,
# tagged HEAD on main, increasing stable version. Full-depth checkout required.
set -euo pipefail
[[ $# -eq 1 ]] || { printf 'version-check: usage: version-check.sh <tag>\n' >&2; exit 1; }
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
exec python3 -B "$script_dir/version.py" guard --tag "$1"
