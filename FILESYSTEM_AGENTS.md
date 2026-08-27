# Filesystem agents for Bench

Research snapshot: 27 August 2026.

## The opportunity

An agent can be a directory.

Not a serialized framework object, a database row, or a provider-specific
assistant. A directory is already editable, diffable, forkable, searchable,
backed up, permissioned, and understood by every programming environment. A
small set of well-known Markdown files can say who the agent is, what it is
trying to accomplish, how it should work, and what it has learned. Ordinary
subdirectories can hold its skills, specialist agents, tools, mutable state,
work, and run evidence.

The important product is not the file names. It is the ability to point at a
folder and say:

```sh
agent show support-chief
agent check support-chief
agent run support-chief
```

`show` makes the exact assembled agent inspectable, `check` refuses an
ambiguous or unverifiable home, and `run` composes the existing Bench filters.
The folder is the source; the run is a reproducible build from that source.

This is the missing object between a `brief` skill and a `draft` project:

- a skill is one reusable procedure selected for a task;
- a Draft project is a design for a system that still has to be built;
- an **agent home** is a built digital worker: standing context, a durable
  objective, scoped capabilities, mutable work, and evidence from every run.

## What other systems have converged on

The convergence is real even though the products use different names.

### OpenClaw: the workspace is the agent

OpenClaw gives each configured agent its own workspace and injects well-known
bootstrap files. `AGENTS.md` carries operating instructions, `SOUL.md` the
persona and boundaries, `IDENTITY.md` the public identity, `USER.md` the user
profile, and `MEMORY.md` curated long-term memory. Its workspace also holds
daily memory logs and workspace-local skills. The documentation explicitly
calls the workspace the agent's home and recommends keeping it in a private
Git repository.

That makes the idea tangible, but two details are more valuable than the
happy path:

- the workspace is only a current working directory unless a separate sandbox
  is enabled; a folder is not an authority boundary;
- `HEARTBEAT.md` became legacy configuration and current heartbeat state moved
  to scheduler-owned scratch, because a wake-up cadence and an agent prompt
  are different kinds of state.

