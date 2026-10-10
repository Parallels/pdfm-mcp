#!/usr/bin/env bash
# Runs shell and Python release/packaging tests. No network.
set -uo pipefail
cd "$(dirname "$0")"
rc=0
for t in test_*.sh; do
  echo "== $t"
  bash "$t" || rc=1
done
python3 -B -m unittest discover -s . -p 'test_*.py' || rc=1
[[ $rc -eq 0 ]] && echo "ALL SCRIPT TESTS PASSED" || echo "SCRIPT TESTS FAILED"
exit $rc
