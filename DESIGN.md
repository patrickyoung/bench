# bench design

`bench` is a terminal workbench for open tasks and agent-building, composed
from Unix filters. The TUI is the glass; the filters remain the machine.

## The product loop

The default product is an open task loop:

```
task <-> tools
 ask      ply
```

Ply carries Ask through commands and results in one replayable session. When
the work is recurring or needs an executable definition of done, that same
task can be promoted through five mechanically distinct stages:

```
requirements -> design -> build -> evaluate -> learn
       ask        draft      ply       draft     hone
                            brief      suites    brief
```

Each arrow is an artifact or a process boundary, not an in-memory framework
call. A user can begin with an ordinary task, inspect everything the model
said and every command that ran, and keep adding checks and capabilities
without changing runtimes.

The first vertical slice was `ask`: a polished, resumable conversation backed
by one explicit append-only Ask session. Ply now makes that session useful for
open tool-backed tasks, and the same evidence boundary carries promoted work
through Design, Build, Prove, and Learn.

When a user explicitly asks for subagents, the same process boundary grows a
flat fan-out rather than a second runtime. The root Ply may start up to three
nested `$PLY` processes for independent, read-heavy work. Each child owns a
fresh Ask session and indexed stdout/stderr/status artifacts under
`.bench/subagents`; only the root synthesizes and writes in the shared tree,
while the configured root check remains the completion verdict. Nested prompts
do not advertise further delegation.

## Non-negotiable boundary

The TUI does not become a model client, an agent runtime, or a shell
implementation. It can yield the terminal to the operator's `$SHELL` or
`$EDITOR`, then restore itself. Its
default task turn executes the equivalent of:

```sh
ply -sh -C WORKSPACE -f SESSION [-m MODEL] [-s SKILL...] [POLICY...] -- GOAL
```

With a `BENCH_TOOLS` directory, `-t DIR` replaces `-sh`; selected procedures
are repeated `-s SKILL` arguments. Ply owns the Ask→command→result loop and
records it in the explicit Ask session. That boundary consumes one complete
shell block per model turn, returns its real result, and visibly defers any
later actions or claims written before the result existed. Bench neither
reimplements nor weakens that rule: it renders stderr as visible tool evidence
and stdout as the answer. Because an unchecked Ply exit zero means only that
the model stopped, the UI never calls that outcome done or passed.
`-check COMMAND` deliberately opts an open task into a shell-backed executable
verdict; Bench passes that command as one literal argument, displays it through
`/status`, and distinguishes a passing pre-check from a worked, replayable
session. `-effort`, `-cycles`, `-turns`, `-timeout`, `-compact`, and
`-compactions` are optional process policy, not a second loop, and omission
leaves Ply's defaults in charge.

Compaction may move the work into a successor Ask session. For checked or
compacting work, Bench passes Ply a private `-session-out FILE` artifact path.
Ply atomically records the absolute current session before model work and after
each successful transition; a passing pre-check creates neither session nor
artifact. Bench reads it only with the terminal process event, removes it, and
makes a reported session authoritative for later interactive turns. Control
data therefore never competes with answer stdout or typescript stderr.

The full-shell grant is visible at all times. A toolbox is an executable-name
grant, not confinement; operating-system sandboxing remains a separate
composition rather than a misleading TUI boolean.

The same seam is available without a TUI. `bench run` and automatically
redirected plain invocations pass stdin to Ply, copy stdout and stderr to the
same streams unchanged, and return its exit status. `bench ask` does the same
for direct Ask. `bench tui` is the explicit override when terminal detection
is unusual. The interactive and headless surfaces therefore differ in
presentation, not process semantics.

## Distribution is a pinned composition

The source projects remain independent filters, but the end-user product is a
suite release containing `bench`, `ask`, `brief`, `ply`, `hone`, and Draft's
script and skill at exact revisions. `internal/suite/manifest.json` is the
machine-readable compatibility set and `cmd/benchpack` turns matching sibling
checkouts into one checksummed, relocatable archive. The emitted manifest
resolves Bench's `self` revision to the actual source commit and records dirty
development builds explicitly.

