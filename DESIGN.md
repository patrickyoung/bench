# bench design

`bench` is a terminal workbench for building agents out of Unix filters.
The TUI is the glass; the filters remain the machine.

## The product loop

An agent project grows through five mechanically distinct stages:

```
requirements -> design -> build -> evaluate -> learn
       ask        draft      ply       draft     hone
                            brief      suites    brief
```

Each arrow is an artifact or a process boundary, not an in-memory framework
call. A user can begin with a few requirements, inspect everything the model
said and every command that ran, and keep adding checks and capabilities
without changing runtimes.

The first vertical slice was `ask`: a polished, resumable conversation backed
by one explicit append-only ask session. The same boundary now carries the
project through Design, Build, Prove, and Learn.

## Non-negotiable boundary

The TUI does not become a model client, an agent runtime, or a shell. For an
unskilled turn it executes the equivalent of:

```sh
ask -f .bench/sessions/SESSION.jsonl -- "$message"
```

With explicit skills selected, the seam is still ordinary composition:

```sh
ask -S "$(ask system; brief cat SKILL...)" \
  -f .bench/sessions/SESSION.jsonl -- "$message"
```

It captures stdout as the answer, renders stderr as transient activity, and
treats the exit code as the outcome. The JSONL session is authoritative. A
failed turn stays failed even if its output sounds confident.

This boundary is what lets later screens compose rather than accrete:

- Build configures and observes `ply`; it does not copy its loop.
- Procedures come from `brief`, not a hidden prompt library. Selected skill
  names are visible, and Ask records the exact composed prompt per turn.
- Evaluation runs named checks and mutation suites; it does not ask the model
  if it won.
- Learning admits only `hone` output from verified recoveries.
- Approval and confinement will be `may` and `cage`, not booleans in UI
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

The whole first screen is one conversation. There is no dashboard before the
work and no command palette full of promises.

- The transcript is primary and scrollable.
- The composer is always visible.
- `ctrl+s` sends; `enter` remains available for writing requirements.
- `esc` interrupts a running process.
- `ctrl+c` interrupts first and quits only when idle.
- `f1` shows the complete keyboard contract.
- The active model, process state, and durable session path are visible.

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

One TUI invocation owns one explicit session path. It never changes ask's
`current` pointer and never guesses which global conversation the user meant.
That makes two workspaces, two terminals, and replay all unsurprising.

Saved conversations are discovered only by filename and filesystem metadata.
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

1. **Done:** make the ask process boundary, cancellation, resize behaviour,
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

At every stage, deleting the TUI must leave a usable directory of files and
commands.
