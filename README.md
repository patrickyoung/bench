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
go run . -m openai-codex/your-model 'Find the failing test and fix it.'
go run . -f 20260820-154753-32d9f393
go run . -n
go run . -project path/to/existing-agent
git diff | bench 'review this patch'
```

Inside the TUI, `enter` runs or sends and `alt+enter` or `shift+enter` inserts a
newline. `ctrl+c` interrupts or quits, `ctrl+d` exits an empty prompt, and
`ctrl+z` suspends to the parent shell so `fg` returns. `f1` opens help and `f2`
opens Skills. These retain normal shell and Readline meanings instead of using
control keys as a private command language.

The prompt accepts explicit, discoverable commands:

```text
/model provider/model   show or switch the model for every later stage
/tools shell|off|PATH   choose Ask + Ply authority or Ask-only
/ask · /work            switch directly between Ask and Ask + Ply
/skills                 browse and build Brief skills
/agent [description]    promote user task text into a checked design
/shell                  open $SHELL in the workspace; exit returns
/status                 show mode, model, and active skills
/help                    show all commands and keys
/quit                    exit Bench
//text                   send a message beginning with one literal slash
```

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

Model choice follows the filters' existing convention. `-m provider/model`
overrides `ASK_MODEL` at startup; `/model provider/model` switches future Ask,
Ask + Ply, skill-refinement, draft-creation, and agent-build turns in the same
workbench. Ask records the model on every request, so switching mid-session is
replayable. `/model default` restores the startup choice.

## Open tasks with Ask + tools

The opening screen is a task composer, not an agent-requirements form. By
default, `enter` starts the equivalent public process:

```sh
ply -sh -C WORKSPACE -f SESSION [-m MODEL] [-s SKILL ...] [POLICY ...] -- GOAL
```

Bench starts `ply` directly. The goal is one literal argv value; it is never
evaluated by Bench as shell text. Ply asks the model, runs the shell blocks it
writes, returns command output to the model, and repeats. Bench renders Ply's
typescript as a durable **TOOLS** block and its stdout as the **ASK** answer.
Both are recoverable from the explicit Ask session with `ask replay`.

`-sh` is a full-shell grant, not a sandbox, and the yellow **ASK + PLY · FULL
SHELL** label keeps that authority visible. Set `BENCH_TOOLS` to an ordinary
toolbox directory to replace `-sh` with `ply -t DIR`; the label then names that
toolbox. A toolbox limits program names but does not confine shell builtins or
redirection—use an operating-system sandbox when that boundary matters.

Ply without `-check` exits zero when the model stops, not when an external
program proves the goal. The TUI therefore says **Task stopped · no executable
check**, never “done” or “passed.” An open task may instead start Bench with a
literal shell-backed `-check COMMAND`; that exact argument is visible through
`/status`, and only its zero exit status produces **Task done · executable check
passed**. Bench passes the command as one argv value and never evaluates it
itself. Agent designs remain the durable home for recurring checked work.

Both the interactive workbench and `bench run` accept Ply's optional policy
controls: `-cycles N`, `-turns N`, `-timeout DURATION`, `-compact`, and
`-compactions N`. Omitted controls stay omitted so the installed Ply owns its
defaults; an explicit zero retains Ply's documented unbounded meaning. For
checked or compacting work, Ply reports the Ask session it actually used
through a private `-session-out` control artifact. Its absence on a passing
pre-check means no model turn or session was created. Bench removes the
artifact after reading it and directs later TUI work—including Ask-only
turns—to any reported successor; stdout remains the answer and stderr remains
the human typescript.

Type `/ask` or `/tools off` for an Ask-only turn. That uses the narrower public seam:

```sh
ask -f SESSION -- MESSAGE
```

Switching does not fork or hide state; both modes continue the same explicit,
replayable session.

## Unix filters and an interactive shell

Bench is both a TUI and a filter. When stdin or stdout is redirected, a plain
invocation automatically behaves like `bench run`; `bench tui` explicitly
forces the alternate-screen interface when terminal detection is unusual:

```sh
git diff | bench -m openai-codex/your-model 'review this patch'
go test ./... 2>&1 | bench run 'fix the smallest root cause' >answer.md 2>tools.log
build.log | bench ask 'explain the first useful failure' | less
bench run -t .bench/tools -s go-review -f review.jsonl 'review this tree'
bench run -check 'go test ./...' -turns 20 -timeout 90s -compact 'fix it'
```

For both headless commands, piped bytes are stdin evidence, the answer alone is
stdout, progress or the Ply typescript is stderr, and the filter exit status is
returned unchanged. Goals, model specs, skill names, and paths remain literal
argv values; Bench never evaluates them as shell syntax.

`/shell` temporarily gives the terminal to `$SHELL` in the workspace, then
restores the TUI when that shell exits. It is deliberately operator-controlled
and not inserted into model context or presented as Ask evidence. `ctrl+z` is
the lighter Unix job-control path. In Design review, `e` opens `DESIGN.md` with
`$VISUAL`, then `$EDITOR`, then `vi`, and automatically reruns `draft check`
when the editor closes.

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

`/skills` or `f2` opens the live `brief ls` catalogue. Typing filters the level-one
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

`/agent` opens the Design stage. It copies only user-authored task text from
the current in-memory session; tool output and assistant prose never become
requirements silently. The user reviews the project path and description, and
`ctrl+enter` (or the `ctrl+s` fallback) runs the equivalent of:

```sh
draft new DIR DESCRIPTION
draft check DIR
```

The directory is constrained to the current workspace. `draft new` owns all
writes and keeps its normal provenance session. `draft check` exit 0 produces
a green **BUILDABLE** verdict and displays the exact check command from
stdout. Exit 1 is the ordinary **NEEDS REVISION** state. Exit 2 is shown as a
broken check. The generated `DESIGN.md` is displayed read-only; press `e` to
edit the real file with the conventional editor environment and recheck it, or
edit it elsewhere and press `r`.

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
`ctrl+enter` (or `ctrl+s`). Bench runs:

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