A suite marker lets Bench find companions next to its own executable. Without
that marker it preserves ordinary `PATH` lookup, and explicit `BENCH_*`
overrides always win. This keeps `go install` useful while allowing an app to
vendor and invoke one private, versioned runtime without rewriting global
state. See [PACKAGING.md](PACKAGING.md) for the release and application
contract.

Ask-only is the deliberately narrower toggle. For an unskilled turn it
executes the equivalent of:

```sh
ask -f .bench/sessions/SESSION.jsonl -- "$message"
```

With explicit skills selected, its seam is still ordinary composition:

```sh
ask -S "$(ask system; brief cat SKILL...)" \
  -f .bench/sessions/SESSION.jsonl -- "$message"
```

It captures stdout as the answer, renders stderr as transient activity, and
treats the exit code as the outcome. The JSONL session is authoritative. A
failed turn stays failed even if its output sounds confident.

These boundaries are what let later screens compose rather than accrete:

- Open tasks and Build configure and observe `ply`; neither copies its loop.
- Procedures come from `brief`, not a hidden prompt library. Selected skill
  names are visible, and Ask records the exact composed prompt per turn.
- Evaluation runs named checks and mutation suites; it does not ask the model
  if it won.
- Learning admits only `hone` output from verified recoveries.
- Approval and confinement are separate `may` and `cage` compositions, not booleans in UI
  state.

## The agent project already has a format

`bench` will not invent an `agent.toml`. The durable project artifact is the
`DESIGN.md` owned by `draft`: one human-readable agreement containing the
requirements, the split between deterministic stages and model decisions,
the explicit refusals, and a `## Check` command whose exit status is done.

The build screen therefore composes the existing public verbs:

```sh
draft new DIR DESCRIPTION   # requirements become a document to review
draft check DIR             # exit 0 buildable, 1 not yet, 2 broken
draft build DIR             # ply works until that same check exits zero
draft prove DIR             # mutate it; measure what the check detects
```

`draft build` already delegates its model, procedure, loop, and verdict to
`ask`, `brief`, `ply`, and the design's check. The TUI's job is to make those
states legible and interruptible, not to reproduce any of them. The stage line
is consequently `Ask → Design → Build → Prove → Learn`, with every stage
leaving an ordinary file or command behind.

## Interaction model

The whole first screen is one open task. There is no dashboard before the work
and no command palette full of promises.

- The transcript is primary and scrollable.
- The composer is always visible.
- `enter` runs or sends; `alt+enter` and enhanced-terminal `shift+enter` insert
  a newline.
- Slash commands expose `/model`, `/tools`, `/ask`, `/work`, `/skills`,
  `/agent`, `/shell`, `/status`, `/help`, and `/quit`; `//` sends a leading
  slash literally.
- `ctrl+d` exits an empty prompt and `ctrl+z` suspends, preserving familiar
  Readline and job-control meanings.
- `/agent` promotes only user-authored task text into an agent design; tool
  output and assistant prose are never requirements silently.
- `esc` interrupts a running process.
- `ctrl+c` interrupts first and quits only when idle.
- `f1` shows the complete command/key contract; `f2` opens Skills.
- The active model, tool grant, process state, and durable session path are visible.

Model choice is one value across the product. `-m` seeds it and `/model`
changes future work. Bench passes it as literal `-m` to Ask and Ply, and as
`ASK_MODEL` to draft's model-backed stages. Skill refinement uses it too.
For open tasks, `-effort` similarly passes literally to Ply and Ask rather than
creating a second provider-policy implementation inside Bench.
Every underlying Ask request records the actual model, so a mid-session switch
does not weaken replay evidence.

The layout collapses at narrow widths instead of clipping a decorative side
rail. Colour is semantic garnish; labels and spacing carry the hierarchy.

## Skills are files; refinement is a checked loop

The Skills workbench does not read brief's directories itself. It composes
`brief ls`, `path`, `cat`, and `lint -strict`, preserving stdout, stderr, and
the grep-like 0/1/2 outcomes. Level-one metadata is enough to browse; the raw
body and bundled files are disclosed only after a user chooses a skill.

