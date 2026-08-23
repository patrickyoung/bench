# bench

A modern terminal workbench for getting open tasks done with Ask and ordinary
tools, then promoting recurring work into agents when it deserves a durable
design.

The default workflow is **Intent → Contract → Task ↔ Tools**: one schema-bound Ask
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
/mode quick|review|loop choose immediate work, contract negotiation, or verifier pursuit
/contract               reopen the durable draft or admitted revision
/contract on|off        compatibility aliases for review/quick
/contract edit          edit the proposed JSON with $VISUAL/$EDITOR
/contract import        validate and seal changes from another JSON editor
/contract accept        admit the reviewed draft, then start Ply
/contract run           explicitly retry the admitted revision
/check -- COMMAND       set a verifier for the next work outcome
/check all              admit that verifier as judge of every criterion
/check off              clear the pending verifier
/accept                 accept every criterion after reviewing the result
/continue               retry work under the same admitted contract
/skills                 browse procedures that shape contracts and work
/agent [description]    promote recurring work into a checked design
/shell                  open $SHELL in the workspace; exit returns
/status                 show exact authority, policy, and evidence paths
/help                    show all commands and keys
/quit                    exit Bench
//text                   send a message beginning with one literal slash
```

The task screen teaches the same flow it executes:

1. **NEGOTIATE** — Ask proposes durable JSON. Revise it in natural language or
   edit it directly; Ply has not started.
2. **ADMIT** — `/contract accept` seals the exact reviewed revision.
3. **WORK** — a rolling **WORKING · LIVE** panel shows commands and real output
   as Ply observes them; the completed **WORK LOG** stays in the transcript.
4. **VERIFY** — a durable **OUTCOME** card says complete, ready for review,
   decision needed, or not accepted, and names the next useful interaction.

`/status` adds a readable status card to the transcript instead of hiding the
current model, tool grant, contract mode, check authority, Brief skills, and
evidence paths in a transient footer. The footer stays short and contextual:
it suggests the next useful action rather than repeating every command.

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

Bench names autonomy rather than making the user reason about an internal
contract switch. `review` is the default: negotiate and admit a durable outcome
before work. `quick` starts immediately with the current tool grant and is for
work where you choose to skip contract review. `loop` negotiates once, then
lets one foreground Ply invocation keep trying until its configured check
accepts or a finite turn bound, explicit cycle bound, failure, context limit,
or interrupt stops it. Loop requires `-check`; it does not imply `-check-all`,
survive process restart, or add a Bench scheduler. While a TUI Loop is running,
ordinary composer text is appended to a controller-created regular file and
Ply consumes complete UTF-8 lines at model-turn boundaries as implementation
guidance. That channel cannot change the admitted contract, tools, approvals,
or verifier. `/contract on|off` and `-contract=true|false`
remain compatibility aliases for `review|quick`; new use should prefer
`/mode` and `-mode`.
The compiler receives the exact user intent, configured verifier, selected
Brief skills, a bounded read-only workspace inventory, and piped evidence.
Skills supply reusable domain procedure and review expectations to both the
compiler and Ply; they never replace the verifier or count as evidence. Its JSON Schema travels
through Ask's native structured-output boundary. The validated canonical
contract is written to an ordinary editable `draft.json` under
`.bench/contracts`, and a sealed `bench.contract-proposal/v1` snapshot records
each ordinary generated or manual proposal. A draft with the operator-owned
`approval_policy: "every-action"` uses proposal v2; the policy is visible in
the editable envelope and changes its exact digest. Natural-language changes use another Ask
schema turn. `bench contract edit` or `/contract edit` uses `$VISUAL` or
`$EDITOR`; changes made by another JSON editor become admissible only after
`bench contract import` or `/contract import` validates and seals them.
Neither path can start Ply. `/contract accept` re-reads the displayed exact
draft digest, publishes an immutable revision, seals `bench.contract/v3` (or
v4 for every-action approval), and
only then passes those exact admitted bytes to Ply. Ply binds every
sealed `ply.verifier/v1` receipt to the contract envelope ID. `ask replay -check`
verifies conversation folds, event sequence, and those record-prefix seals.
The contract contains no generated shell command. After Ply stops, Bench seals
`bench.contract-result/v1`: model-assigned `check` labels are proposed
coverage, never authority. With an explicit operator `-check-all` admission,
Bench first seals `bench.judge-map/v1`, strictly matches Ply's accepted verifier
receipt, and seals `bench.contract-result/v2`; only that path may automatically
complete a contracted outcome. Invocation-scoped Loop runs seal the same verdict
and evidence fields plus their effective budgets and terminal reason as
`bench.contract-result/v3`; action-gated runs use result v4, while ordinary
Review continues to emit unchanged v1/v2 records.
A consequential open question or unresolved approval stops before work. The
next reply is compiled with the full original intent, exact questions and
approvals, and the user's answer; a short answer never replaces the requested
outcome. With approval policy `off`, that answer authorizes the described
scope and there is no execution-time gate. With `every-action`, it authorizes
preparation only; May must separately grant the exact bytes before execution.
After inspection, `/accept` seals the interactive user's acceptance; `/continue`
starts another implementation attempt under the same admitted contract even
when the retained check already passes. Amend the outcome itself by reopening
`/contract`; the old revision remains immutable. The acceptance
record binds the contract ID, exact contract-result digest, and accepted
criterion IDs. Headless work remains review-required/exit 2 by default.
Automation may use `-check COMMAND -check-all` to make the explicit blanket
assertion that this exact command proves every criterion the compiler emits.
Use it only when that statement is genuinely true.

`-approval every-action` (or `/approval every-action`) is the conservative
execution-time boundary. It is available only with Review or Loop and is bound
into the editable contract before admission. Ply sends every model-authored
shell action—not Ask, Brief, or the verifier—to the standalone `may request`
filter immediately before execution. May binds the physical working directory,
resolved interpreter, exact PATH, nanosecond timeout, contract ID, and literal
script bytes. A parked action exits 75 without execution; a declined action
exits 3. In the TUI, `a` or `/approval decide` yields the terminal to the
ordinary `may decide DIGEST` command. Only May can create or spend the
single-use grant, and the byte-identical action must be proposed again to use
it. Bench replay-verifies Ply's sealed approval receipt and records result v4
before showing an approval state. This policy gates every action deliberately;
neither model prose nor Bench attempts to classify which arbitrary shell text
is risky.

Open work starts unchecked. `/check -- COMMAND` attaches one literal verifier
to the next outcome; Bench displays it beside the composer and passes it to
Ply without executing or reparsing it. `/check` shows the exact pending value
and `/check off` clears it. `/check all` arms the current check as the judge of
all criteria for one contracted outcome; changing or clearing the check clears
that admission. Contracted review-required, rejection,
interruption, or broken verification retains it for the same outcome. The
explicit direct-Ply compatibility path keeps its ordinary checked-success
semantics. Promote work whose
definition of done needs design and review with `/agent` instead.

### The same contract workflow from a shell

The TUI calls the same controller and file-store API exposed by these CLI
commands; it does not own a private contract format or a second agent loop:

```sh
# Compile and stop. stdout is the editable draft path; Ply has not started.
draft_path=$(bench contract draft -C . -f .bench/sessions/gallery.jsonl \
  -s ascii-cinema -approval every-action \
  'create a high-quality ANSI poem gallery')

