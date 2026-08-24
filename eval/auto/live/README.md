# Auto live vertical slice

This is a developer experiment over the public `bench`, `ask`, and `ply`
filters. It measures eight exact disposable fixtures:

- two routine Auto-versus-Review pairs;
- two checked Auto-versus-Review pairs; and
- four consequential Auto routing traps that must stop before Ply.

It is not product telemetry, a benchmark of general language understanding, or
evidence that Auto is better in aggregate. It establishes only that a run
requesting one named model traversed the real process boundaries on these
committed fixtures, with external outcome-oracle parity and no pre-admission Ply invocation
for the consequential traps. The recorded model is the requested model string;
this first slice does not independently attest the provider's selected model.

## Safety boundary

Run this only in a disposable, unprivileged VM or CI account with a short-lived
model credential and no production credentials or data. The harness copies
every fixture to a fresh workspace, uses a narrow synthetic toolbox, and keeps
hash-bound oracles, sessions, ledgers, and trace files outside the worker
workspace. Every toolbox entry has a catalogue synopsis and explicitly
implements side-effect-free `-h` and `--help`; the harness tests both forms
without assuming they are universal Unix conventions. The three connector
commands only append to a synthetic effect
ledger; they do not contact a network or external service. The deletion case
is instead detected by the workspace digest and zero-Ply boundary. This is
defense in depth for a cooperative smoke test, not a sandbox or hostile-worker
evidence boundary: ordinary Quick/Review actions still run with the account's
ambient same-user authority and could address paths outside the workspace.

For a containerized run, pass an absolute operator-owned adapter with
`-action-shell`. The harness rejects symlinks and relative paths, snapshots its
exact bytes into the mode-0700 result controller directory, sets
`PLY_ACTION_SHELL` for nesting, and has the trace wrapper pass the literal
`-action-shell PATH` option on every initial and admitted Ply invocation. A Ply
without that public option therefore fails instead of falling back to the host
shell. The scorer rehashes both source and snapshot. Model actions use that
interpreter; configured
Ply checks remain host-side on `/bin/sh`. The action-shell mechanism is optional
for a different case manifest, but the committed corpus requires it. The `l02`
host verifier is a narrow exception at the candidate-execution seam: it copies
the candidate workspace to a disposable probe, sends a fixed behavioral test
driver through the same manifest-bound action interpreter, rejects any probe
mutation, and deletes the probe. Neither the worker nor the host scorer executes
candidate code directly on the host. This
split allows an operator adapter that exposes only the workspace and toolbox to
keep the immutable oracle and expected trees out of the worker. The adapter is
trusted operator code: its digest proves identity, not confinement. Docker, its
pinned image, mounts, environment, signals, cleanup, and resource limits remain
the adapter's responsibility; this is not Bench Cage or contract
`action_confinement`. The committed checked corpus therefore requires
`-action-shell`; preparation fails before creating a run without it.

The complete `l02` grammar, stdout, stderr, and exit-status contract is visible
in its intent. Its immutable host verifier checks behavior through the bound
action interpreter and reports every mismatched case; it does not compare the
candidate's source bytes with the expected implementation. Static host checks
still require the one expected workspace path, its executable bit, and an empty
effect ledger. The corpus test includes a different but behaviorally equivalent
implementation to keep that distinction honest.

The harness never accepts a generated contract. `prepare` writes ordinary,
literal `accept.sh` scripts for safe draft arms. A human must inspect the exact
`draft.json` and its digest in `NEXT.tsv`, then execute each corresponding
script. Consequential arms never receive an acceptance script.

## Run

Build the three public binaries from the exact revisions you want to measure.
Then bind the immutable case bytes explicitly:

```sh
cases_sha=$(shasum -a 256 eval/auto/live/cases.jsonl | awk '{print $1}')
go run ./cmd/bench-auto-live prepare \
  -bench /absolute/path/to/bench \
  -ask /absolute/path/to/ask \
  -ply /absolute/path/to/ply \
  -action-shell /absolute/path/to/container-action-shell \
  -model PROVIDER/MODEL \
  -effort high \
  -cases eval/auto/live/cases.jsonl \
  -expect "sha256:$cases_sha" \
  -out /absolute/new/result-directory
```

`prepare` prints the absolute `NEXT.tsv` path. Inspect every listed draft with
ordinary tools, then run the listed script paths yourself. Finally:

`NEXT.tsv` includes each script's expected exit. Routine Review scripts
successfully admit and run the contract but intentionally exit `2` at the
`review_required` boundary; checked Review/Loop scripts are expected to exit
`0`. Each script preserves that raw Bench exit, prints the expected value, and
writes `accept.stdout`, `accept.stderr`, `accept.exit`, and elapsed-time
evidence beside the script. Run scripts individually rather than under an
unqualified `set -e` loop.

```sh
go run ./cmd/bench-auto-live score -out /absolute/result-directory
```

`score` rehashes the case manifest, fixtures, toolbox, oracle, executables, and
the snapshotted action interpreter and Ply tracing wrapper; verifies every Ask
session with `ask replay -check -json`;
runs the external artifact oracle; and writes `results.jsonl` plus
`summary.json`.

Exit status follows a filter contract:

- `0`: all eight exact cases and all four paired safe outcomes passed;
- `1`: the experiment ran correctly but at least one route/artifact gate missed;
- `2`: usage, input integrity, replay, admission, or oracle infrastructure was
  incomplete or broken;
- `130`: interrupted.

Timing is retained only as descriptive wall-clock evidence. This slice makes no
latency, token-cost, statistical, or model-ranking claim. A larger paired study
with repetitions, blinded artifact review, and live-model provenance remains a
separate experiment.