New skills begin with `brief new -d DIR NAME`. Content—pasted prose, logs,
examples, feedback, or paths to local files—travels on stdin to `ply`, never
through a shell or argv. `SOURCE_ROOT` names the workspace explicitly so
relative local paths remain useful while ply works inside the skill directory.
Ply edits the ordinary directory and the fixed check is `"$BRIEF" lint
-strict .`; user text cannot alter it. Refinement sessions live under
`.bench/brief/refine/NAME`, outside the skill so provenance does not pollute
progressive disclosure.

This produces two intentionally different growth paths:

- **source refinement:** material helps `ply` improve a procedure, with
  `brief lint -strict` proving its shape but not the truth of every claim;
- **verified learning:** `hone -into SKILL SESSION` may append a lesson only
  when a replayable build failed and later passed.

Calling arbitrary notes “learning” would erase the only evidence boundary in
the system. The UI names the distinction instead of smoothing it away.

## State and artifacts

By default, sessions live under `.bench/sessions` in the working directory.
`BENCH_DIR` moves that root. The directory is created only when the first
turn is sent, so opening and quitting leaves the workspace untouched.

One TUI invocation owns one explicit session lineage shared by tools and
Ask-only turns. It begins at the selected path and follows only a successor
that the invoked Ply reports through `-session-out`; it never changes Ask's
`current` pointer and never guesses which global conversation the user meant.
That makes two workspaces, two terminals, and replay all unsurprising.

Saved task sessions are discovered only by filename and filesystem metadata.
Opening one is the ordinary filter sequence `ask replay -check SESSION`
followed by `ask replay SESSION`. A failed proof returns to the picker. The
human rendering from the second command is shown verbatim (with terminal
control characters removed); the TUI does not learn ask's event schema.

Agent projects have no registry. `bench -project DIR` reopens the ordinary
directory and calls `draft check DIR`; the current file and command outcomes
reconstruct the UI state. Build evidence stays under `.draft/build` because
bench sets `PLY_DIR` for the public `draft build` process, not because the TUI
owns the session format.

Learning has the same shape. Bench calls
`hone -into SKILL .draft/build/SESSION.jsonl`, shows stdout and stderr, and
maps exit 0/1/2 to admitted/nothing/broken. Hone alone verifies the recovery
and writes the brief skill. There is no TUI memory schema.

## Growth order

1. **Done:** make the Ask process boundary, cancellation, resize behaviour,
   empty and error states excellent.
2. **Done:** restore only sessions proven and rendered through `ask replay`,
   without parsing private ask internals.
3. **Done:** integrate `draft new` and `draft check`; `DESIGN.md` is the agent
   project, not a new manifest owned by the TUI.
4. **Done:** render `draft build` as the visible `brief` + `ply` build run it
   already is, retaining its replayable evidence beside the project.
5. **Done:** add `draft prove` as an executable evaluation verdict with
   survivor reporting.
6. **Done:** admit lessons through `hone`, with provenance visible beside each
   one and exit 1 preserved as “nothing to learn.”
7. **Done:** reopen an existing project from its files and public check, with
   no private registry.
8. Add domain-specific suites as ordinary check artifacts when a real agent
   design calls for them; do not make a suite framework inside the TUI.
9. **Done:** integrate `brief` as a progressive-disclosure Skills workbench,
   compose source-driven refinement through `ply`, and let selected skills
   shape replayable Ask turns without a private catalogue or prompt store.
10. **Done:** make open tasks the default, composing Ask + tools through Ply,
    retaining visible tool evidence, exposing the full-shell/toolbox grant,
    and preserving Ask-only and agent-design promotion as deliberate paths.
11. **Done:** align the workbench with shell conventions: composable headless
    filters, automatic pipe detection, explicit model selection, slash
    commands, Enter-to-run, Ctrl-D/Ctrl-Z semantics, operator shell/editor
    handoff, and consistent model propagation through every AI-backed stage.
12. **Done:** make explicit subagent requests work through nested ordinary Ply
    processes, with flat bounded fan-out, inherited model/tool/workspace
    context, private parent-scoped evidence, ordered failure-aware synthesis,
    and cooperative whole-tree interruption.

At every stage, deleting the TUI must leave a usable directory of files and
commands.
