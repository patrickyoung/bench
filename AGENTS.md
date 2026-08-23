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
strictly matching sealed accepted receipt followed by a durable v2 result.
Neither a skill nor model output may set or infer that policy. `/continue` begins another
implementation attempt under the same admitted revision even when
the retained check already passes. `/contract off` and
`-contract=false` are the explicit direct-Ply compatibility seam. Work then
runs through `ply` with that one explicit Ask session; the full-shell or toolbox
grant, deferred unobserved actions, and the resulting typescript stay visible,
while unchecked exit zero is only a model stop, never a completed verdict.
Contracted compaction is rejected until successor lineage can be independently
verified; the direct compatibility seam retains Ply's existing compaction.
Consequential open questions and ungranted approvals stop before work; the
next contract request must bind the original intent, exact pending items, and
the user's answer rather than treating the answer as a replacement goal.
Bench must not reconstruct Ply's turn protocol: the pinned Ply consumes one
action, returns its evidence, and decides when to ask again. `/ask` uses direct
`ask` for a no-tools turn in that same session. The public `bench contract`
draft/revise/show/edit/import/accept/run commands must use the same store and controller API
as the TUI. Default `bench run` drafts and exits 2 without Ply; `-contract=false`
is the explicit immediate-work filter. `bench ask` remains direct Ask. `-m` and `/model` must propagate
through every model-backed stage. Open-task `-effort` passes literally through
Ply to Ask; Bench must not validate provider-specific effort names.
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
