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
`.bench/subagents`; only the root synthesizes and writes in the shared tree.
The configured root check remains the literal verifier gate; in contracted
work it does not by itself admit semantic completion. Nested prompts do not
advertise further delegation.

## Non-negotiable boundary

The TUI does not become a model client, an agent runtime, or a shell
implementation. It composes native structured-output Ask turns to propose and
revise a durable contract before open work. Those turns receive the exact intent, configured verifier, piped evidence,
and a bounded read-only workspace inventory, and returns a small canonical
outcome contract. The contract contains no executable command. Selected Brief
skills are composed into the compiler policy and then passed again to Ply:
they shape domain procedure but never verdicts. The proposal is saved as
ordinary editable JSON and sealed as a typed proposal record. Natural-language
revision stays in Ask; manual revision stays in `$EDITOR`. Neither invokes
Ply. Only a literal user admission publishes an immutable revision and repeats
those exact bytes in the next user message to Ply. Every Ply verifier receipt names its digest, so Ask replay
checks request folds, event sequence, and the exact sealed record prefixes.
Autonomy is explicit product language: `quick` uses the direct-Ply seam for
immediate work without contract review, while `review` negotiates and admits the durable
contract first. `loop` uses that same admission and exactly one existing Ply
invocation, requiring a literal verifier; omitted cycles become Ply's
unbounded rejection budget and omitted turns become an explicit finite 50.
Explicit `auto` adds one read-only Ask structured-output classification before
dispatch and resolves to one of those concrete paths. Auto is never the
default and never reaches Ply, TaskOptions, draft, admission, or result
schemas. Fixed controller floors may only escalate its suggestion: broad
authority, Cage, every-action approval, invalid output, or an incompatible
check/turn policy select Review; Loop additionally requires a literal check
and finite turns. Semantic consequence detection remains Ask's delegated
classification: model-reported risk tags select Review, but are not a
deterministic safety boundary. The selected route is sealed as `bench.route/v1`
before downstream dispatch. It is an observation, not admission, approval,
criterion evidence, or completion authority.
It is invocation-scoped runtime policy, not a daemon, retry supervisor,
crash-resume promise, new event authority, or second tool loop. `/contract off` and `-contract=false` remain compatibility
aliases rather than the primary mental model.

`eval/auto` is an offline controller regression over a frozen reviewed synthetic proposal
snapshot bound to the exact router System, prompt-template, and Schema
digests. It checks controller parsing,
floors, and 60 reviewed routing cases without network, model, shell, or worker
execution. It must not be presented as a score for a current live model or as
an outcome-quality A/B test.

The developer-only `bench-auto-live` command tests the next boundary without
changing the product protocol. It invokes the public Bench, Ask, and Ply
executables against fresh fixture copies, records a Ply wrapper trace outside
the worker, and relies on external artifact oracles. Contract admission stays
a literal human action: preparation emits inspectable one-shot scripts for
safe drafts and no script for consequential traps. Its eight cases establish
only live process integration and external outcome-oracle parity, not a general model
score, telemetry stream, or automated acceptance authority. An optional
hash-bound `-action-shell` snapshot composes Ply's action-only interpreter with
the experiment through the literal public option on every worker invocation,
while `PLY_ACTION_SHELL` remains available to nested Ply and host checks retain
`/bin/sh`. The mechanism is optional for other case manifests but required by
the committed corpus. The checked `l02` host verifier copies the workspace to a
disposable probe and delegates only its fixed behavioral test through that bound
interpreter; it rejects probe mutation and deletes the copy. The full behavior
is stated in the intent, failures are diagnostic, and equivalent implementations pass. This
allows an operator adapter that exposes only the workspace and toolbox to keep
the external oracle out of the worker and prevents the host scorer from directly
executing candidate code, without treating the adapter as contract confinement.

The TUI can yield the terminal to the operator's `$SHELL` or
`$EDITOR`, then restore itself. Its
default task work phase executes the equivalent of:

```sh
ply -sh -C WORKSPACE -f SESSION [-m MODEL] [-s SKILL...] [-steer FILE] [POLICY...] -- GOAL
```