Sources: [agent runtime](https://github.com/openclaw/openclaw/blob/main/docs/concepts/agent.md),
[agent workspace](https://docs.openclaw.ai/agent-workspace), and
[heartbeat behavior](https://github.com/openclaw/openclaw/blob/main/docs/start/openclaw.md).

### Hermes: goals, memory, skills, and scheduling are separate mechanisms

Hermes discovers project context files, keeps bounded `MEMORY.md` and
`USER.md` snapshots, uses Agent Skills for procedural memory, delegates work
to isolated child contexts, and runs scheduled work in fresh sessions.
Its persistent-goal loop is especially relevant: a standing goal survives
turns, a bounded continuation loop keeps working, and deterministic quality
gates can prevent a model judge from declaring success prematurely.

Hermes also has the best current version of an idle-run optimization. A cron
pre-check can emit `wakeAgent: false`, so frequent polling can cost no model
call when external state has not changed. Its own documentation distinguishes:

- a goal loop, which is completion-driven in one session;
- a heartbeat or loop, which is cadence-driven in one session;
- cron, which is durable scheduling with a fresh session per run;
- a task board, which is durable multi-worker coordination.

Those distinctions should survive in Bench. They are different control
planes, not four spellings of “keep going.”

Sources: [feature overview](https://hermes-agent.nousresearch.com/docs/user-guide/features/overview),
[persistent goals](https://hermes-agent.nousresearch.com/docs/user-guide/features/goals),
[scheduled tasks](https://hermes-agent.nousresearch.com/docs/user-guide/features/cron),
[persistent memory](https://hermes-agent.nousresearch.com/docs/user-guide/features/memory),
and [subagent delegation](https://hermes-agent.nousresearch.com/docs/user-guide/features/delegation).

### Claude Code: one extension mechanism per loading and authority need

Claude Code now presents its filesystem features as a set of distinct
extension points:

- `CLAUDE.md` is always-on project context;
- skills are progressively disclosed procedures;
- subagents have isolated context and return summaries;
- hooks run deterministic automation at lifecycle boundaries;
- MCP supplies external capabilities;
- plugins are the distribution unit.

Its custom-agent files can name tools, preload skills, choose models, attach
hooks, and give an agent a persistent memory directory. The documentation also
adds explicit trust handling for executable configuration found in a folder.
The lesson is not that Bench should copy all of those fields. It is that
instructions, procedures, workers, triggers, capabilities, and memory have
different loading and authority semantics even when Markdown is used to
author several of them.

Sources: [extension overview](https://code.claude.com/docs/en/features-overview)
and [custom subagents](https://code.claude.com/docs/en/sub-agents).

### Codex: the filesystem is layered context, not hidden runtime state

Official OpenAI documentation says Codex constructs an instruction chain from
`AGENTS.md` files, from broad project rules down to the current directory,
with bounded input and closer files taking precedence. Skills are directories
with progressively disclosed `SKILL.md` instructions and optional resources.
Custom subagents are Markdown-defined focused contexts. Local memories are
stored as inspectable generated files, but OpenAI explicitly recommends that
required rules remain in `AGENTS.md` or checked-in documentation rather than
depending on the recall layer.

Codex scheduled tasks reinforce the control-plane distinction: a task can run
in a local project or isolated worktree, while the schedule belongs to the
desktop task manager rather than to `AGENTS.md`. The folder supplies context;
the runner supplies cadence, isolation, and authority.

Sources: official OpenAI documentation for
[AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[skills](https://learn.chatgpt.com/docs/build-skills),
[subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents),
[memories](https://learn.chatgpt.com/docs/customization/memories), and
[scheduled tasks](https://learn.chatgpt.com/docs/automations).

### Pi: keep the harness small and make the filesystem extensible

Pi is the useful counterweight to a feature-complete runtime. It loads
`AGENTS.md`, lets `SYSTEM.md` replace or append to its prompt, discovers skills
and prompt templates, and exposes lifecycle events to TypeScript extensions.
It deliberately does not bake in a single plan mode, subagent design,
permission flow, or memory system. Packages distribute extensions, skills,
prompts, and themes.

That ecosystem demonstrates both the power and the cost of a small core. A
minimal harness can support thousands of experiments, but the user must still
decide which goal loop, supervisor, memory package, subagent package, and
permission system compose safely. Bench should keep its runtime compositional
without making every user assemble the product boundary from scratch.

Sources: [Pi overview](https://pi.dev/) and
[extension documentation](https://pi.dev/docs/latest/extensions).

## The common architecture

Across these systems, six concepts keep reappearing:

| concept | filesystem expression | runtime responsibility |
| --- | --- | --- |
| standing instructions | `AGENTS.md`, `CLAUDE.md`, `SOUL.md` | discover, bound, and show exact input |
| on-demand expertise | `skills/*/SKILL.md` | select progressively and record what loaded |
| specialist workers | `agents/*.md` or child directories | isolate context, cap fan-out, retain evidence |
| durable state | memory files, plan files, key-value data | separate authored facts from generated state |
| triggers | heartbeat, hook, cron, event | schedule outside the model turn; wake cheaply |
| run history | session files, logs, task rows | append evidence and make it replayable |

The folder is becoming an agent ABI. Markdown is winning because a person and
a model can both edit it, while the surrounding filesystem supplies naming,
hierarchy, shadowing, permissions, version control, and composition.

## Where current implementations go wrong

“Everything is a file” is not enough. A useful design has to say what kind of
file each one is.

### Instructions are mistaken for authority

`SOUL.md` can say “never publish without asking,” but it cannot prevent a
publish command. `TOOLS.md` can describe a narrow tool set, but it does not
remove any executable from the process. A downloaded agent folder must never
be able to grant itself network access, broader filesystem writes, approval,
credentials, a more expensive model, or more continuation budget.

Markdown may shape behavior. Only the runner and operator grant authority.

### The worker can rewrite its own constitution and judge

If the workspace, instructions, memory, and completion check share one
writable root, an agent can accidentally or deliberately edit the rule that
was supposed to constrain it or the check that was supposed to judge it. A
passing check then proves only that the worker found a writable opinion.

The definition, mutable work, controller state, and verifier must be separate
write domains even when they appear beneath one convenient top-level folder.

### Memory becomes an append-only junk drawer

The obvious learning loop—summarize every run and append it to memory—is known
to produce more confident errors. Bench already has the stronger boundary:
`hone` admits procedural lessons only from replayable runs that first failed
and later passed an executable check. An agent home should not create a second,
weaker automatic-learning path.

`MEMORY.md` should be small, curated factual context. Verified procedural
learning belongs in Agent Skills through `hone`. Raw history belongs in Ask
sessions and is searched by `trail`; it is not copied into memory.

### Plans, goals, and transcripts collapse into one chat

A goal is the stable outcome. A plan is a revisable approach. State is the
world as it currently exists. A transcript is evidence of what happened.
When all four live only in one conversation, compaction silently changes the
first two and process death loses the third.

They need ordinary, separately inspectable artifacts.

### Heartbeats generate expensive busywork

A timer should not wake a model merely to discover that nothing changed. The
deterministic probe runs first. Exit zero means quiet; exit one supplies the
changed state to an agent turn. Scheduling is then an ordinary cron, desktop
automation, CI event, webhook, or service concern.

### A model grades its own completion

An LLM judge can help decide whether to continue, but it cannot be the final
authority for a claim that a program can check. Bench already has the needed
contract: `ply -check` runs before work and after candidate completion, records
a receipt, and only exit zero accepts. A folder agent should inherit that
property unchanged.

## The Bench-native model: an agent home

An agent home is one portable directory with four planes:

```text
support-chief/
  # Definition: human-authored, versioned, read-only to a confined worker
  AGENTS.md
  GOAL.md
  SOUL.md
  PLAN.md
  HEARTBEAT.md
  MEMORY.md
  skills/
    triage/SKILL.md
  agents/
    researcher/...
    writer/...
  tools/
  bin/
    check
    wake

  # Mutable data: writable to the worker when explicitly granted
  work/
  state/
    plan.md
    kv/

  # Controller evidence: never writable to the worker
  .agent/
    runs/
    learning/
    contracts/
    receipts/
```

Only `AGENTS.md`, `GOAL.md`, `work/`, and an executable `bin/check` are needed
for a goal-driven home. Every other entry is optional and progressively adds
one concept.

### Definition plane

`AGENTS.md`
: The constitution: operating rules, priorities, escalation conditions, and
  how the agent should use the other files. Always loaded. It grants no
  authority.

`GOAL.md`
: The durable outcome, acceptance evidence, constraints, scope, and stop
  conditions. It is user-authored task input, not a system instruction. The
  executable truth remains `bin/check`.

`SOUL.md`
: Voice, temperament, values, and relationship style. It affects how work is
  done and reported, never which actions are allowed.

`PLAN.md`
: The standing strategy a person wants to review and tune. Live progress goes
  in `state/plan.md`, so a run can update its approach without rewriting the
  admitted definition.

`HEARTBEAT.md`
: What to examine and do when an external scheduler asks for a tick. It does
  not contain or create its own schedule. `bin/wake` decides whether a tick
  needs a model at all.

`MEMORY.md`
: Small, curated, durable facts that should be present on every run. It is
  read-only to a confined worker and never automatically rewritten. Required
  rules belong in `AGENTS.md`; repeatable procedures and verified learning
  belong in `skills/`.

`skills/`
: Ordinary Agent Skills. The v1 runner scopes `BRIEF_PATH` to this directory,
  so an agent does not silently acquire ambient procedures from the host;
  `brief` retains progressive disclosure and lint authority. A later
  caller-owned flag can add an inspected shared catalogue without letting
  Markdown widen the source set.

`agents/`
: Nested agent homes, not a second subagent schema. Running a specialist is
  another `agent run PATH` process with its own context, work, state, check,
  and Ask session. The parent gets a bounded catalogue and the child's final
  summary; the child evidence remains separately replayable.

`tools/`
: Agent-specific programs. As with Ply, a tool is a program and the directory
  is the catalogue. Presence in the home does not automatically grant use;
  the invocation chooses toolbox-only or full-shell authority.

`bin/check`
: The completion verdict. It runs from `work/`, outside the model-action
  sandbox, with the candidate final report on stdin. It must be operator-owned
  or admitted outside the worker's writable roots.

`bin/wake`
: The cheap heartbeat probe. Exit zero means no work and no model call. Exit
  one means the heartbeat goal should run, and its output is the first evidence
  the model receives. Any other status means the probe is broken.

### Mutable plane

`work/` is the agent's ordinary working directory and deliverable surface.
It is the default current directory for tools. `state/` holds durable machine
state that should survive runs but is not prompt context by default.

The key-value store is intentionally a directory, not an embedded database or
new API:

```text
state/kv/last-ticket
state/kv/last-successful-sync
state/kv/customer/acme/status
```

A value is a file. Atomic replacement is the write protocol. Programs can use
SQLite inside `state/` when they actually need transactions or queries; the
agent runner does not pretend a flat file and a database are the same thing.

### Evidence plane

`.agent/` is controller state. A model action may know that it exists but may
not write it. Ask sessions remain the authoritative transcript, Ply verifier
receipts remain the completion evidence, and admitted Bench contracts remain
the operator-reviewed outcome definition. `trail` reads session archives;
the new tool does not create a second log or replay implementation.

## Exact composition with the existing system

The proposed `agent` command has no provider adapter, agent loop, skill
catalogue, memory index, verifier engine, approval UI, sandbox, scheduler, or
session format. Each already exists.

```text
agent home files ──compile──> exact run inputs
       │
       ├─ AGENTS/SOUL/MEMORY ───────────────┐
       ├─ skills/ ───────────────> brief ───┼─> ply ─> ask session
       ├─ tools/ ───────────────────────────┤     │
       ├─ GOAL.md + invocation input ───────┤     ├─> bin/check receipt
       ├─ work/ + state/ ────────> cage ────┘     └─> stdout answer
       └─ agents/* ─────────> child agent processes

human approval ─> may        verified recovery ─> hone ─> skills/
session browse ─> trail      project guidance ─> rules
design/build ───> draft      interactive control plane ─> bench
```

The mechanical mapping is:

```sh
# illustrative, not the implementation: values remain literal argv/env
BRIEF_PATH="$HOME/skills" \
PLY_DIR="$HOME/.agent/runs" \
ply -C "$HOME/work" -t "$HOME/tools" -s AGENT_CONTEXT -s - \
  -check "$HOME/bin/check" <COMPOSED_GOAL
```

`AGENT_CONTEXT` is a private, temporary Brief-compatible value assembled from
the bounded context files. Passing it through Ply's existing `-s` seam keeps
private context out of process argv; the exact resulting system prompt still
lands in the Ask session. The temporary value is removed after Ply has loaded
it. Agent-local skills are then chosen through the normal `brief find` path.

The first implementation should call public binaries. It must not import
their packages or parse Ask events.

## Command contract

```text
agent new DIR [description ...]     scaffold an inspectable home
agent check [DIR]                   validate the complete home
agent show [DIR]                    print exact context and run wiring
agent run [flags] [DIR] [-- input]  pursue GOAL.md until bin/check accepts
agent tick [flags] [DIR]            use bin/wake before HEARTBEAT.md work
agent specialist PARENT NAME [flags] run one direct child home and return stdout
agent learn -into SKILL DIR SESSION  offer a verified recovery to hone
```

The Unix result contract should match the family:

| status | meaning |
| --- | --- |
| 0 | valid, no heartbeat work, or the executable check accepted |
| 1 | invalid home or heartbeat probe says work is pending but was not run |
| 2 | broken invocation or dependency |
| 3 / 75 | May declined / parked an exact action |
| 125 | confinement could not be established |

`show` is load-bearing. It prints:

- every context file in load order with its path and byte count;
- the selected workspace, state, toolbox, skill path, verifier, and evidence
  directory;
- exact hashes for the definition and check;
- the authority supplied by this invocation, including network, approval,
  and confinement;
- the specialist catalogue and any validation warnings.

It never prints secrets, run state values, or an inferred promise that a tool
is safe. It is the equivalent of `cc -###`: the compiled agent before it runs.

## Running safely

An agent home is code. Opening one should be harmless; running one should make
the authority obvious.

### Default boundary

The strongest useful default is:

- definition and `.agent/` read-only;
- only `work/`, `state/`, and a private temporary directory writable;
- network denied;
- agent-specific toolbox visible, with broader shell access an explicit
  invocation choice;
- multiply-linked regular files refused in writable roots, because hard links
  alias inodes beyond pathname policy;
- `bin/check` executed by the controller, outside the action boundary;
- sessions and receipts outside every worker-writable root.

`cage -w work -w state` already expresses the filesystem boundary. Broader
network, host writes, credentials, or full-shell reach are operator flags and
must never be inferred from Markdown. Consequential actions compose through
May exactly as they do in Bench and Ply.

Cage is a write and network boundary, not a confidentiality boundary. A full
shell action can read files, environment values, credentials, and programs
available to its operating-system identity. Run the controller under a narrow
identity and scrub its environment; use a container or virtual machine when
host reads or secrets must be isolated. The raw Cage adapter also owns Cage's
documented hard-link preflight and must refuse unsafe writable roots before
every action.

### Definition changes are amendments

If the agent proposes a change to `AGENTS.md`, `SOUL.md`, `GOAL.md`,
`HEARTBEAT.md`, `MEMORY.md`, a skill, a specialist, or `bin/check`, it writes a
patch under `work/proposals/`. A person reviews and applies it outside the run.
That keeps self-improvement transparent without letting one execution rewrite
the constitution or judge that governs the next one.

### A folder never carries ambient secrets

Credentials remain in Vouch, an outer isolated identity, or connected tool
configuration. They are provided to a narrow program at execution time. They
never live in Markdown, `state/`, the toolbox, or inherited environment merely
because an agent says it needs them. Cage alone does not hide ambient
credentials already readable by the worker identity.

## Goal, plan, heartbeat, and schedule

These four often get conflated, so their lifecycle is explicit:

| artifact | changes when | decides |
| --- | --- | --- |
| `GOAL.md` | a person changes the desired outcome | what the worker pursues |
| `PLAN.md` | a person changes the standing strategy | how runs should begin |
| `state/plan.md` | the worker records current progress | where the next run resumes |
| `HEARTBEAT.md` | a person changes recurring watch behavior | what a due tick should examine |
| external schedule | an operator changes cadence/event source | when `agent tick` is invoked |
| `bin/check` | an operator changes completion truth | whether goal work is done |
| `bin/wake` | an operator changes the cheap trigger | whether a tick spends a model call |

This yields three useful invocation shapes without a daemon:

```sh
agent run chief                         # keep pursuing the standing goal
agent tick chief                        # quiet when nothing changed
0 * * * * /usr/local/bin/agent tick /srv/chief
```

The same home can later be scheduled by ChatGPT, launchd, systemd, CI, a
webhook receiver, or a desktop app. Portability comes from keeping cadence out
of the home and making `tick` idempotent.

## Subagents without a team framework

A specialist is another home because recursion is the smallest complete
abstraction:

```text
agents/researcher/
  AGENTS.md
  GOAL.md
  skills/
  tools/
  bin/check
  work/
  state/
```

The root sees only a bounded catalogue at startup. An external controller can
invoke a direct child as an ordinary foreground process with a concrete,
bounded task. The child receives no parent's chat history, mutable state,
loaded skills, check, or authority by accident. Its summary returns on stdout;
its full typescript and verifier receipt remain in its own evidence directory.
This explicit controller seam avoids pretending a provider-calling child can
escape a network-denied parent Cage or write controller evidence through it.

The first version should retain Bench's current flat fan-out rules: at most
three independent read-heavy workers, one writer per worktree, stable task
order, visible failures, and root synthesis. Nested delegation stays bounded
by the process depth counter. A specialist definition may narrow inherited
authority but cannot widen it.

## Memory and learning

The home exposes three different things instead of naming all of them memory:

1. `MEMORY.md`: a small, reviewed set of durable facts always in context.
2. `state/`: current machine state read on demand, never injected wholesale.
3. `skills/`: procedures selected progressively, including verified lessons
   admitted by `hone`.

Run history remains in Ask sessions. `trail find` is recall over that history;
copying session summaries into `MEMORY.md` would create a stale second record.

`agent learn -into NAME HOME SESSION` should only compose:

```sh
BRIEF_PATH="$HOME/skills:$BRIEF_PATH" hone -into NAME SESSION
```

It must preserve Hone's fail-then-pass gate and explicit invocation. There is
no automatic end-of-run memory write, consolidation pass, embedding store, or
background self-improvement daemon.

## Why this fits Bench better than a new framework

Bench already has every hard mechanism:

- Ask owns models and replayable conversations.
- Rules owns bounded repository instructions.
- Brief owns progressive procedures.
- Ply owns action/check iteration.
- Hone owns verified procedural learning.
- Trail owns read-only session discovery.
- May owns exact human approval.
- Cage owns process confinement.
- Draft owns system design and executable definitions of done.
- Bench owns the interactive control plane and contract admission.

The new tool has one job: **compile an agent home into those public process
boundaries and make the exact composition inspectable.**

That is small enough to trust and large enough to feel like a product.

## The golden moment

The revolution is not “agents configured with Markdown.” It is this:

> An agent becomes a versioned directory whose authored context, mutable
> world, executable truth, granted authority, and run evidence are separate,
> inspectable things.

That turns agent building into ordinary software work:

- edit a file to tune behavior;
- diff two agents to understand why they differ;
- fork a folder to create a specialist;
- run `check` before spending tokens;
- review a proposed constitution change as a patch;
- replay the exact run that produced a claim;
- admit a verifier and keep it outside the worker's write boundary;
- learn only from a verified recovery;
- schedule the same idempotent entry point anywhere.

The directory is not merely storage. It is the linking format for the whole
Bench family. Markdown supplies judgment-shaping source, programs supply
capability and truth, and the filesystem supplies composition and provenance.

## Delivery slices

Prototype status on 27 August 2026: the sibling `agent` repository implements
Slice 1, the cheap wake gate from Slice 2, direct foreground specialist runs
from Slice 3, and explicit Hone admission from Slice 4. Its offline process-boundary
suite covers scaffold, validation, composition, private-context transport,
authority flags, heartbeat behavior, specialist-home catalog validation,
hard-link refusal, and exit preservation. A live disposable home also ran
red-to-green through installed Brief, Ply, Ask, and Cage; its accepted Ask
session replayed exactly, definition/evidence write probes were denied, and a
later quiet tick created no new session. Suite packaging, amendment review,
history browsing, and the Bench view remain future slices.

The local `agent` repository is now committed and installs as
`~/.local/bin/agent`. Benchpack's installer derives commands from the manifest,
accepts a single executable companion asset, and the agent resolves that
companion through its real path when invoked by an installation symlink. The
remaining suite step is intentionally external: publish the independent
repository, then pin that fetchable commit in `internal/suite/manifest.json`.
The release manifest is not pointed at an unpublished or mutable source.

### Slice 1: compile and run one home

Build `new`, `check`, `show`, and `run` for the required core:
`AGENTS.md`, `GOAL.md`, `work/`, `state/`, `skills/`, `tools/`, `bin/check`,
and `.agent/runs`. Compose public `brief`, `ply`, `ask`, and `cage` binaries.
Tests use fake filters and make no model or network call.

The acceptance demonstration is one tiny home whose check starts red, whose
worker changes only `work/`, and whose run ends zero with a replayable verifier
receipt while attempts to edit `AGENTS.md` and `.agent/` fail.

### Slice 2: cheap heartbeats

Add `HEARTBEAT.md`, `bin/wake`, and `tick`. Prove that a quiet tick creates no
Ask session and that a changed-state tick supplies the probe output as initial
evidence. Keep scheduling external.

### Slice 3: recursive specialists

Add the bounded `agents/` catalogue and child invocation. Prove separate
sessions, inherited-or-narrower authority, depth limits, stable synthesis
order, and one-writer behavior. Do not add a task database or daemon.

### Slice 4: review, learning, and Bench UI

Compose May for definition amendments and consequential actions, Hone for
verified learning, Trail for run history, and Bench for the visual `show`,
run, evidence, approval, and proposal-review journey. Keep every headless
command independently useful.

### Slice 5: portability and evaluation

Add an import assistant for OpenClaw, Hermes, Claude Code, Codex, and Pi
folders. Import copies and maps authored context; it never imports credentials,
ambient permissions, opaque scheduler state, or unverified memory silently.

Publish a frozen corpus of representative homes and measure:

- definition bytes loaded at startup;
- skill-selection precision and context saved by progressive disclosure;
- zero-model quiet-tick rate;
- verifier acceptance and false-success rate;
- unauthorized definition/controller write attempts blocked;
- run replay success;
- lessons admitted only from verified recoveries;
- clean re-entry when work is already done.

## Decisions for the first implementation

- The product term is **agent home**. The provisional executable is `agent`
  because `agent run DIR` says exactly what happens; rename it only if a real
  collision appears.
- Do not add `agent.yaml`, a registry, or a database in v1.
- Require an executable `bin/check`; prose acceptance criteria alone cannot
  produce completion.
- Keep definition files read-only during confined work.
- Keep schedule and authority outside Markdown.
- Treat `MEMORY.md` as curated facts, not automatic learning.
- Reuse Agent Skills unchanged.
- Make nested agent homes the subagent format.
- Make `show` and offline `check` excellent before adding a daemon, board, or
  marketplace.

## Open questions to test, not debate abstractly

1. Should `GOAL.md` be required, or should a one-shot invocation be allowed to
   supply the entire goal while the home supplies only worker identity?
2. Is a root `PLAN.md` useful once `GOAL.md` and mutable `state/plan.md` are
   distinct, or does it become stale duplication?
3. Should a home default to toolbox-only authority, or to full shell inside
   Cage with network denied? Measure task success and surprise for both.
4. Can a useful subagent catalogue be derived from bounded `AGENTS.md`
   headings, or does each child need a small standard metadata file?
5. Should an agent-local verified-memory skill always load, or should Brief
   select it like every other procedure? Compare retrieval precision and
   context cost.
6. Which definition changes are common enough to deserve a first-class
   proposal view in Bench rather than an ordinary patch under `work/`?

None of these blocks Slice 1. The first vertical slice produces the evidence
needed to answer them.
