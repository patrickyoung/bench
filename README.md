# bench

A modern terminal workbench for building agents from the bench Unix filters.

The working loop is **Ask → Design → Build → Prove → Learn**. Conversation is
an explicit, replayable `ask` session; the agent project is draft's ordinary
`DESIGN.md`; building and evaluation are `draft build/prove`; durable lessons
are admitted by `hone` into an ordinary `brief` skill. `bench` is not a second
model client, build runtime, evaluator, or memory store. It runs the installed
filters and respects their stdout/stderr/exit-status contracts.

## Run

Requirements:

- Go 1.26 or newer
- `ask` on `PATH` and configured with a model
- `draft` on `PATH` for agent-project creation (`ask`, `brief`, `ply`, and
  `hone` are draft's own dependencies)
- `hone` on `PATH` to admit lessons from verified build recoveries

```sh
go run .
go run . 'Start with these requirements…'
go run . -session 20260820-154753-32d9f393
go run . -new
go run . -project path/to/existing-agent
```

Inside the TUI, press `ctrl+s` to send, `enter` for a newline, `f1` for help,
and `ctrl+c` to quit. While a turn is running, `esc` or `ctrl+c` interrupts
the process. Press `ctrl+d` to promote the user requirements into an agent
project.

Sessions are written to `.bench/sessions` only after the first message is
sent. Set `BENCH_DIR` to place them elsewhere, or `BENCH_ASK` to use a
specific `ask` executable (also useful for testing).

`BENCH_DRAFT` selects a specific `draft` executable. This is useful when
running from the bench checkout before installing `draft/bin/draft` on
`PATH`.

`BENCH_HONE` likewise selects the `hone` executable used by the Learn stage.

When saved sessions exist, `bench` opens an explicit session picker. It does
not guess that the newest one is current. Before a selected conversation is
shown or continued, bench runs:

```sh
ask replay -check SESSION
ask replay SESSION
```

The first command must succeed. The second command's public rendering becomes
the restored transcript; bench does not import ask internals or decode its
event schema. `-session id-or-path` selects directly, while `-new` bypasses
the picker.

## From requirements to a checked design

`ctrl+d` opens the Design stage. It copies only user-authored requirements
from the current in-memory conversation; assistant prose never becomes a
requirement silently. The user reviews the project path and description, and
`ctrl+s` runs the equivalent of:

```sh
draft new DIR DESCRIPTION
draft check DIR
```

The directory is constrained to the current workspace. `draft new` owns all
writes and keeps its normal provenance session. `draft check` exit 0 produces
a green **BUILDABLE** verdict and displays the exact check command from
stdout. Exit 1 is the ordinary **NEEDS REVISION** state. Exit 2 is shown as a
broken check. The generated `DESIGN.md` is displayed read-only; edit the real
file with any editor and press `r` to check it again.

## Build, prove, and learn

From a **BUILDABLE** design, press `b`. Bench runs `draft build DIR`, renders
stderr as the visible `brief`/`ply` typescript and stdout as the final answer,
and declares **CHECK PASSED** only when the process exits zero. The replayable
build session lives beside the project at `.draft/build/*.jsonl` and is shown
as evidence. Exit 2 is **NOT DONE**, not a false success inferred from prose.

After a passing build, press `p` to run `draft prove DIR`. The Prove screen
separates measurement from surviving mutations. Exit 0 is **CHECK PROVEN**;
exit 1 is the ordinary **GAPS FOUND** verdict; exit 2 is an execution failure.

After a proven evaluation, press `l`, choose a brief skill name, and press
`ctrl+s`. Bench runs:

```sh
hone -into SKILL .draft/build/SESSION.jsonl
```

`hone` verifies the session and admits only fail-then-pass recoveries. Exit 0
means a lesson was written to the ordinary brief skill catalogue. Exit 1 means
there was nothing new and trustworthy to learn, so nothing was written. The
TUI shows hone's provenance; it never maintains a private memory store.

To add features later, edit the project's ordinary files or `DESIGN.md`, then
reopen it with `bench -project DIR`. Bench reruns `draft check` and returns to
the same Build → Prove → Learn loop. There is no project registry to migrate
or synchronize.

See [DESIGN.md](DESIGN.md) for the product model and why the TUI remains a
thin control plane over the filters.