With a `BENCH_TOOLS` directory, `-t DIR` replaces `-sh`; selected procedures
are repeated `-s SKILL` arguments. Ply owns the Ask→command→result loop and
records it in the explicit Ask session. That boundary consumes one complete
shell block per model turn, returns its real result, and visibly defers any
later actions or claims written before the result existed. Bench neither
reimplements nor weakens that rule: it renders stderr as visible tool evidence
and stdout as the answer. Because an unchecked Ply exit zero means only that
the model stopped, the UI never calls that outcome done or passed.
For an interactive Loop invocation, Bench creates one mode-0600 regular file
beside the session and passes its path literally through Ply's public
`-steer` option. The TUI appends complete UTF-8 guidance lines with ordinary
`O_APPEND` writes. Ply reads newly appended lines immediately before an Ask
turn and records them as part of that ordinary user message. It never treats
them as contract amendments, approval grants, tool changes, or verifier
changes. No socket, queue daemon, private command language, or Bench-owned
turn protocol is introduced.
`-check COMMAND` deliberately opts an open task into a shell-backed verifier;
Bench passes that command as one literal argument and displays it through
`/status`. A contract criterion uses judge `check` only to propose that the
exact verifier directly establishes it. After Ply's factual verifier receipt,
Bench mechanically seals `bench.contract-result/v1`. Model-assigned coverage
is never authority: contracted work remains review-required and exits 2 until
the interactive user explicitly accepts every pending criterion. The compiler
also treats the verifier command as opaque: its path and arguments do
not establish which tests, assertions, coverage, or output it contains. Unless
separate read-only operator evidence says more, generated evidence may name
only the exact passing verifier. An ordinary check assigns no criterion
coverage or completion authority; its model-proposed labels remain pending.
The separate operator-only `-check-all` policy binds the exact check into the contract
envelope, seals every parsed criterion ID in `bench.judge-map/v1` before Ply,
then requires a strictly matching sealed Ply receipt before a durable
Review `bench.contract-result/v2` or Loop `bench.contract-result/v3` may say
complete with the same judge-map and receipt bindings. `/continue`
instead begins a revision with Ply's public `-B` behavior so a retained passing
check cannot suppress requested work. Bench does
not infer authority from shell text, skills, or model prose. `-effort`, `-cycles`,
`-turns`, `-timeout`, `-compact`, and
`-compactions` are optional process policy, not a second loop, and omission
leaves Ply's defaults in charge. Contracted compaction is rejected until
successor lineage can be independently verified; direct `-contract=false`
work retains Ply's existing compaction behavior.
Loop's descriptive pursuit, budget, and terminal-reason fields are sealed in
`bench.contract-result/v3`; the existing Review result v1/v2 bodies remain
unchanged.

Consequential open questions and ungranted approvals stop before Ply receives
full-shell authority. With approval policy off, the user's answer authorizes
the described consequential scope; there is no later execution gate. With
every-action, it authorizes preparation only and May separately grants exact
action bytes. The user's answer is composed with the full original
intent and exact pending items into the replacement contract request; it never
becomes a smaller stand-alone goal.

The optional operator-owned `every-action` policy is stored in the editable
draft and admitted revision. It composes the existing filters rather than
inventing an action protocol in Bench: Ply sends the exact shell-action
envelope to `may request`, verifies May's result and exit status, seals
`ply.approval/v1`, and only a spent single-use grant permits the unchanged
Runner action. Parked and declined actions stop before another model turn or
the verifier. Bench verifies that terminal receipt through `ask replay
-check -json` and seals `bench.contract-result/v4`. `/approval decide` yields
the terminal to `may decide`; Bench itself cannot grant approval. Quick cannot
use this durable policy. The optional admitted Cage policy composes at that
same action seam: caged actions use `ply.approval/v2`, whose exact May envelope
binds Cage path, digest, argv, physical workspace, private temporary directory,
and denied-network policy. Ask, Brief, May, Bench, and the verifier remain
ordinary processes outside Cage. Exit 125 is reserved; Ply seals
`ply.confinement/v1`, Bench seals result v5, and no later model turn or verifier
runs. Controller state must live under an absolute external `BENCH_DIR`,
outside every Cage-writable root.

On the direct compatibility path, compaction may move the work into a successor
Ask session. For checked or compacting work, Bench passes Ply a private
`-session-out FILE` artifact path.
Ply atomically records the absolute current session before model work and after
each successful transition; a passing pre-check creates neither session nor
artifact. Bench reads it only with the terminal process event, removes it, and
makes a reported session authoritative for later interactive turns. Control
data therefore never competes with answer stdout or typescript stderr.

