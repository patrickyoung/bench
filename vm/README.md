# Bench terminal-minimal VM scaffold

This directory is a buildable foothold for a Fedora 44 QEMU VM using current
mkosi v27 configuration. It stages the standalone `bench-worker`, builds an
uncompressed bootable GPT disk, and asks mkosi to emit a JSON package manifest
and SHA-256 checksum file.

This is not a production worker runtime. It is not a Firecracker broker, a
jailer, a capability-lock verifier, a credential proxy, a transport, an image
signing pipeline, or proof of isolation. QEMU/KVM and the systemd sandboxing in
this image are useful development boundaries, not substitutes for the host-side
policy and acceptance gates in [DIGITAL_WORKER.md](../DIGITAL_WORKER.md).

## Deliberately inert guest

The image has no root password, autologin, SSH package, SSH unit, guest
credential, or enabled worker instance. Its console is read-only. mkosi creates
a VSock device, but nothing in the guest listens on it. `RuntimeNetwork=none`
removes mkosi's VM network, and the worker service also gets a private network
namespace, IP deny policy, and only `AF_UNIX` sockets.

The guest therefore has no control plane. A successful `mkosi vm` is only a
boot smoke test. No request can run until a future external controller safely
provides the per-run directories and volumes, triggers one explicit template
instance, and retrieves its artifacts.

The root disk is run with `Ephemeral=yes`, so changes made by `mkosi vm` are
discarded on shutdown. This is not an immutable or signed root filesystem.
Fedora 44 is pinned, but package versions are not snapshot-pinned, so two builds
at different times need not be byte-for-byte reproducible. A checksum detects
artifact corruption; it does not authenticate the image.

## Exact worker interface

One future controller-owned run named `RUN_ID` has this guest interface:

```text
/run/bench/inbox/RUN_ID/request.json    immutable request
/run/bench/outbox/RUN_ID/stdout         exclusive worker output
/run/bench/outbox/RUN_ID/stderr         exclusive worker output
/run/bench/outbox/RUN_ID/receipt.json   exclusive provisional receipt
/work                                   controller-provided workspace
/state                                  controller-provided writable state
```

After verifying the admitted contract and a signed capability lock outside the
guest, the controller would start exactly:

```text
bench-worker@RUN_ID.service
```

The template invokes no shell:

```text
/usr/libexec/bench-worker run -request /run/bench/inbox/RUN_ID/request.json -receipt /run/bench/outbox/RUN_ID/receipt.json
```

The request schema is `bench.worker-request/v1`: exact `argv`, an absolute
`cwd` beneath `/work`, an optional `stdin_path` beneath the inbox, separate
stdout and stderr paths beneath the outbox, a timeout, and the lowercase
SHA-256 digest of the already-verified capability lock. The worker preserves
the child status and writes a `bench.worker-receipt/v1` receipt. `run_id` must
match the request, receipt, stdout, stderr, and optional stdin directory
namespace; a cross-run path fails before execution. The worker does not
verify the lock, authorize argv, interpret a task, decide completion, mount
volumes, or retry effects.

The guest receipt is explicitly `"provisional": true`. Process-group cleanup
inside the unprivileged worker cannot contain a hostile descendant that calls
`setsid`. The service therefore uses systemd control-group cleanup, and a
future external controller must wait until the instance is inactive and its
cgroup is empty, re-hash the final stdout/stderr artifacts, compare request and
unit status, and write an authoritative receipt outside the guest. A newline
terminated guest receipt before that point is evidence, not acceptance.

`request.example.json` mirrors the current Go schema. The zero digests in both
example JSON files are conspicuous placeholders, not valid authority. A real
controller must canonicalize, verify, and bind real digests before creating a
VM; merely replacing the zeros does not make the illustrative lock signed.

## Validate

From the repository root, on any host:

```sh
sh vm/validate.sh
```

Validation checks both scripts with `sh -n`, runs `go test
./cmd/bench-worker` when that command exists, parses the example JSON when
Python is available, and asserts the important no-credential, no-network, and
service-hardening settings. On Linux with mkosi installed it also runs `mkosi
summary` from this directory. macOS and hosts without mkosi report a clear
syntax-check skip and still validate everything else.

## Build

Building requires Linux, Go matching the repository's `go.mod`, mkosi v27,
and mkosi's QEMU/Fedora dependencies. KVM is used when available. From the
repository root:

```sh
sh vm/build.sh
```

The script uses the native `amd64` or `arm64` target, forces `GOOS=linux` and
`CGO_ENABLED=0`, stages `vm/staging/usr/libexec/bench-worker`, checks its Go
build metadata and static ELF identity, then executes `mkosi build` from
`vm/`. Staging, output, incremental cache, and mkosi private history are
ignored. The resulting raw disk, JSON manifest, and checksum artifacts appear
under `vm/output/`.

mkosi may need distribution-specific host packages and unrestricted user
namespaces. Use `mkosi dependencies` on the Linux builder to inspect those
requirements. Image construction downloads Fedora packages; runtime networking
remains disabled.

## Boot smoke test

After a successful build, on Linux with QEMU:

```sh
cd vm
mkosi vm
```

Expect boot logs on a read-only console, no login route, and no worker run.
Stopping the VM discards its snapshot. This command does not exercise the
request/receipt interface because that transport and controller intentionally
do not exist here.

## Security boundary and next gates

The unit runs as a fixed `systemd-sysusers` account with an empty capability
set, a read-only system, explicit writable paths, private devices/mounts/IPC/
temporary/network namespaces, namespace and address-family restrictions, and
other systemd hardening. Those settings are defense in depth around the guest
process; they do not confine QEMU on the host, prove a hostile kernel safe, or
enforce the capability-lock example.

Before this can be called a production digital-worker runtime, it still needs:

1. A controller-side canonical capability-lock verifier and signature/update
   trust with fail-closed pre-launch tests.
2. A broker/jailer that creates disposable VMs, transports exact files over a
   designed VSock protocol, mounts only admitted work/state, triggers one unit,
   waits for an empty service cgroup, re-hashes guest artifacts, and preserves
   authoritative receipts outside replaceable compute.
3. Host cgroup v2 CPU, memory, PID, disk, and I/O limits plus host-side egress
   enforcement; guest settings are not sufficient enforcement.
4. Secret-free launch tests and external short-lived identity, credential, and
   consequential-action proxies. Long-lived credentials must never enter the
   image, environment, model context, artifacts, or snapshots.
5. Signed, digest-pinned image/kernel/tool inputs, an SBOM and provenance,
   reproducible-build evidence, canary health checks, and rollback.
6. End-to-end QEMU and later Firecracker boundary tests for path escapes,
   symlinks/hard links, devices, namespaces, resource exhaustion, cancellation,
   crashes, duplicate delivery, and unknown external effects.

Until those gates have executable evidence, this directory should be described
only as the QEMU/KVM terminal-minimal scaffold it is.
