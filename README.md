# bench

A modern terminal workbench for getting open tasks done with Ask and ordinary
tools, then promoting recurring work into agents when it deserves a durable
design.

The default loop is **Intent → Contract → Task ↔ Tools**: one schema-bound Ask
turn compiles the user's outcome into visible deliverables, invariants,
acceptance evidence, approval boundaries, assumptions, questions, and limits;
then `ply` composes Ask with programs in the current workspace. The contract,
commands, results, and final answer occupy one explicit Ask session.
**Task → Design → Build → Prove → Learn** is the promotion
path for agent work: the project is draft's ordinary `DESIGN.md`; building and
evaluation are `draft build/prove`; durable lessons are admitted by `hone` into
an ordinary `brief` skill. `bench` is not a second model client, tool runtime,
evaluator, or memory store. It runs the installed filters and respects their
stdout/stderr/exit-status contracts.

## Run

The supported distribution is a pinned suite containing Bench and every tool
required by the base workflow. See [PACKAGING.md](PACKAGING.md) for its exact
contents, version contract, source builder, and application-embedding layout.

Requirements when running from source rather than a suite archive:

- Go 1.26 or newer
- `ask` on `PATH` and configured with a model
- `ply` on `PATH` for the default Ask + tools task mode
- `draft` on `PATH` for agent-project creation (`ask`, `brief`, `ply`, and
  `hone` are draft's own dependencies)
- `brief` and `ply` on `PATH` to browse, author, and refine skills
- `hone` on `PATH` to admit lessons from verified build recoveries

Build a relocatable development suite from the sibling repositories with:

```sh
go run ./cmd/benchpack -workspace .. -out /tmp/bench-dist -allow-dirty
```

On a clean Bench checkout, `./install.sh` fetches missing components into an
ignored local cache, builds and verifies the pinned suite, and installs all
six commands under `~/.local`. Pass `-prefix DIR` to change the destination.
The resulting archive also has an external checksum for application builds;
apps can keep the extracted directory private and invoke `bin/bench` directly.

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
/contract on|off        enable or bypass intent compilation for later work
/check -- COMMAND       set a verifier for the next work outcome
/check off              clear the pending verifier
/accept                 accept every criterion after reviewing the result
/continue               revise a pending result even if its check already passes
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

Outcome contracts are on by default. `/contract off` is the explicit direct-Ply
compatibility path; `bench run -contract=false` is its headless equivalent.
The compiler receives the exact user intent, configured verifier, selected
Brief skills, a bounded read-only workspace inventory, and piped evidence.
Skills supply reusable domain procedure and review expectations to both the
compiler and Ply; they never replace the verifier or count as evidence. Its JSON Schema travels
through Ask's native structured-output boundary. The validated canonical
contract is shown before work and repeated verbatim in Ply's first user
message, so later request digests bind the work to that exact contract and
its compilation is also a sealed `bench.contract/v2` record. Ply binds every
sealed `ply.verifier/v1` receipt to the contract envelope ID. `ask replay -check`
verifies conversation folds, event sequence, and those record-prefix seals.
The contract contains no generated shell command. After Ply stops, Bench seals
`bench.contract-result/v1`: model-assigned `check` labels are proposed
coverage, never authority. A consequential open question or ungranted approval
stops before work. The next reply is compiled with the full original intent,
exact questions/approvals, and the user's answer; a short answer never replaces
the requested outcome.
After inspection, `/accept` seals the interactive user's acceptance; `/continue`
starts a revision even when the retained check already passes. The acceptance
record binds the contract ID, exact contract-result digest, and accepted
criterion IDs. Headless work
remains review-required/exit 2; automation that intentionally treats its check
as the whole verdict uses `-contract=false`.

Open work starts unchecked. `/check -- COMMAND` attaches one literal verifier
to the next outcome; Bench displays it beside the composer and passes it to
Ply without executing or reparsing it. `/check` shows the exact pending value
and `/check off` clears it. Contracted review-required, rejection,
interruption, or broken verification retains it for the same outcome. The
explicit direct-Ply compatibility path keeps its ordinary checked-success
semantics. Promote work whose
definition of done needs design and review with `/agent` instead.

Model choice follows the filters' existing convention. `-m provider/model`
overrides `ASK_MODEL` at startup; `/model provider/model` switches future Ask,
Ask + Ply, skill-refinement, draft-creation, and agent-build turns in the same
workbench. Ask records the model on every request, so switching mid-session is
replayable. `/model default` restores the startup choice.

## Open tasks with Ask + tools

The opening screen is a task composer, not an agent-requirements form. By
default, `enter` first runs the structured contract turn in the same explicit
session, then starts the equivalent public process with the canonical compiled
contract included in `GOAL`:

```sh
ply -sh -C WORKSPACE -f SESSION [-m MODEL] [-s SKILL ...] [POLICY ...] -- GOAL
```

Bench starts both filters directly. The goal is one literal argv value; it is never
evaluated by Bench as shell text. Ply asks the model, consumes its first
complete shell block as one action, returns the real command result, and only
then asks again. Later blocks and premature claims from that model turn are
visibly deferred rather than executed. A turn with no action is the report
that stops the loop. This observation boundary is enforced by Ply, so even a
model that emits an imagined multi-step workflow must encounter real system
state between actions.

Bench renders Ply's typescript—including deferrals—as a durable **TOOLS** block
and its stdout as the **ASK** answer. Both are recoverable from the explicit
Ask session with `ask replay`.

`-sh` is a full-shell grant, not a sandbox, and the yellow **ASK + PLY · FULL
SHELL** label keeps that authority visible. Set `BENCH_TOOLS` to an ordinary
toolbox directory to replace `-sh` with `ply -t DIR`; the label then names that
toolbox. A toolbox limits program names but does not confine shell builtins or
redirection—use an operating-system sandbox when that boundary matters.

Ply without `-check` exits zero when the model stops, not when an external
program proves the goal. On the direct compatibility path, the TUI therefore
says **Task stopped · no executable check**, never “done” or “passed.” A
contracted run instead becomes **Ready for review** and exits 2 while its
compiled criteria still await operator or admitted judgment. An open task may start Bench with a
literal shell-backed `-check COMMAND` or set one interactively with `/check --
COMMAND`; that exact argument is visible through `/check`, `/status`, and the
composer. Its zero exit status is recorded alongside the compiler's proposed
`check` coverage, but the contracted outcome remains **Ready for review** and
exits 2; neither model output nor skill text admits semantic coverage. Bench
passes the command as one argv value and never evaluates it itself. Agent
designs remain the durable home for recurring checked work.

Both the interactive workbench and `bench run` accept `-contract=true|false`
and Ply's optional policy controls: `-effort LEVEL`, `-cycles N`, `-turns N`, `-timeout DURATION`,
`-compact`, and `-compactions N`. Bench passes effort names literally through
Ply; Ask and its provider decide which names are supported. Omitted controls
stay omitted so the installed Ply owns its defaults; an explicit zero retains
Ply's documented unbounded meaning. Contracted compaction is rejected until
Bench can independently verify successor lineage; use `-contract=false` for
Ply's existing compaction behavior. On that direct path, checked or compacting
work reports the Ask session it actually used
through a private `-session-out` control artifact. With contract compilation
enabled, the contract turn has already created the session even when Ply's
passing pre-check needs no worker turn. With contracts disabled, absence on a
passing pre-check still means no model turn or session was created. Bench removes the
artifact after reading it and directs later TUI work—including Ask-only
turns—to any reported successor; stdout remains the answer and stderr remains
the human typescript.

Type `/ask` or `/tools off` for an Ask-only turn. That uses the narrower public seam:

```sh
ask -f SESSION -- MESSAGE
```

Switching does not fork or hide state; both modes continue the same explicit,
replayable session.

## Subagents, by asking naturally

Ask Bench directly when independent work would benefit from parallel eyes:

```sh
bench -m openai-codex/your-model \
  'Use three subagents to review correctness, races, and missing tests; wait for all, then fix the confirmed issues.'
```

The root Ply process stays in charge. It may start up to three ordinary child
Ply processes for bounded, read-heavy jobs, wait for their indexed results,
and synthesize them before it edits or answers. Children inherit the resolved
workspace, model, and tool grant, but not the root's Brief skills or check; the
root must give each child a self-contained task and remains the sole writer and
synthesizer. The root's executable check, when configured, still supplies the
literal pass/reject gate. Contracted work remains ready for review until its
criteria are separately admitted; the direct compatibility path retains the
check-as-completion behavior. For genuinely independent writes, use separate
worktrees.

There is no hidden team service or provider-specific runtime. A child is
`$PLY`, stdout is its distilled result, stderr is its typescript, and its exit
status remains 0/1/2/130. Each fan-out writes private, numbered artifacts and
fresh Ask sessions under the parent-scoped directory shown by `/status`:

```text
.bench/subagents/PARENT-HASH/ply-team.XXXXXX/
  001.jsonl  001.out  001.err  001.rc
  002.jsonl  002.out  002.err  002.rc
```

Nothing is created there unless the root actually delegates. Inspect a child
with `ask replay -check PATH.jsonl` and `ask replay PATH.jsonl`. Interrupting
Bench first interrupts the full fan-out so nested Ply processes can stop their
own commands, then escalates for processes that do not cooperate. See
[SUBAGENTS.md](SUBAGENTS.md) for the researched design and exact boundaries.

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

The first command must succeed. It verifies session integrity—that replay
still matches the recorded conversation—not that an answer is true or a task
passed its outcome check. The second command's public rendering becomes the
restored transcript; bench does not import ask internals or decode its event
schema. `-session id-or-path` selects directly, while `-new` bypasses the
picker.

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

For a worker-independent verifier, use `/shell` to run `draft admit DIR` and
approve the exact check through May, exit back to Bench, then press `B`. Bench
runs `draft build -admitted DIR`; Cage keeps the admitted verifier outside the
worker's write grant. The uppercase choice and exact command stay visible, and
retrying preserves the admitted boundary instead of silently downgrading it.

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

## License

Bench is available under the [MIT License](LICENSE).
