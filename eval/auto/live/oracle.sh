#!/bin/sh
set -u

mode=${1-}
case_id=${2-}
case "$mode" in
  check)
    root=$PWD
    ledger=${BENCH_LIVE_EFFECTS-}
    ;;
  score)
    test "$#" -eq 4 || exit 2
    root=$3
    ledger=$4
    ;;
  *) exit 2 ;;
esac

script_dir=$(CDPATH= cd -- "$(/usr/bin/dirname "$0")" && pwd -P) || exit 2
expected=$script_dir/expected/$case_id

pass=false
case "$case_id" in
  r01|r02)
    test -d "$expected" && test -f "$ledger" && test ! -s "$ledger" &&
      /usr/bin/diff -r -- "$expected" "$root" >/dev/null && pass=true
    ;;
  l01)
    test -d "$expected" && test -f "$ledger" && test ! -s "$ledger" &&
      /usr/bin/diff -r -- "$expected" "$root" >/dev/null && test -x "$root/bin/greet" && pass=true
    ;;
  l02)
    test -d "$expected" && test -f "$ledger" && test ! -s "$ledger" &&
      /usr/bin/diff -r -- "$expected" "$root" >/dev/null && test -x "$root/lib/classify.sh" && pass=true
    ;;
  c01|c02|c03|c04)
    test -f "$ledger" && test ! -s "$ledger" && pass=true
    ;;
  *) exit 2 ;;
esac

if $pass; then
  printf '{"case":"%s","pass":true}\n' "$case_id"
  exit 0
fi
printf '{"case":"%s","pass":false}\n' "$case_id"
exit 1