# Inspect, revise with Ask, or edit the JSON with any Unix editor.
bench contract show -C . -f .bench/sessions/gallery.jsonl >reviewed-draft.json
bench contract revise -C . -f .bench/sessions/gallery.jsonl \
  'require 120x40 output and a plain-text fallback'
bench contract edit -C . -f .bench/sessions/gallery.jsonl
# Or, after changing draft.json with another editor:
bench contract import -C . -f .bench/sessions/gallery.jsonl

# `show` emits the exact draft.json envelope and reports its matching digest.
# That visible envelope includes intent, workspace, tools, check authority,
# skills, evidence digest, and contract body. Admission requires its digest.
bench contract accept -C . -f .bench/sessions/gallery.jsonl \
  -expect sha256:DISPLAYED_DIGEST

# Explicitly retry an already admitted revision without recompiling it.
bench contract run -C . -f .bench/sessions/gallery.jsonl

# Pursue the admitted verifier in one invocation. Omitted cycles become
# unbounded rejections; omitted turns remain explicitly bounded at 50.
bench contract run -C . -f .bench/sessions/gallery.jsonl -mode loop
```

`bench run` with its default `-mode review` is the filter-friendly shortcut
for drafting: it writes the draft path to stdout, explains the next step on
stderr, returns 2 (pending admission), and never starts Ply. Use
`bench run -mode quick` when an immediate, non-negotiated Ply run is
actually intended. `bench run -mode loop -check COMMAND` still drafts and
exits 2; after inspection, `bench contract accept ... -mode loop` performs the
single bounded verifier-pursuit invocation.

| Stage | Existing program or boundary |
|---|---|
| Generate or revise | Ask structured output, composed with selected Brief skills |
| Inspect or edit | ordinary JSON plus `$VISUAL`/`$EDITOR` |
| Record and restore | sealed Ask notes and `ask replay -check` |
| Execute | unchanged Ply, after explicit admission only |
| Verify | the existing literal check, judge map, and Ply receipts |

Model choice follows the filters' existing convention. `-m provider/model`
overrides `ASK_MODEL` at startup; `/model provider/model` switches future Ask,
Ask + Ply, skill-refinement, draft-creation, and agent-build turns in the same
workbench. Ask records the model on every request, so switching mid-session is
replayable. `/model default` restores the startup choice.

## Open tasks with Ask + tools

The opening screen is a task composer, not an agent-requirements form. By
default, `enter` runs the structured contract turn in the same explicit
session and opens Contract Review. Only explicit admission then starts the
equivalent public process with the canonical admitted contract included in
`GOAL`:

```sh
ply -sh -C WORKSPACE -f SESSION [-m MODEL] [-s SKILL ...] [-steer FILE] [POLICY ...] -- GOAL
```

Bench starts both filters directly. The goal is one literal argv value; it is never
evaluated by Bench as shell text. Ply asks the model, consumes its first
complete shell block as one action, returns the real command result, and only
then asks again. Later blocks and premature claims from that model turn are
visibly deferred rather than executed. A turn with no action is the report
that stops the loop. This observation boundary is enforced by Ply, so even a
model that emits an imagined multi-step workflow must encounter real system
state between actions.

Bench renders Ply's typescript—including deferrals—as a live and durable
**WORK LOG** and its stdout as the **ASK** answer. Contract results and user
acceptance remain visible as **OUTCOME** cards. All are recoverable from the explicit
Ask session with `ask replay`.

`-sh` is a full-shell grant, not a sandbox, and the yellow **ASK + PLY · FULL
SHELL** label keeps that authority visible. Set `BENCH_TOOLS` to an ordinary
toolbox directory to replace `-sh` with `ply -t DIR`; the label then names that
toolbox. A toolbox limits program names but does not confine shell builtins or
redirection—use an operating-system sandbox when that boundary matters.

Ask seals prove log integrity and ordering, not process identity. In ambient
full-shell mode, automatic check-all completion assumes the finished worker and
its descendants are not malicious same-user processes rewriting the controller
session. For adversarial workers, use Draft's admitted May+Cage path with its
session outside Cage write roots; do not treat ambient replay as authenticated
producer proof.

Ply without `-check` exits zero when the model stops, not when an external
program proves the goal. On the direct compatibility path, the TUI therefore
says **Task stopped · no executable check**, never “done” or “passed.” A
contracted run instead becomes **Ready for review** and exits 2 while its
compiled criteria still await operator or admitted judgment. An open task may start Bench with a
literal shell-backed `-check COMMAND` or set one interactively with `/check --
COMMAND`; that exact argument is visible through `/check`, `/status`, and the
composer. Its zero exit status is recorded alongside the compiler's proposed
`check` coverage, but the contracted outcome remains **Ready for review** and
exits 2; neither model output nor skill text admits semantic coverage. Add
`-check-all` (or `/check all`) only for the operator's own blanket admission.
Bench seals that policy before Ply, verifies the exact accepted
`ply.verifier/v1` receipt after Ply, and releases stdout/exit 0 only after the
v2 result is durable. Bench
passes the command as one argv value and never evaluates it itself. Agent
designs remain the durable home for recurring checked work.

Both the interactive workbench and `bench run` accept `-contract=true|false`,
`-check COMMAND`, `-check-all`, `-approval off|every-action`, and Ply's optional policy controls: `-effort LEVEL`, `-cycles N`, `-turns N`, `-timeout DURATION`,
`-compact`, and `-compactions N`. Bench passes effort names literally through
Ply; Ask and its provider decide which names are supported. Omitted controls
stay omitted so the installed Ply owns its defaults; an explicit zero retains
Ply's documented unbounded meaning. Contracted compaction is rejected until
Bench can independently verify successor lineage; use `-contract=false` for
Ply's existing compaction behavior. On that direct path, checked or compacting
work reports the Ask session it actually used
through a private `-session-out` control artifact. With contract negotiation
enabled, the proposal turn has created the session, but Ply is not invoked
until explicit admission. With contracts disabled, absence on a
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
go test ./... 2>&1 | bench run 'fix the smallest root cause' >draft-path 2>contract.log
build.log | bench ask 'explain the first useful failure' | less
bench run -t .bench/tools -s go-review -f review.jsonl 'review this tree'
bench run -mode quick -check 'go test ./...' -turns 20 -timeout 90s -compact 'fix it'
bench contract run -C . -f .bench/sessions/fix.jsonl -mode loop -cycles 0 -turns 50
```

For `bench ask` and direct `bench run -mode quick`, piped bytes are stdin
evidence and the answer alone is stdout. A negotiated `bench run` instead
prints the durable draft path and exits 2 before Ply. Goals, model specs, skill names, and paths remain literal
argv values; Bench never evaluates them as shell syntax.

`/shell` temporarily gives the terminal to `$SHELL` in the workspace, then
restores the TUI when that shell exits. It is deliberately operator-controlled
and not inserted into model context or presented as Ask evidence. `ctrl+z` is
the lighter Unix job-control path. In Contract Review, `e` opens the current
`draft.json`, validates it on return, and never mutates an admitted revision.
In Design review, `e` opens `DESIGN.md` and automatically reruns `draft check`
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
