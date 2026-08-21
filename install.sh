#!/bin/sh
# Build and install the pinned Bench suite from this checkout.
set -eu

ME=install.sh
prefix=
allow_dirty=

usage() {
	cat <<'EOF'
usage: ./install.sh [-prefix DIR] [-allow-dirty]

Build the complete pinned Bench suite, fetching missing component sources into
.benchpack/sources, then install all six commands. The default prefix is
$HOME/.local. A clean checkout is required unless -allow-dirty is explicit.
EOF
}

while [ "$#" -gt 0 ]; do
	case $1 in
	-prefix|--prefix)
		[ "$#" -ge 2 ] || { echo "$ME: $1 needs a directory" >&2; exit 2; }
		prefix=$2
		shift 2
		;;
	-allow-dirty|--allow-dirty)
		allow_dirty=1
		shift
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "$ME: unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

for command in go git tar; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "$ME: $command is required to build from source" >&2
		exit 1
	}
done

src=$0
while [ -L "$src" ]; do
	dir=$(CDPATH= cd -P -- "$(dirname -- "$src")" && pwd)
	src=$(readlink "$src")
	case $src in /*) ;; *) src=$dir/$src ;; esac
done
here=$(CDPATH= cd -P -- "$(dirname -- "$src")" && pwd)
temp=$(mktemp -d "${TMPDIR:-/tmp}/bench-source-install.XXXXXX")
cleanup() { rm -rf -- "$temp"; }
trap cleanup EXIT HUP INT TERM

if [ -n "$allow_dirty" ]; then
	archive=$(cd "$here" && go run ./cmd/benchpack \
		-fetch -self "$here" -workspace "$here/.benchpack/sources" \
		-out "$temp/dist" -allow-dirty)
else
	archive=$(cd "$here" && go run ./cmd/benchpack \
		-fetch -self "$here" -workspace "$here/.benchpack/sources" \
		-out "$temp/dist")
fi

archive_dir=$(dirname -- "$archive")
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$archive_dir" && sha256sum -c "$(basename -- "$archive").sha256")
elif command -v shasum >/dev/null 2>&1; then
	(cd "$archive_dir" && shasum -a 256 -c "$(basename -- "$archive").sha256")
else
	echo "$ME: need sha256sum or shasum to verify the built archive" >&2
	exit 1
fi

tar -xzf "$archive" -C "$temp"
bundle=$(basename -- "$archive" .tar.gz)
if [ -n "$prefix" ]; then
	"$temp/$bundle/install.sh" "$prefix"
else
	"$temp/$bundle/install.sh"
fi
