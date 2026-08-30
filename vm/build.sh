#!/bin/sh
set -eu

umask 022

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
worker_bin="$script_dir/staging/usr/libexec/bench-worker"

fail() {
	printf 'build: %s\n' "$*" >&2
	exit 1
}

[ "$(uname -s)" = Linux ] || fail "Linux is required to build and run this image"

for tool in go file mkosi; do
	command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
done

host_arch=$(go env GOHOSTARCH)
case "$host_arch" in
	amd64|arm64) ;;
	*) fail "unsupported native architecture: $host_arch (expected amd64 or arm64)" ;;
esac

mkdir -p "$script_dir/staging/usr/libexec"
(
	cd "$repo_dir"
	CGO_ENABLED=0 GOOS=linux GOARCH="$host_arch" \
		go build -trimpath -ldflags='-s -w -buildid=' \
		-o "$worker_bin" ./cmd/bench-worker
)

[ -x "$worker_bin" ] || fail "staged bench-worker is not executable"

build_info=$(go version -m "$worker_bin") || fail "cannot read Go build metadata"
printf '%s\n' "$build_info" | grep -Fq 'GOOS=linux' || fail "staged worker is not a Linux binary"
printf '%s\n' "$build_info" | grep -Fq "GOARCH=$host_arch" || fail "staged worker is not native $host_arch"
printf '%s\n' "$build_info" | grep -Fq 'CGO_ENABLED=0' || fail "staged worker has cgo enabled"

file_info=$(file -b "$worker_bin")
case "$file_info" in
	*ELF*statically\ linked*) ;;
	*) fail "staged worker is not a static ELF binary: $file_info" ;;
esac

printf 'build: staged %s (%s)\n' "$worker_bin" "$file_info"
cd "$script_dir"
exec mkosi build
