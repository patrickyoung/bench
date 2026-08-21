# bench

A modern terminal workbench for getting open tasks done with Ask and ordinary
tools, then promoting recurring work into agents when it deserves a durable
design.

The default loop is **Task ↔ Tools**: `ply` composes Ask with programs in the
current workspace and records commands, results, and the final answer in one
explicit Ask session. **Task → Design → Build → Prove → Learn** is the promotion
path for agent work: the project is draft's ordinary `DESIGN.md`; building and
evaluation are `draft build/prove`; durable lessons are admitted by `hone` into
an ordinary `brief` skill. `bench` is not a second model client, tool runtime,
evaluator, or memory store. It runs the installed filters and respects their
stdout/stderr/exit-status contracts.

## Run

Requirements:

- Go 1.26 or newer
- `ask` on `PATH` and configured with a model
- `ply` on `PATH` for the default Ask + tools task mode
- `draft` on `PATH` for agent-project creation (`ask`, `brief`, `ply`, and
  `hone` are draft's own dependencies)
- `brief` and `ply` on `PATH` to browse, author, and refine skills
- `hone` on `PATH` to admit lessons from verified build recoveries

```sh
go run .
go run . 'Find why the tests fail and fix the smallest root cause.'
go run . -session 20260820-154753-32d9f393
go run . -new
go run . -project path/to/existing-agent
```

Inside the TUI, press `ctrl+s` to run the task, `enter` for a newline, `f1` for
help, and `ctrl+c` to quit. While a turn is running, `esc` or `ctrl+c`
interrupts the process. Press `ctrl+t` to toggle between the default tools mode
and Ask-only, `ctrl+d` to promote the user-authored task into an agent project,
or `ctrl+b` from any idle stage to open the Skills workbench.

Sessions are written to `.bench/sessions` only after the first message is
sent. Set `BENCH_DIR` to place them elsewhere, or `BENCH_ASK` to use a
specific `ask` executable (also useful for testing).

`BENCH_DRAFT` selects a specific `draft` executable. This is useful when
running from the bench checkout before installing `draft/bin/draft` on
`PATH`.

`BENCH_HONE` likewise selects the `hone` executable used by the Learn stage.
`BENCH_BRIEF` and `BENCH_PLY` select the executables used by the Skills
workbench and default task loop. They are also useful for exercising the
complete flow offline with fake filters.

## Open tasks with Ask + tools

The opening screen is a task composer, not an agent-requirements form. By
default, `ctrl+s` starts the equivalent public process:

```sh
ply -sh -C WORKSPACE -f SESSION [-s SKILL ...] -- GOAL
```

Bench starts `ply` directly. The goal is one literal argv value; it is never
evaluated by Bench as shell text. Ply asks the model, runs the shell blocks it
writes, returns command output to the model, and repeats. Bench renders Ply's
typescript as a durable **TOOLS** block and its stdout as the **ASK** answer.
Both are recoverable from the explicit Ask session with `ask replay`.

`-sh` is a full-shell grant, not a sandbox, and the yellow **TASK · FULL
SHELL** label keeps that authority visible. Set `BENCH_TOOLS` to an ordinary
toolbox directory to replace `-sh` with `ply -t DIR`; the label then names that
toolbox. A toolbox limits program names but does not confine shell builtins or
redirection—use an operating-system sandbox when that boundary matters.

Ply without `-check` exits zero when the model stops, not when an external
program proves the goal. The TUI therefore says **Task stopped · no executable
check**, never “done” or “passed.” Checked, repeatable work belongs in an agent
design whose `DESIGN.md` supplies the executable verdict.

Press `ctrl+t` for an Ask-only turn. That uses the narrower public seam:

```sh
ask -f SESSION -- MESSAGE
```

Toggling does not fork or hide state; both modes continue the same explicit,
replayable session.

When saved sessions exist, `bench` opens an explicit session picker. It does
not guess that the newest one is current. Before selected work is
shown or continued, bench runs:

```sh
ask replay -check SESSION
ask replay SESSION
```

The first command must succeed. The second command's public rendering becomes
the restored transcript; bench does not import ask internals or decode its
event schema. `-session id-or-path` selects directly, while `-new` bypasses
the picker.

## Skills from source

`ctrl+b` opens the live `brief ls` catalogue. Typing filters the level-one
name and description metadata already printed by brief; opening an item runs
the public sequence:

```sh
brief path SKILL
brief cat PATH/SKILL.md
brief ls PATH
brief lint -strict PATH
```

The detail view therefore shows the raw, human-editable `SKILL.md`, its
bundled progressive-disclosure files, its resolved provider path, and the
strict executable verdict. There is no copied catalogue or private skill
schema in bench.

Press `ctrl+n` to create a project skill. The default destination is
`.claude/skills`, so it shadows personal skills by the ordinary `BRIEF_PATH`
rule and travels with the project. Paste notes, documentation, logs, examples,
feedback, or paths to readable local source files. Relative paths resolve from
the workspace through the explicit `SOURCE_ROOT` environment contract. Bench
first runs:

```sh
brief new -d .claude/skills NAME
```

Then it sends the source on stdin—not argv—and lets `ply` edit the ordinary
skill directory until brief owns the verdict:

With `SKILL_DIR` as the process working directory, the equivalent invocation
is:

```sh
SOURCE | BRIEF=brief PLY_DIR=.bench/brief/refine/NAME \
  ply -sh -check '"$BRIEF" lint -strict .' GOAL
```

Bench starts `ply` directly rather than through this illustrative shell
pipeline; source is connected to stdin and every argument remains literal.

The visible typescript and replayable Ask session stay under
`.bench/brief/refine/NAME`. Exit 0 is **STRICT CLEAN**, exit 2 is **NOT DONE**,
and no model sentence can override either. On an existing skill, press `e` to
run the same refinement loop against new source or feedback, or `l` to lint
without changing anything.

Press `u` on a skill to toggle it for future task turns. In tools mode Bench
passes each name as a literal `ply -s SKILL`; in Ask-only mode it composes the
public values `ask system` and `brief cat SKILL` into one literal `ask -S`
argument. Ask records the exact system prompt in either request, so the
procedure that shaped a run is replayable rather than hidden in TUI state.

Arbitrary source and verified learning are deliberately different. Source
may refine instructions, but only `hone` may admit a lesson from a replayable
failed-then-passed build recovery. Press `h` from a skill after Build and
Prove to send that evidence to `hone -into`.

## From an open task to a checked design

`ctrl+d` opens the Design stage. It copies only user-authored task text from
the current in-memory session; tool output and assistant prose never become
requirements silently. The user reviews the project path and description, and
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