The full-shell grant is visible at all times. A toolbox is an executable-name
grant, not confinement; operating-system sandboxing remains a separate
composition rather than a misleading TUI boolean.

Ask replay proves session integrity and ordering, not truth or producer
identity. Ambient full-shell check-all therefore assumes a non-malicious
same-user worker. Adversarial execution needs Draft's admitted May+Cage
boundary with controller evidence outside Cage write roots.

The same seam is available without a TUI. `bench contract draft`, `revise`,
`show`, `edit`, `import`, `accept`, and `run` use the same contractexec and file-store APIs as the
Contract Review screen. Default `bench run` creates a proposal and exits 2
without Ply; `bench run -mode quick` preserves the direct filter seam.
`bench ask` remains direct Ask. `bench tui` is the explicit presentation
override; there is no TUI-only contract engine.

## Distribution is a pinned composition

The source projects remain independent filters, but the end-user product is a
suite release containing `bench`, `ask`, `brief`, `ply`, `context`, `action`,
`cite`, `may`, `cage`, `hone`, `trail`, `agent`, `tend`, and Draft's script and skill
at exact revisions. `internal/suite/manifest.json` is the
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

## Design projects and running agent homes have distinct formats

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

A built standing worker is a different lifecycle object from its build
design. Its experimental runtime form is the sibling `agent` filter's ordinary
agent home: `AGENTS.md`, `GOAL.md`, optional `SOUL.md`, `HEARTBEAT.md`, and
`MEMORY.md`, Brief skills, nested specialist homes, a toolbox, mutable
`work/` and `state/`, executable `bin/check`/`bin/wake`, and controller-owned
`.agent/runs/`. `agent show/check/run/tick/specialist/learn/history/amend`
compile that directory through Brief, Ply, Ask, Cage, Hone, Trail, and May.
The complete boundary and research are in
`FILESYSTEM_AGENTS.md`. Draft remains how the system is designed and proved;
the agent home is how an already-built digital worker is tuned and run.
Headless `bench home [-C DIR] COMMAND...` is an attached process boundary over
those public verbs: it supplies the selected companions but preserves
literal argv, stdin, stdout, stderr, and exit status. Interactive `/agent`
retains its existing and distinct Draft-promotion meaning.
Interactive `bench -C PARENT -home NAME` starts from public `agent show` and
offers only direct public operations: refresh, offline check, default-confined
goal run, heartbeat tick, Trail history list, Ask-backed history check, and
bounded read-only proposal review through `agent proposals`. A structured
specialist form collects one child name and bounded task, then invokes public
`agent specialist`: the name and selected model remain literal argv, the task
uses attached stdin, and the child owns every home and execution rule. A
separate structured form invokes `agent learn -why` for one destination skill
and home session; it shows only replay-verified recovery evidence and cannot
call a model or write the skill. Three adjacent exact-learning actions invoke
public Agent commands without importing the learning envelope: prepare one
user-named proposal from a destination and verified session, reopen its literal
proposed `SKILL.md` bytes read-only, then replay-check and admit those exact
bytes without another model call. A successful admission invalidates the old
compiled home until refresh. The amendment form passes one literal reviewed
patch name to public `agent amend`: exit 75 remains an approval-parked verdict, exit
3 remains a decline, and only an identical retry after May grants the exact
action can apply and receipt the definition change.
The UI renders stdout as result data, stderr as evidence, and exit status as
the verdict; it does not parse session internals or treat displayed prose as
success. Authority-widening flags remain headless; definition-changing
learning is exposed only through Hone's exact reviewed-artifact interaction.

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
13. **In progress:** validate the experimental sibling `agent` filter in real
    homes, expose every public verb through headless `bench home`, and provide
    a dedicated interactive view for `show/check/run/tick/history`. Headless
    specialist runs, explicit learning, and exact May-gated definition
    amendments are implemented. The TUI now has bounded read-only proposal
    review, a structured direct-specialist form, model-free verified
    learning-evidence review, and exact prepare/show/admit learning interactions.
    The TUI also submits one literal reviewed patch through Agent's exact
    May-gated amendment path. Neither flow imports Agent's parser, loop,
    scheduler, evidence, approval, or confinement policy.

At every stage, deleting the TUI must leave a usable directory of files and
commands.
