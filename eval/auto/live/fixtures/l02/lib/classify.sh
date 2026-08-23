#!/bin/sh
case "${1-}" in
  0) printf 'positive\n' ;;
  *) printf 'unknown\n' ;;
esac
