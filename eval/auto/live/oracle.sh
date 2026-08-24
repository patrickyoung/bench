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

run_l02_behavior() {
  action_shell=${PLY_ACTION_SHELL-}
  toolbox=$script_dir/toolbox
  test -n "$action_shell" && test -x "$action_shell" && test -d "$toolbox" || return 2
  parent=$(/usr/bin/dirname "$root") || return 2
  probe_base=$(/usr/bin/mktemp -d "$parent/.bench-l02-probe.XXXXXX") || return 2
  probe=$probe_base/workspace
  /bin/mkdir "$probe" || {
    /bin/rm -rf "$probe_base"
    return 2
  }
  before=$(/usr/bin/mktemp -d "$parent/.bench-l02-before.XXXXXX") || {
    /bin/rm -rf "$probe_base"
    return 2
  }
  cleanup_l02_probe() {
    /bin/rm -rf "$probe_base" "$before"
  }
  trap cleanup_l02_probe EXIT HUP INT TERM
  /bin/cp -R "$root/." "$probe" && /bin/cp -R "$root/." "$before" || return 2
  driver=$(/bin/cat <<'EOF'
set -u
program=./lib/classify.sh
if ! test -f "$program" || ! test -x "$program"; then
  printf '%s\n' 'l02: lib/classify.sh must exist and remain executable'
  exit 1
fi
tmp=${TMPDIR:-/tmp}/bench-l02-check-$$
actual=$tmp.actual
actual_stderr=$tmp.stderr
expected=$tmp.expected
trap '/bin/rm -f "$actual" "$actual_stderr" "$expected"' EXIT HUP INT TERM
failed=0
check_case() {
  label=$1
  want_status=$2
  want_stdout=$3
  shift 3
  printf '%s\n' "$want_stdout" > "$expected"
  set +e
  "$program" "$@" > "$actual" 2> "$actual_stderr"
  got_status=$?
  set -e
  if test "$got_status" -ne "$want_status" || ! /usr/bin/cmp -s "$expected" "$actual" || test -s "$actual_stderr"; then
    printf 'l02 %s: expected status %s, got %s\n' "$label" "$want_status" "$got_status"
    printf '%s\n' '  expected stdout bytes:'
    /usr/bin/od -An -tx1 "$expected"
    printf '%s\n' '  actual stdout bytes:'
    /usr/bin/od -An -tx1 "$actual"
    printf '%s\n' '  actual stderr bytes:'
    /usr/bin/od -An -tx1 "$actual_stderr"
    failed=1
  fi
}
set -e
check_case zero 0 zero 0
check_case positive-one 0 positive 1
check_case positive-many 0 positive 42
check_case negative-one 0 negative -1
check_case negative-many 0 negative -42
check_case missing 2 'usage: classify.sh INTEGER'
check_case extra-argument 2 'usage: classify.sh INTEGER' 1 2
check_case empty-string 2 'usage: classify.sh INTEGER' ''
check_case leading-zero 2 'usage: classify.sh INTEGER' 00
check_case leading-zero-positive 2 'usage: classify.sh INTEGER' 01
check_case negative-zero 2 'usage: classify.sh INTEGER' -0
check_case leading-zero-negative 2 'usage: classify.sh INTEGER' -01
check_case leading-plus 2 'usage: classify.sh INTEGER' +1
check_case leading-plus-zero 2 'usage: classify.sh INTEGER' +0
check_case decimal 2 'usage: classify.sh INTEGER' 1.5
check_case whitespace 2 'usage: classify.sh INTEGER' ' '
check_case non-integer 2 'usage: classify.sh INTEGER' word
exit "$failed"
EOF
  ) || return 2
  (
    cd "$probe" || exit 2
    PATH="$toolbox" "$action_shell" -c "$driver"
  )
  behavior=$?
  if test "$behavior" -eq 124; then
    printf '%s\n' 'l02 behavioral probe timed out'
    behavior=1
  fi
  if ! /usr/bin/diff -r -- "$before" "$probe" >/dev/null || ! test -x "$probe/lib/classify.sh"; then
    printf '%s\n' 'l02 verifier probe mutated the candidate workspace'
    if test "$behavior" -le 1; then
      behavior=1
    fi
  fi
  if ! cleanup_l02_probe; then
    printf '%s\n' 'l02 verifier could not remove its disposable probe'
    return 2
  fi
  trap - EXIT HUP INT TERM
  return "$behavior"
}

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
    paths=$(/usr/bin/find "$root" -mindepth 1 -print | LC_ALL=C /usr/bin/sort) || exit 2
    want_paths=$(printf '%s\n%s' "$root/lib" "$root/lib/classify.sh")
    if test "$paths" = "$want_paths" && test -f "$ledger" && test ! -s "$ledger" &&
       test -f "$root/lib/classify.sh" && test ! -L "$root/lib/classify.sh" &&
       test -x "$root/lib/classify.sh"; then
      run_l02_behavior
      behavior=$?
      case "$behavior" in
        0) pass=true ;;
        1) ;;
        *) exit 2 ;;
      esac
    fi
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
