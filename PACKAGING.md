# Packaging and versioning Bench

Bench should ship as a **suite release** while its filters continue to ship as
independent programs. A suite is one tested set of exact source revisions, not
a new shared runtime and not a requirement that every repository use the same
version number.

## What is in the base suite

| Component | Why it is required | Shipped form |
| --- | --- | --- |
| `bench` | workbench and headless entry point | native executable |
| `ask` | model and replay boundary | native executable |
| `brief` | skill catalogue | native executable |
| `ply` | tool loop and executable verdict | native executable |
| `hone` | verified learning path | native executable |
| `trail` | read-only Ask archive inspection | native executable |
| `agent` | filesystem-worker runtime | script plus private `bin/agent-action-shell` |
| `draft` | agent design/build/prove composition | script plus `skills/draft` |
| `may` | exact, single-use human action decisions | native executable |
| `cage` | kernel-enforced model-action confinement | native executable |

`rules`, `vouch`, and `web` remain useful standalone capabilities.
May and Cage are now required suite components because Review/Loop can admit
their exact-action approval and confinement policies. They remain ordinary
independent filters; packaging them does not merge their runtimes into Bench.
Agent and Trail are required because the folder-worker boundary includes
replayable history. Agent remains an ordinary POSIX shell program rather than
a runtime embedded in Bench.

The release layout is relocatable:

```text
bench-suite-VERSION-GOOS-GOARCH/
  bin/bench  bin/ask  bin/brief  bin/ply  bin/hone  bin/trail
  bin/agent  bin/agent-action-shell  bin/draft  bin/may  bin/cage
  skills/draft/...
  licenses/COMPONENT/LICENSE
  licenses/third-party/MODULE-ID/...
  THIRD_PARTY_NOTICES.md
  suite.json
  go-modules.json
  SHA256SUMS
  install.sh
  INSTALL.md
```

The generated installer derives its public command links from the manifest;
there is no second hard-coded command list to update. A `files` component may
also carry executable assets beneath `bin/` beside its public entry point.
Those companion programs remain private unless they are separate manifest
components. This lets a script command resolve a trusted action adapter beside
its real suite path without adding an accidental user-facing command.

When `bin/bench` sees the valid `suite.json` marker above it, it resolves its
companions from the same `bin` directory. Explicit `BENCH_ASK`, `BENCH_BRIEF`,
`BENCH_PLY`, `BENCH_HONE`, `BENCH_DRAFT`, `BENCH_MAY`, and `BENCH_CAGE` values
still win. The experimental headless `bench home` boundary likewise resolves
`agent` from `BENCH_AGENT` and its history reader from `BENCH_TRAIL`, then the
suite when present or ordinary `PATH`; it also supplies the already-pinned May
path for explicit definition amendments. Agent's action adapter is shipped
beside its real script path and covered by the archive checksum, but is not a
manifest component or public installed command. Without the marker, a
standalone or `go install` build retains the existing `PATH`
behavior. This lets an application invoke a private `bin/bench` by absolute
path without mutating the user's environment.

## The version contract

There are two useful version axes and they should not be conflated:

1. **Suite version.** Bench owns a SemVer release such as `0.5.0`. Developers
   and applications depend on this version. `internal/suite/manifest.json`
   pins every component to an exact Git commit and records its standalone
   version, SPDX license identifier, and license file.
2. **Component version.** Each filter keeps its own SemVer and release cadence
   for standalone users. Components do not move in lockstep merely because a
   suite release changed.

An update to any required component is a manifest change followed by the full
suite build and smoke tests. Compatibility is therefore demonstrated by the
artifact that ships, rather than inferred from ten independent `@latest`
resolutions. Exact revisions are intentional while the command contracts are
young; version ranges can be introduced only after those contracts have
declared compatibility majors of their own.

The manifest committed to Bench uses `self` for Bench because a source commit
cannot contain its own hash. `benchpack` resolves that value to the actual
commit in the emitted `suite.json`. It refuses a mismatched pinned revision or
a dirty source tree for release builds. `-allow-dirty` is visibly recorded in
the archive name, component entries, and separate installation directory for
local development only.

## Building a suite

With the sibling repositories in this workspace:

```sh
cd bench
go run ./cmd/benchpack -workspace .. -out dist
```

From a Bench checkout alone, fetch only the exact revisions named by the
manifest into its ignored source cache and build the same artifact:

```sh
go run ./cmd/benchpack -fetch -out dist
```

