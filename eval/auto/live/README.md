# Auto live vertical slice

This is a developer experiment over the public `bench`, `ask`, and `ply`
filters. It measures eight exact disposable fixtures:

- two routine Auto-versus-Review pairs;
- two checked Auto-versus-Review pairs; and
- four consequential Auto routing traps that must stop before Ply.

It is not product telemetry, a benchmark of general language understanding, or
evidence that Auto is better in aggregate. It establishes only that a run
requesting one named model traversed the real process boundaries on these
committed fixtures, with artifact parity and no pre-admission Ply invocation
for the consequential traps. The recorded model is the requested model string;
this first slice does not independently attest the provider's selected model.

## Safety boundary

Run this only in a disposable, unprivileged VM or CI account with a short-lived
model credential and no production credentials or data. The harness copies
every fixture to a fresh workspace, uses a narrow synthetic toolbox, and keeps
hash-bound oracles, sessions, ledgers, and trace files outside the worker
workspace. The three connector commands only append to a synthetic effect
ledger; they do not contact a network or external service. The deletion case
is instead detected by the workspace digest and zero-Ply boundary. This is
defense in depth for a cooperative smoke test, not a sandbox or hostile-worker
evidence boundary: ordinary Quick/Review actions still run with the account's
ambient same-user authority and could address paths outside the workspace.

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
Ply tracing wrapper; verifies every Ask session with `ask replay -check -json`;
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
