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
- test the process boundary with a fake executable and the interface as a
  state machine; tests must not require a model, credentials, or a network;
- run `go test ./...` before reporting success.

The implemented product boundary is deliberately compositional. Open tasks
run through `ply` with one explicit Ask session; the full-shell or toolbox
grant, deferred unobserved actions, and the resulting typescript stay visible,
while unchecked exit zero is only a model stop, never a completed verdict.
Bench must not reconstruct Ply's turn protocol: the pinned Ply consumes one
action, returns its evidence, and decides when to ask again. `/ask` uses direct
`ask` for a no-tools turn in that same session; `bench run` and `bench ask`
preserve the same boundaries headlessly. `-m` and `/model` must propagate
through every model-backed stage. `draft new/check/build/prove` and
`hone -into` remain separate programs for promoted agent work, with visible
evidence and exit-status verdicts. The Skills workbench likewise composes
`brief ls/path/cat/new/lint`, passes sources to `ply` on stdin, and uses strict
lint as the only refinement verdict. Further task, build, evaluation, and
learning features belong here only as real compositions of existing filters,
added one verified vertical slice at a time.