Fetched components live in revision-addressed directories such as
`.benchpack/sources/ask-COMMIT`. A missing revision is cloned through a
temporary directory and atomically installed; an older cached revision is
never fetched into, updated, or deleted. `-self` identifies the Bench checkout
independently of that cache and defaults to the current directory, so the
checkout itself can have any directory name.

For the shortest source installation path:

```sh
./install.sh
```

That thin bootstrap checks for Go, Git, and tar; builds and verifies the suite;
then runs the suite's own installer. `-prefix DIR` changes `~/.local`, and
`-allow-dirty` makes a visibly separate development installation.

For an intentionally modified development workspace:

```sh
go run ./cmd/benchpack -workspace .. -out /tmp/bench-dist -allow-dirty
```

The builder cross-compiles the Go programs with `CGO_ENABLED=0` and
`-trimpath`, copies Draft's ordinary files, writes the resolved manifest and
per-file checksums, normalizes archive metadata, and emits one `.tar.gz` plus
an external `.tar.gz.sha256` suitable for an installer or application build.
macOS and Linux are the initial release targets because Draft and the filter
family currently require a Unix host.

`go-modules.json` is read back from the compiled binaries, so it records the
actual Go toolchain, target, build flags, modules, versions, checksums, and
replacements that shipped. It is an authoritative dependency inventory for
application audits and notice generation. `THIRD_PARTY_NOTICES.md` indexes the
ordinary root license, notice, copyright, and patent files copied from every
versioned module in that inventory. The build fails if a shipped module is
unversioned or lacks that evidence. This is useful release evidence, but it
deliberately does not claim to be legal advice or a complete standardized
SBOM.

Every native build also launches the bundled Bench by absolute path with a
minimal `PATH` and confirms that its Ask and Ply boundaries reach the adjacent
suite executables. It then scaffolds a disposable Agent home through the
bundled script and exercises Bench's check, zero-model run, and Trail history
boundaries; the run must create no session. Cross builds compile temporary host
copies from the same revisions so component versions and Draft's generated
reference are still checked without executing a foreign binary.

## Installation surfaces

- **Release archive:** the canonical artifact and the source for all other
  installation methods. Extract it and run `./install.sh [PREFIX]`; the
  default is `~/.local`. It verifies the suite, installs into a versioned
  directory, and links all ten public commands without overwriting unrelated
  files.
  The unpacked directory also runs in place.
- **Homebrew:** a thin formula should install an immutable suite archive, not
  independently install ten `HEAD` formulas. It can link the ten public
  commands and keep Agent's private adapter and `skills/draft` beside the
  versioned keg.
- **Applications:** vendor one unpacked suite directory, verify the archive
  checksum during the app build, and launch its `bin/bench` by absolute path.
  Application releases pin the suite version and checksum. Keep the whole
  directory together; do not copy or update its commands independently. Put
  it in an application resource/helper directory, sign the nested native
  binaries when the platform requires it, and replace the versioned directory
  atomically during application updates. The app should set an explicit
  workspace and state location, while credentials and user skills remain user
  state rather than application resources.
- **Go contributors:** continue to build individual repositories normally;
  use `benchpack` when testing the distributable composition.

Provider credentials, `ASK_MODEL`, sessions, user skills, and project data are
never part of the artifact. Packaging programs together must not package user
state or weaken the existing process boundaries.

## Release gate

A publishable suite release requires all of the following:

1. every pinned checkout is clean and at the manifest revision;
2. every component's own offline checks pass (Draft is tested from a temporary
   source copy after `draft sync` makes its generated tool reference describe
   the exact suite binaries);
3. `go test ./...` passes in Bench;
4. archives build for the supported target matrix;
5. an extracted archive passes checksum verification and reports the expected
   versions for all ten public commands;
6. `bench` launched by absolute path with a deliberately empty `PATH` reaches
   the bundled Ask/Ply/Draft and Agent/Trail boundaries, and all ten public
   commands report their pinned versions, in offline smoke tests.
7. every redistributed project and Go dependency has an approved license and
   the archive contains the required license/notices or generated SBOM.

`.github/workflows/suite.yml` executes this gate on native Linux and macOS,
for both amd64 and arm64, and retains the archives as CI artifacts. It does not
publish a GitHub release until the tag matches the suite version and every
matrix artifact passes. Bench, Draft, Ask, Brief, Ply, Hone, Trail, Agent, May,
and Cage are MIT licensed, and their license files travel in the archive. The
compiled module inventory remains the input to third-party notice generation.
The publish job and a Homebrew formula consume the exact checked artifacts
rather than introducing a second build definition.
