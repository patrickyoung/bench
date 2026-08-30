#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
config="$script_dir/mkosi.conf"
unit="$script_dir/mkosi.extra/usr/lib/systemd/system/bench-worker@.service"
sysusers="$script_dir/mkosi.extra/usr/lib/sysusers.d/bench-worker.conf"
tmpfiles="$script_dir/mkosi.extra/usr/lib/tmpfiles.d/bench-worker.conf"

fail() {
	printf 'validate: %s\n' "$*" >&2
	exit 1
}

require_line() {
	file=$1
	expected=$2
	grep -Fqx "$expected" "$file" || fail "$file is missing: $expected"
}

for script in "$script_dir/build.sh" "$script_dir/validate.sh"; do
	sh -n "$script" || fail "shell syntax check failed: $script"
done
printf 'validate: POSIX shell syntax passed\n'

if [ -f "$repo_dir/cmd/bench-worker/main.go" ]; then
	command -v go >/dev/null 2>&1 || fail "go is required while cmd/bench-worker exists"
	(
		cd "$repo_dir"
		go test ./cmd/bench-worker
	)
	printf 'validate: cmd/bench-worker tests passed\n'
else
	printf 'validate: skip cmd/bench-worker tests (command is not present)\n'
fi

for expected in \
	'MinimumVersion=27' \
	'Distribution=fedora' \
	'Release=44' \
	'Format=disk' \
	'ManifestFormat=json' \
	'CompressOutput=no' \
	'Bootable=yes' \
	'Autologin=no' \
	'Ssh=never' \
	'Checksum=yes' \
	'ExtraTrees=staging' \
	'Ephemeral=yes' \
	'RuntimeNetwork=none' \
	'BindUser=no' \
	'RuntimeBuildSources=no' \
	'VSock=yes' \
	'CPUs=2' \
	'RAM=4G'; do
	require_line "$config" "$expected"
done

if grep -Eq '^(RootPassword|Credentials|SshKey|SshCertificate)=' "$config"; then
	fail "mkosi.conf must not configure login or runtime credentials"
fi
if grep -Eiq '^[[:space:]]*openssh' "$config"; then
	fail "mkosi.conf must not install OpenSSH"
fi
for forbidden in \
	"$script_dir/mkosi.rootpw" \
	"$script_dir/mkosi.key" \
	"$script_dir/mkosi.crt" \
	"$script_dir/mkosi.credentials"; do
	[ ! -e "$forbidden" ] || fail "credential material must not be present: $forbidden"
done

for expected in \
	'User=bench-worker' \
	'Group=bench-worker' \
	'ExecStart=/usr/libexec/bench-worker run -request /run/bench/inbox/%i/request.json -receipt /run/bench/outbox/%i/receipt.json' \
	'UMask=0077' \
	'NoNewPrivileges=yes' \
	'PrivateDevices=yes' \
	'PrivateNetwork=yes' \
	'ProtectSystem=strict' \
	'ProtectHome=yes' \
	'CapabilityBoundingSet=' \
	'AmbientCapabilities=' \
	'RestrictAddressFamilies=AF_UNIX' \
	'RestrictNamespaces=yes' \
	'RestrictSUIDSGID=yes' \
	'IPAddressDeny=any' \
	'KillMode=control-group' \
	'ReadOnlyPaths=/run/bench/inbox/%i' \
	'ReadWritePaths=/work /state /run/bench/outbox/%i'; do
	require_line "$unit" "$expected"
done

require_line "$sysusers" 'u bench-worker - "Bench worker" /var/lib/bench-worker /usr/sbin/nologin'
require_line "$tmpfiles" 'd /run/bench/inbox 0750 root bench-worker -'
require_line "$tmpfiles" 'd /run/bench/outbox 0750 root bench-worker -'
require_line "$tmpfiles" 'd /work 0750 bench-worker bench-worker -'
require_line "$tmpfiles" 'd /state 0700 bench-worker bench-worker -'
printf 'validate: configuration and unit assertions passed\n'

if command -v python3 >/dev/null 2>&1; then
	python3 -m json.tool "$script_dir/capability-lock.example.json" >/dev/null
	python3 -m json.tool "$script_dir/request.example.json" >/dev/null
	printf 'validate: example JSON syntax passed\n'
else
	printf 'validate: skip JSON parser check (python3 is not installed)\n'
fi

host_os=$(uname -s)
if [ "$host_os" != Linux ]; then
	printf 'validate: skip mkosi summary (Linux required; host is %s)\n' "$host_os"
elif ! command -v mkosi >/dev/null 2>&1; then
	printf 'validate: skip mkosi summary (mkosi is not installed)\n'
else
	(
		cd "$script_dir"
		mkosi summary
	)
	printf 'validate: mkosi summary passed\n'
fi
