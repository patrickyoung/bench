# Development guidance

`bench` is the terminal workbench for the small bench filters. It is a
control plane, never a second implementation of a filter.

When changing `bench`:

- invoke `ask`, `brief`, `ply`, and the rest as ordinary programs; do not
  import their internals or duplicate their policy;
- keep every boundary inspectable: arguments are argv, data is stdin,
  answers are stdout, progress is stderr, and outcome is the exit status;
- use explicit session and artifact paths. The TUI may render derived state,
  but an ordinary command must be able to inspect the durable source;
- never run user text through a shell. Shell syntax is data unless the user
  has explicitly chosen a shell-backed check;
- do not claim success from model prose. A successful build or evaluation is
  a check that exited zero;
- keep the interface usable at 80x24, without colour, and with the mouse
  unplugged;
- keep the task journey self-explanatory: understanding, workspace work, and
  verdict are distinct visible phases; live observations must not collapse to
  an opaque spinner, and consequential verdicts must remain in the transcript
  rather than existing only in a transient footer;
- make the next useful interaction discoverable in context. Help may be
  comprehensive, but ordinary work should not require memorizing it or reading
  a tutorial before the first outcome;
- test the process boundary with a fake executable and the interface as a
  state machine; tests must not require a model, credentials, or a network;
- run `go test ./...` before reporting success.

The implemented product boundary is deliberately compositional. Open tasks
first use native structured-output Ask turns to propose and revise the exact intent,
bounded read-only workspace inventory, configured verifier, selected Brief
skills, and piped evidence
into a visible canonical outcome contract. Bench validates, persists, and renders
that ordinary editable JSON but never turns model text into a shell command.
Proposal and revision cannot invoke Ply. A literal interactive `/contract accept`
or CLI `bench contract accept -expect DIGEST` publishes an immutable revision,
seals its admission, and only then repeats the exact bytes in Ply's first user message;
verifier receipts bind its envelope ID, so one Ask session replays contract,
actions, observations, answer, and verdict. Skills may shape domain procedure
and evidence expectations, but they never become evidence or completion
authority. After Ply stops, Bench seals a deterministic contract result.
Model-assigned `check` criteria are proposed coverage, never authority;
contracted work remains review-required and exits 2 until the interactive user
explicitly accepts pending criteria. `-check-all` and `/check all` are the one
operator-owned exception: they admit the exact configured check for every
parsed criterion, must be sealed before Ply, and may complete only from a
strictly matching sealed accepted receipt followed by a durable Review v2 or
Loop v3 result carrying the same judge-map and receipt bindings.
Neither a skill nor model output may set or infer that policy. `/continue` begins another
implementation attempt under the same admitted revision even when
the retained check already passes. User-facing autonomy is `auto`, `quick`,
`review`, or `loop`: Auto resolves once at the Bench front door, quick uses the
direct Ply seam, review requires contract admission, and Loop keeps one
bounded foreground Ply invocation pursuing an explicit check.
`/contract off` and `-contract=false` remain compatibility aliases. Work then
runs through `ply` with that one explicit Ask session; the full-shell or toolbox
grant, deferred unobserved actions, and the resulting typescript stay visible,
while unchecked exit zero is only a model stop, never a completed verdict.
Contracted compaction is rejected until successor lineage can be independently
verified; the direct compatibility seam retains Ply's existing compaction.
Consequential open questions and ungranted approvals stop before work; the
next contract request must bind the original intent, exact pending items, and
the user's answer rather than treating the answer as a replacement goal.
With approval policy off, those entries are the person's permission for the
described consequential scope. With every-action, they authorize preparation
only; exact execution still requires a separate May grant.
The optional operator-owned `every-action` policy must be visible in and bound
to the editable draft/admission, is forbidden in Quick, and may be removed only
by amendment and re-admission. It composes exact `may request` results through
Ply's pre-execution seam: parked/declined actions execute nothing and stop;
Bench verifies the sealed `ply.approval/v1` terminal receipt and uses result
v4. Caged admissions use `ply.approval/v2`; Cage wraps only the approved model
action, while Ask, May, Brief, Bench, and checks remain outside. A terminal
confinement failure must have sealed `ply.confinement/v1` evidence and result
v5 before Bench exposes exit 125. Bench must never interpret model/skill prose as approval or implement its
own yes path; the TUI yields to literal `may decide DIGEST`.
Bench must not reconstruct Ply's turn protocol: the pinned Ply consumes one
action, returns its evidence, and decides when to ask again. `/ask` uses direct
`ask` for a no-tools turn in that same session. The public `bench contract`
draft/revise/show/edit/import/accept/run commands must use the same store and controller API
as the TUI. Default `bench run` drafts and exits 2 without Ply; `-mode quick`
is the explicit immediate-work filter. `-mode loop` must remain one foreground
Ply invocation over an admitted contract: it requires a literal check, maps
omitted cycles to `-cycles 0`, keeps an explicit finite turn bound, and may
compose only Ply's public regular-file `-steer` seam. Do not add a Bench
supervisor, daemon, scheduler, hot socket, private turn protocol, or automatic
restart. `bench ask` remains direct Ask. `-m` and `/model` must propagate
through every model-backed stage. Open-task `-effort` passes literally through
Ply to Ask; Bench must not validate provider-specific effort names.
`-mode auto` is an explicit Bench-only routing delegation, never a new Ply or
contract mode. It performs one strict read-only Ask turn, applies fixed
controller floors, seals `bench.route/v1`, and only then dispatches the
selected existing Quick, Review, or Loop path. The default remains Review.
The route record is observation only: it grants no admission, approval,
criterion judgment, or completion. Behavior-affecting router System, Schema,
normalization, or rule changes require a new router identifier and frozen
corpus snapshot.
Live Auto experiments must remain developer-only compositions of public
Bench/Ask/Ply executables, fresh disposable workspaces, ordinary tracing
wrappers, verified Ask replay, and external hash-bound oracles. A harness must
never admit a generated contract: it may emit an exact script for a person to
run after inspection, and consequential route traps must receive no admission
path. Do not add telemetry, a private event store, or import product internals
to make the experiment easier. A live-run action adapter must pass Ply's public
literal `-action-shell` option on every worker invocation, retain
`PLY_ACTION_SHELL` for nesting, be snapshotted and hash-bound outside every
worker workspace, and leave the configured verifier on the host; adapter
identity is experiment provenance, never contract confinement or completion
authority.
Loop-only pursuit, budget, and stop metadata belongs in
`bench.contract-result/v3`; Review must keep its existing v1/v2 result bodies.
Interactive `/check -- COMMAND` is a literal per-outcome value: Bench shows
and forwards it but never evaluates it. Contracted review-required, not-done,
interruption, or verifier failure retains it; only the explicit direct-Ply
compatibility seam consumes a checked success. Ask replay checks session
integrity and ordering, not truth, producer identity, or task completion; UI
wording must keep those claims separate. Ambient full-shell check-all assumes a
non-malicious same-user worker. Use Draft's admitted May+Cage composition with
controller state outside Cage write roots when the worker itself is adversarial.
Design review offers `b` for the ordinary Draft build and `B` only for
`draft build -admitted`; retries must preserve which boundary the operator
selected. Bench does not imitate May admission or Cage confinement.
`draft new/check/build/prove` and
`hone -into` remain separate programs for promoted agent work, with visible
evidence and exit-status verdicts. The Skills workbench likewise composes
`brief ls/path/cat/new/lint`, passes sources to `ply` on stdin, and uses strict
lint as the only refinement verdict. Further task, build, evaluation, and
learning features belong here only as real compositions of existing filters,
added one verified vertical slice at a time.
