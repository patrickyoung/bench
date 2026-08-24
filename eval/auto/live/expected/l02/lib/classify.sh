#!/bin/sh
if test "$#" -ne 1; then
  printf 'usage: classify.sh INTEGER\n'
  exit 2
fi
case "${1-}" in
  0) printf 'zero\n' ;;
  -[1-9]|-[1-9][0-9]*) printf 'negative\n' ;;
  [1-9]|[1-9][0-9]*) printf 'positive\n' ;;
  *) printf 'usage: classify.sh INTEGER\n'; exit 2 ;;
esac
